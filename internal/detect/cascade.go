// Package detect: this file assembles the individual pure detectors (trust
// gate, hard rules, behavioral checks) into a single decision pipeline. It
// wires the M3 detectors together but performs no wiring-level side effects
// itself (e.g. bumping trust); that happens in the caller (see M3 task 9).
package detect

import "github.com/stufently/telegram-antispam/internal/domain"

// Cascade holds the injected dependencies and config needed to run the full
// M3 detection pipeline over one message. It carries no mutable state of its
// own, so Decide is pure with respect to Cascade itself.
type Cascade struct {
	Trust           TrustSource
	Hist            History
	Rules           Rules
	Behavior        BehaviorCfg
	TrustThreshold  int
	DefaultAction   domain.Action
	DefaultScope    domain.Scope
	Bayes           BayesSource
	BayesScope      string
	BayesThreshold  float64
	BayesVocabGuess int
	BayesEnabled    bool
	// BayesBorderlineBand, when > 0, defines a band just below BayesThreshold
	// [threshold-band, threshold) in which a non-spam Bayes result is emitted
	// as a non-actionable "bayes_borderline" signal instead of silently
	// passing, so the wiring layer can consult the optional LLM stage (§5.4).
	BayesBorderlineBand float64
	Admins              AdminSource
	FakeAdmin           FakeAdminCfg
	Blocklist           BlocklistSource
	BlocklistEnabled    bool
}

// BlocklistSource reports whether a user ID is present in a global blocklist
// (e.g. shared cross-chat banlist). *blocklist.Blocklist satisfies this.
type BlocklistSource interface {
	Listed(userID int64) bool
}

// ReasonAdminLookupUnavailable is the Verdict.Reason (and signal name) the
// cascade emits when the admin list cannot be resolved and moderation is
// therefore deferred (§4 fail-safe). The wiring layer keys behavior off this
// value — it must not bump trust or log the message as merely "observed" —
// so it is exported rather than duplicated as a literal across packages.
const ReasonAdminLookupUnavailable = "admin_lookup_unavailable"

