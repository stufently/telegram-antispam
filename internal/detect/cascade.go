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
}

// Decide runs the cascade over one message: normalize, resolve trust, then
// try hard rules and behavioral checks in order (first hit wins). It
// returns the resulting Verdict and whether it is actionable.
//
// Decide is pure: it does not bump trust and performs no action of its own.
// The injected History may record state as part of CheckBehavior (e.g.
// duplicate/short-message counters) — that is an intentional part of those
// detectors' design (every message must be observed, not just actionable
// ones), not a side effect introduced by Decide.
func (c Cascade) Decide(m domain.Message, edited bool) (domain.Verdict, bool) {
	n := Normalize(m)
	trusted := IsTrusted(c.Trust, m.ChatID, m.Sender.UserID, c.TrustThreshold)

	if sig, hit := c.Rules.Check(n, trusted); hit {
		return c.actionable(sig), true
	}

	if sig, hit := CheckBehavior(c.Hist, m.ChatID, m.Sender.UserID, n, edited, c.Behavior); hit {
		return c.actionable(sig), true
	}

	if c.BayesEnabled && !trusted {
		tokens := Tokenize(n)
		if spam, _, _ := BayesIsSpam(c.Bayes, c.BayesScope, tokens, c.BayesVocabGuess, c.BayesThreshold); spam {
			return c.actionable(domain.Signal{Name: "bayes"}), true
		}
	}

	return domain.Verdict{Action: domain.ActionNone}, false
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