// Decide runs the cascade over one message: normalize, resolve trust, then
// try hard rules, fake-admin impersonation, behavioral checks, and Bayes in
// order (first hit wins). It returns the resulting Verdict and whether it
// is actionable.
//
// Decide is pure: it does not bump trust and performs no action of its own.
// The injected History may record state as part of CheckBehavior (e.g.
// duplicate/short-message counters) — that is an intentional part of those
// detectors' design (every message must be observed, not just actionable
// ones), not a side effect introduced by Decide.
func (c Cascade) Decide(m domain.Message, edited bool) (domain.Verdict, bool) {
	n := Normalize(m)

	// Spec §4: current chat admins (and the bot, when it is an admin) are
	// always immune — their messages are never moderated, independent of
	// trust. This gate MUST run before every detector. In particular the
	// fake-admin stage is designed for a non-admin sender (CheckFakeAdmin
	// deliberately does not exclude a sender's own name), so without this
	// gate a real admin would match their own admin-list entry at distance 0
	// and be flagged fake_admin on every message. The admin list is the same
	// short-TTL cache the fake-admin stage uses; it is fetched once here and
	// reused below. Failure to resolve this list is fail-safe: without a list
	// we can trust, we cannot prove the sender is not an admin, so the message
	// is deferred instead of being allowed to reach any punitive detector. A
	// stale list returned with that error still settles the positive case;
	// see the error branch below.
	var admins []AdminIdentity
	if c.Admins != nil {
		var err error
		admins, err = c.Admins.AdminIdentities(m.ChatID)
		if err != nil {
			// The source may hand back a stale list alongside the error.
			// That list is asymmetric evidence: a match proves the sender was
			// an admin as of the last good lookup (and a demotion would have
			// invalidated the cache), so honour it. Absence proves nothing —
			// an admin promoted during the outage would be missing — so
			// everyone else is deferred rather than exposed to a punitive
			// detector on unverified data.
			if isCurrentAdmin(admins, m.Sender.UserID) {
				return domain.Verdict{Action: domain.ActionNone}, false
			}
			// Deferring must not blind the behavioral windows. The wiring
			// layer has already committed this update as seen before Decide
			// runs, so a message dropped here is never reprocessed: without
			// this, a flood arriving during a Telegram hiccup would leave no
			// trace in the duplicate/short-message counters and stay
			// invisible even after the admin lookup recovers. Observation
			// only — evaluating a threshold is precisely what the deferral
			// forbids, and CheckBehavior's first-hit-wins ordering would in
			// any case skip windows on the way out.
			ObserveBehavior(c.Hist, m.ChatID, m.Sender.UserID, n, c.Behavior)
			return domain.Verdict{
				Action:  domain.ActionNone,
				Signals: []domain.Signal{{Name: ReasonAdminLookupUnavailable, Detail: err.Error()}},
				Reason:  ReasonAdminLookupUnavailable,
			}, false
		}
	}
	if isCurrentAdmin(admins, m.Sender.UserID) {
		return domain.Verdict{Action: domain.ActionNone}, false
	}

	trusted := IsTrusted(c.Trust, m.ChatID, m.Sender.UserID, c.TrustThreshold)

	// Blocklist stage: a hit on the global banlist is an authoritative,
	// cheap (local lookup) hard signal, checked ahead of the more expensive
	// rules/behavior/Bayes stages. Per spec §5.2 the blocklist applies to
	// EVERYONE regardless of trust — trust only skips the expensive semantic
	// stages, it never grants immunity; this is the explicit defense against
	// warmed-up and later-hijacked accounts (a locally-trusted account that
	// is subsequently sold and globally CAS/LOLS-banned must still be caught).
	// Admins are already immune via the §4 gate above, which runs first.
	if c.BlocklistEnabled && c.Blocklist != nil && c.Blocklist.Listed(m.Sender.UserID) {
		return c.actionable(domain.Signal{Name: "blocklist"}), true
	}

	if sig, hit := c.Rules.Check(n, trusted); hit {
		return c.actionable(sig), true
	}

	if c.FakeAdmin.Enabled && !trusted {
		if sig, hit := CheckFakeAdmin(m, admins, c.FakeAdmin); hit {
			return c.actionable(sig), true
		}
	}

	if sig, hit := CheckBehavior(c.Hist, m.ChatID, m.Sender.UserID, n, edited, c.Behavior); hit {
		return c.actionable(sig), true
	}

	if c.BayesEnabled && !trusted {
		tokens := Tokenize(n)
		ratio, scoreable, _ := bayesScore(c.Bayes, c.BayesScope, tokens, c.BayesVocabGuess)
		if scoreable {
			if ratio >= c.BayesThreshold {
				return c.actionable(domain.Signal{Name: "bayes"}), true
			}
			// Below threshold but close to it: hand the wiring layer a
			// non-actionable borderline signal so it can optionally ask the
			// LLM stage (§5.4). Non-actionable means behavior is unchanged
			// (trust still bumps, no action taken) unless the LLM is wired.
			if c.BayesBorderlineBand > 0 && c.BayesThreshold-ratio <= c.BayesBorderlineBand {
				return domain.Verdict{Action: domain.ActionNone, Signals: []domain.Signal{{Name: "bayes_borderline"}}}, false
			}
		}
	}

	return domain.Verdict{Action: domain.ActionNone}, false
}

// isCurrentAdmin reports whether userID is a current admin of the chat (spec
// §4 immunity). A zero userID (unknown sender) is never an admin, and admin
// identities with an unresolved UserID (0) never match. Admin-source errors
// are handled before this helper and defer moderation entirely.
func isCurrentAdmin(admins []AdminIdentity, userID int64) bool {
	if userID == 0 {
		return false
	}
	for _, a := range admins {
		if a.UserID == userID {
			return true
		}
	}
	return false
}

// actionable builds the Verdict for a matched signal using the cascade's
// configured default action and scope.
func (c Cascade) actionable(sig domain.Signal) domain.Verdict {
	return domain.Verdict{
		Action:     c.DefaultAction,
		Scope:      c.DefaultScope,
		Confidence: 1.0,
		Signals:    []domain.Signal{sig},
		Reason:     sig.Name,
	}
}
