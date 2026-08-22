package detect

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stufently/telegram-antispam/internal/domain"
)

func TestCascadeDecide_DenyStopwordActionable(t *testing.T) {
	trust := &fakeTrustSource{counts: map[[2]int64]int{}}
	hist := &fakeHistory{}
	c := Cascade{
		Trust:          trust,
		Hist:           hist,
		Rules:          Rules{DenyStopwords: []string{"spamword"}},
		Behavior:       BehaviorCfg{},
		TrustThreshold: 5,
		DefaultAction:  domain.ActionDeleteMute,
		DefaultScope:   domain.ScopeChat,
	}
	m := domain.Message{
		ChatID: -100,
		Sender: domain.Sender{UserID: 1},
		Text:   "buy spamword now",
	}

	v, actionable := c.Decide(m, false)

	if !actionable {
		t.Fatal("expected actionable verdict for deny stopword")
	}
	if v.Action != domain.ActionDeleteMute {
		t.Errorf("expected DefaultAction delete_mute, got %v", v.Action)
	}
	if v.Scope != domain.ScopeChat {
		t.Errorf("expected DefaultScope chat, got %v", v.Scope)
	}
	if v.Confidence != 1.0 {
		t.Errorf("expected confidence 1.0, got %v", v.Confidence)
	}
	if len(v.Signals) != 1 || v.Signals[0].Name != "deny_stopword" {
		t.Errorf("expected single deny_stopword signal, got %+v", v.Signals)
	}
	if v.Reason != "deny_stopword" {
		t.Errorf("expected reason deny_stopword, got %q", v.Reason)
	}
}

func TestCascadeDecide_BenignShortFromTrustedNotActionable(t *testing.T) {
	trust := &fakeTrustSource{counts: map[[2]int64]int{{-100, 1}: 10}}
	hist := &fakeHistory{}
	c := Cascade{
		Trust: trust,
		Hist:  hist,
		Rules: Rules{},
		Behavior: BehaviorCfg{
			DupThreshold:        3,
			DupWindow:           time.Minute,
			ShortLen:            10,
			ShortFloodThreshold: 5,
			ShortWindow:         30 * time.Second,
		},
		TrustThreshold: 5,
		DefaultAction:  domain.ActionDeleteMute,
		DefaultScope:   domain.ScopeChat,
	}
	m := domain.Message{
		ChatID: -100,
		Sender: domain.Sender{UserID: 1},
		Text:   "hi",
	}

	v, actionable := c.Decide(m, false)

	if actionable {
		t.Fatalf("expected not actionable for benign short message from trusted user, got %+v", v)
	}
	if v.Action != domain.ActionNone {
		t.Errorf("expected ActionNone, got %v", v.Action)
	}
}

func TestCascadeDecide_UntrustedLinkActionable(t *testing.T) {
	trust := &fakeTrustSource{counts: map[[2]int64]int{}} // count 0 < threshold: untrusted
	hist := &fakeHistory{}
	c := Cascade{
		Trust:          trust,
		Hist:           hist,
		Rules:          Rules{BlockLinksForUntrusted: true},
		Behavior:       BehaviorCfg{},
		TrustThreshold: 5,
		DefaultAction:  domain.ActionMute,
		DefaultScope:   domain.ScopeGlobal,
	}
	m := domain.Message{
		ChatID: -200,
		Sender: domain.Sender{UserID: 2},
		Text:   "check this out https://spam.example/x",
	}

	v, actionable := c.Decide(m, false)

	if !actionable {
		t.Fatal("expected actionable verdict for untrusted link")
	}
	if len(v.Signals) != 1 || v.Signals[0].Name != "link_from_untrusted" {
		t.Errorf("expected link_from_untrusted signal, got %+v", v.Signals)
	}
	if v.Action != domain.ActionMute || v.Scope != domain.ScopeGlobal {
		t.Errorf("expected configured default action/scope, got %v/%v", v.Action, v.Scope)
	}
}

func TestCascadeDecide_BayesSpamActionableForUntrustedOnly(t *testing.T) {
	bayes := fakeBayes{
		spam: map[string]int{"casino": 50},
		ham:  map[string]int{"casino": 0},
		c:    BayesCounts{SpamDocs: 100, HamDocs: 100, SpamTokenTotal: 500, HamTokenTotal: 500},
	}
	hist := &fakeHistory{}
	m := domain.Message{
		ChatID: -300,
		Sender: domain.Sender{UserID: 3},
		Text:   "casino casino casino",
	}

	untrustedTrust := &fakeTrustSource{counts: map[[2]int64]int{}}
	c := Cascade{
		Trust:           untrustedTrust,
		Hist:            hist,
		Rules:           Rules{},
		Behavior:        BehaviorCfg{},
		TrustThreshold:  5,
		DefaultAction:   domain.ActionDeleteMute,
		DefaultScope:    domain.ScopeChat,
		Bayes:           bayes,
		BayesScope:      globalScope,
		BayesThreshold:  0.0,
		BayesVocabGuess: 1000,
		BayesEnabled:    true,
	}

	v, actionable := c.Decide(m, false)

	if !actionable {
		t.Fatalf("expected actionable verdict for bayes spam signal, got %+v", v)
	}
	if len(v.Signals) != 1 || v.Signals[0].Name != "bayes" {
		t.Errorf("expected bayes signal, got %+v", v.Signals)
	}
	if v.Reason != "bayes" {
		t.Errorf("expected reason bayes, got %q", v.Reason)
	}

	trustedTrust := &fakeTrustSource{counts: map[[2]int64]int{{-300, 3}: 10}}
	c.Trust = trustedTrust

	v2, actionable2 := c.Decide(m, false)

	if actionable2 {
		t.Fatalf("expected bayes to be skipped for trusted sender, got %+v", v2)
	}
	if v2.Action != domain.ActionNone {
		t.Errorf("expected ActionNone for trusted sender, got %v", v2.Action)
	}
}

type fakeAdminSrc struct {
	a   []AdminIdentity
	err error
}

func (f fakeAdminSrc) AdminIdentities(int64) ([]AdminIdentity, error) { return f.a, f.err }

func TestCascadeDecide_FakeAdminUntrustedOnly(t *testing.T) {
	c := Cascade{
		Trust:          &fakeTrustSource{counts: map[[2]int64]int{}},
		Hist:           &fakeHistory{},
		Rules:          Rules{},
		Behavior:       BehaviorCfg{},
		TrustThreshold: 5,
		Admins:         fakeAdminSrc{a: []AdminIdentity{{Username: "owner"}}},
		FakeAdmin:      FakeAdminCfg{Enabled: true, MaxDistance: 1},
		DefaultAction:  domain.ActionDeleteMute,
		DefaultScope:   domain.ScopeGlobal,
	}
	m := domain.Message{ChatID: 1, Sender: domain.Sender{UserID: 2, Username: "0wner"}}

	v, actionable := c.Decide(m, false)
	if !actionable || v.Reason != "fake_admin" {
		t.Fatalf("untrusted fake-admin should flag: ok=%v v=%+v", actionable, v)
	}

	c.Trust = &fakeTrustSource{counts: map[[2]int64]int{{1, 2}: 10}}
	if _, actionable := c.Decide(m, false); actionable {
		t.Fatal("trusted sender must skip fake-admin")
	}
}

// TestCascadeDecide_CurrentAdminImmune guards the spec §4 invariant that a
// current chat admin's message is never moderated. Without the immunity gate
// a real admin matches their own admin-list entry at distance 0 and is
// flagged fake_admin on every message (self-perpetuating, since trust only
// bumps on non-actionable verdicts).
func TestCascadeDecide_CurrentAdminImmune(t *testing.T) {
	// Admin user 42 with an identity that would otherwise self-match the
	// fake-admin near-match check.
	admins := fakeAdminSrc{a: []AdminIdentity{{UserID: 42, Username: "alice_admin", DisplayName: "Alice"}}}
	c := Cascade{
		Trust:          &fakeTrustSource{counts: map[[2]int64]int{}}, // untrusted
		Hist:           &fakeHistory{},
		Rules:          Rules{},
		Behavior:       BehaviorCfg{},
		TrustThreshold: 5,
		Admins:         admins,
		FakeAdmin:      FakeAdminCfg{Enabled: true, MaxDistance: 1, SuspiciousTags: []string{"admin"}},
		DefaultAction:  domain.ActionDeleteMute,
		DefaultScope:   domain.ScopeGlobal,
	}
	// The admin posts, with their own admin name AND a suspicious tag — both
	// would flag a non-admin. As a current admin they must be immune.
	m := domain.Message{ChatID: 1, Sender: domain.Sender{UserID: 42, Username: "alice_admin", DisplayName: "Alice"}, SenderTag: "Admin"}
	if v, ok := c.Decide(m, false); ok {
		t.Fatalf("current admin must be immune, got actionable %+v", v)
	}

	// A non-admin impersonator (different UserID) with the same near-match
	// name is still flagged — immunity is keyed on UserID, not name.
	imp := domain.Message{ChatID: 1, Sender: domain.Sender{UserID: 99, Username: "alice_admln", DisplayName: "Alice"}}
	if _, ok := c.Decide(imp, false); !ok {
		t.Fatal("non-admin impersonator must still be flagged")
	}
}

func TestCascadeDecide_AdminLookupFailureDefersAllModeration(t *testing.T) {
	c := Cascade{
		Trust:            &fakeTrustSource{counts: map[[2]int64]int{}},
		Hist:             &fakeHistory{},
		Rules:            Rules{DenyStopwords: []string{"spamword"}},
		TrustThreshold:   5,
		Admins:           fakeAdminSrc{err: errors.New("telegram unavailable")},
		DefaultAction:    domain.ActionBan,
		DefaultScope:     domain.ScopeGlobal,
		Blocklist:        fakeBlocklist{ids: map[int64]bool{42: true}},
		BlocklistEnabled: true,
	}
	m := domain.Message{ChatID: 1, Sender: domain.Sender{UserID: 42}, Text: "spamword"}

	v, actionable := c.Decide(m, false)
	if actionable || v.IsActionable() {
		t.Fatalf("admin lookup failure must suppress punitive detectors, got %+v", v)
	}
	if v.Reason != ReasonAdminLookupUnavailable || len(v.Signals) != 1 {
		t.Fatalf("expected explicit deferred signal, got %+v", v)
	}
}

// Deferring must not create a hole in the behavioral window. The wiring layer
// marks the update seen before Decide runs, so a message dropped here is never
// reprocessed: if the deferral also skipped CheckBehavior, a flood arriving
// during a Telegram hiccup would leave no trace in the duplicate counters and
// stay invisible even after the admin lookup recovered.
func TestCascadeDecide_AdminLookupFailureStillRecordsBehavior(t *testing.T) {
	hist := &fakeHistory{}
	c := Cascade{
		Trust:          &fakeTrustSource{counts: map[[2]int64]int{}},
		Hist:           hist,
		Rules:          Rules{},
		TrustThreshold: 5,
		Admins:         fakeAdminSrc{err: errors.New("telegram unavailable")},
		Behavior:       BehaviorCfg{DupThreshold: 3, DupWindow: time.Minute},
		DefaultAction:  domain.ActionBan,
		DefaultScope:   domain.ScopeGlobal,
	}
	m := domain.Message{ChatID: 1, Sender: domain.Sender{UserID: 42}, Text: "buy my thing"}

	v, actionable := c.Decide(m, false)
	if actionable || v.Reason != ReasonAdminLookupUnavailable {
		t.Fatalf("expected a deferred verdict, got %+v", v)
	}
	if len(hist.recordedDups) != 1 {
		t.Fatalf("deferred message must still be recorded in the dup window, recorded %d", len(hist.recordedDups))
	}
}

// The recording above is for its side effect only: a duplicate flood that
// crosses the threshold while the admin list is unavailable must still not
// produce an actionable verdict, because we cannot prove the sender is not
// an admin.
func TestCascadeDecide_AdminLookupFailureNeverActsOnRecordedBehavior(t *testing.T) {
	hist := &fakeHistory{defaultDupCount: 99}
	c := Cascade{
		Trust:          &fakeTrustSource{counts: map[[2]int64]int{}},
		Hist:           hist,
		Rules:          Rules{},
		TrustThreshold: 5,
		Admins:         fakeAdminSrc{err: errors.New("telegram unavailable")},
		Behavior:       BehaviorCfg{DupThreshold: 3, DupWindow: time.Minute},
		DefaultAction:  domain.ActionBan,
		DefaultScope:   domain.ScopeGlobal,
	}
	m := domain.Message{ChatID: 1, Sender: domain.Sender{UserID: 42}, Text: "buy my thing"}

	v, actionable := c.Decide(m, false)
	if actionable || v.IsActionable() {
		t.Fatalf("a deferred lookup must never produce an action, got %+v", v)
	}
	if v.Reason != ReasonAdminLookupUnavailable {
		t.Fatalf("expected the deferred reason, got %+v", v)
	}
}

type fakeBlocklist struct{ ids map[int64]bool }

func (f fakeBlocklist) Listed(id int64) bool { return f.ids[id] }

// TestCascadeDecide_BlocklistAppliesToEveryone guards the blocklist cascade
// stage: a global-banlist hit is authoritative and applies to EVERYONE
// regardless of trust (spec §5.2 — trust never grants blocklist immunity;
// the warmed-up/hijacked-account defense). Only current admins are exempt,
// via the §4 admin-immunity gate that runs ahead of it.
func TestCascadeDecide_BlocklistAppliesToEveryone(t *testing.T) {
	const chatID = int64(1)

	baseCascade := func() Cascade {
		return Cascade{
			Hist:             &fakeHistory{},
			Rules:            Rules{},
			Behavior:         BehaviorCfg{},
			TrustThreshold:   5,
			DefaultAction:    domain.ActionDeleteMute,
			DefaultScope:     domain.ScopeGlobal,
			Blocklist:        fakeBlocklist{ids: map[int64]bool{2: true}},
			BlocklistEnabled: true,
		}
	}

	// Untrusted listed sender: actionable with reason "blocklist".
	c := baseCascade()
	c.Trust = &fakeTrustSource{counts: map[[2]int64]int{}}
	m := domain.Message{ChatID: chatID, Sender: domain.Sender{UserID: 2}, Text: "hello"}

	v, actionable := c.Decide(m, false)
	if !actionable {
		t.Fatalf("expected actionable verdict for listed untrusted sender, got %+v", v)
	}
	if v.Reason != "blocklist" {
		t.Errorf("expected reason blocklist, got %q", v.Reason)
	}

	// Same listed sender but TRUSTED: still flagged. Spec §5.2 — trust never
	// grants blocklist immunity (the warmed-up/hijacked-account defense).
	c2 := baseCascade()
	c2.Trust = &fakeTrustSource{counts: map[[2]int64]int{{chatID, 2}: 10}}
	v2, actionable2 := c2.Decide(m, false)
	if !actionable2 {
		t.Fatalf("expected trusted listed sender to STILL be flagged (spec §5.2), got %+v", v2)
	}
	if v2.Reason != "blocklist" {
		t.Errorf("expected reason blocklist for trusted listed sender, got %q", v2.Reason)
	}

	// Listed sender who is also an admin: admin-immunity gate wins.
	c3 := baseCascade()
	c3.Trust = &fakeTrustSource{counts: map[[2]int64]int{}}
	c3.Admins = fakeAdminSrc{a: []AdminIdentity{{UserID: 2}}}
	if v3, actionable3 := c3.Decide(m, false); actionable3 {
		t.Fatalf("expected admin listed sender to be immune, got %+v", v3)
	}

	// Non-listed untrusted sender: falls through the blocklist stage.
	c4 := baseCascade()
	c4.Trust = &fakeTrustSource{counts: map[[2]int64]int{}}
	m4 := domain.Message{ChatID: chatID, Sender: domain.Sender{UserID: 3}, Text: "hello"}
	if v4, actionable4 := c4.Decide(m4, false); actionable4 {
		t.Fatalf("expected non-listed sender to fall through, got %+v", v4)
	}
}

func TestCascadeDecide_BayesBorderlineSignal(t *testing.T) {
	// Ham-leaning tokens: ratio sits below threshold. With a wide band the
	// cascade must emit a non-actionable "bayes_borderline" signal; with the
	// band off (0) it must stay silent (ActionNone, no signal).
	bayes := fakeBayes{
		spam: map[string]int{"hello": 1},
		ham:  map[string]int{"hello": 80},
		c:    BayesCounts{SpamDocs: 100, HamDocs: 100, SpamTokenTotal: 500, HamTokenTotal: 500},
	}
	m := domain.Message{ChatID: -400, Sender: domain.Sender{UserID: 7}, Text: "hello hello"}
	base := Cascade{
		Trust:           &fakeTrustSource{counts: map[[2]int64]int{}},
		Hist:            &fakeHistory{},
		TrustThreshold:  5,
		DefaultAction:   domain.ActionDeleteMute,
		DefaultScope:    domain.ScopeChat,
		Bayes:           bayes,
		BayesScope:      globalScope,
		BayesThreshold:  0.0,
		BayesVocabGuess: 1000,
		BayesEnabled:    true,
	}

	// Band off: no BORDERLINE signal. A diagnostic "bayes_pass" signal is
	// still emitted — it carries the score that explains the pass, and
	// nothing keys behavior off it (it is non-actionable, and only the
	// borderline name reaches the LLM stage).
	v0, actionable0 := base.Decide(m, false)
	if actionable0 {
		t.Fatalf("band off: expected non-actionable, got %+v", v0)
	}
	if len(v0.Signals) != 1 || v0.Signals[0].Name != "bayes_pass" {
		t.Fatalf("band off: expected one bayes_pass signal, got %+v", v0.Signals)
	}
	if !strings.Contains(v0.Signals[0].Detail, "ratio=") {
		t.Fatalf("band off: diagnostic must carry the ratio, got %q", v0.Signals[0].Detail)
	}

	// Band on and wide: borderline signal, non-actionable.
	withBand := base
	withBand.BayesBorderlineBand = 100.0
	v, actionable := withBand.Decide(m, false)
	if actionable {
		t.Fatalf("borderline must be non-actionable, got %+v", v)
	}
	if len(v.Signals) != 1 || v.Signals[0].Name != "bayes_borderline" {
		t.Fatalf("expected bayes_borderline signal, got %+v", v.Signals)
	}

	// Trusted sender skips Bayes entirely — no borderline signal.
	withBand.Trust = &fakeTrustSource{counts: map[[2]int64]int{{-400, 7}: 10}}
	if v3, _ := withBand.Decide(m, false); len(v3.Signals) != 0 {
		t.Fatalf("trusted sender must skip borderline, got %+v", v3.Signals)
	}
}

// A stale admin list returned alongside a lookup error still settles the
// positive case: a sender on it was an admin as of the last good fetch, and a
// demotion would have invalidated the cache. Immunity, not deferral.
func TestCascadeDecide_StaleAdminListStillGrantsImmunity(t *testing.T) {
	hist := &fakeHistory{defaultDupCount: 99}
	c := Cascade{
		Trust:          &fakeTrustSource{counts: map[[2]int64]int{}},
		Hist:           hist,
		Rules:          Rules{DenyStopwords: []string{"spamword"}},
		TrustThreshold: 5,
		Admins: fakeAdminSrc{
			a:   []AdminIdentity{{UserID: 42, Username: "boss"}},
			err: errors.New("telegram unavailable"),
		},
		Behavior:      BehaviorCfg{DupThreshold: 3, DupWindow: time.Minute},
		DefaultAction: domain.ActionBan,
		DefaultScope:  domain.ScopeGlobal,
	}
	m := domain.Message{ChatID: 1, Sender: domain.Sender{UserID: 42}, Text: "spamword"}

	v, actionable := c.Decide(m, false)
	if actionable || v.IsActionable() {
		t.Fatalf("a stale-listed admin must stay immune, got %+v", v)
	}
	if v.Reason == ReasonAdminLookupUnavailable {
		t.Fatalf("a resolved admin must not be reported as deferred, got %+v", v)
	}
	// Admins are not fed to the behavioral windows on the normal path either.
	if len(hist.recordedDups) != 0 {
		t.Fatalf("an immune admin must not be recorded, recorded %d", len(hist.recordedDups))
	}
}

// Absence from a stale list proves nothing — an admin promoted during the
// outage would be missing from it — so everyone not on it is deferred rather
// than exposed to a punitive detector on unverified data.
func TestCascadeDecide_StaleAdminListDefersEveryoneElse(t *testing.T) {
	hist := &fakeHistory{}
	c := Cascade{
		Trust:          &fakeTrustSource{counts: map[[2]int64]int{}},
		Hist:           hist,
		Rules:          Rules{DenyStopwords: []string{"spamword"}},
		TrustThreshold: 5,
		Admins: fakeAdminSrc{
			a:   []AdminIdentity{{UserID: 42, Username: "boss"}},
			err: errors.New("telegram unavailable"),
		},
		Behavior:         BehaviorCfg{DupThreshold: 3, DupWindow: time.Minute},
		Blocklist:        fakeBlocklist{ids: map[int64]bool{7: true}},
		BlocklistEnabled: true,
		DefaultAction:    domain.ActionBan,
		DefaultScope:     domain.ScopeGlobal,
	}
	m := domain.Message{ChatID: 1, Sender: domain.Sender{UserID: 7}, Text: "spamword"}

	v, actionable := c.Decide(m, false)
	if actionable || v.IsActionable() {
		t.Fatalf("a sender absent from a stale list must be deferred, got %+v", v)
	}
	if v.Reason != ReasonAdminLookupUnavailable {
		t.Fatalf("expected the deferred reason, got %+v", v)
	}
	if len(hist.recordedDups) != 1 {
		t.Fatalf("a deferred message must still be observed, recorded %d", len(hist.recordedDups))
	}
}

// emptyBayes is a BayesSource with no corpus at all: the state a freshly
// deployed bot is in before anyone runs `tg-antispam import`.
type emptyBayes struct{}

func (emptyBayes) TokenCounts(string, []string) (map[string]int, map[string]int, error) {
	return map[string]int{}, map[string]int{}, nil
}
func (emptyBayes) Totals(string) (BayesCounts, error) { return BayesCounts{}, nil }

func TestUntrainedBayesStillReachesTheLLMStage(t *testing.T) {
	base := Cascade{
		Trust:           &fakeTrustSource{counts: map[[2]int64]int{}},
		TrustThreshold:  5,
		Bayes:           emptyBayes{},
		BayesScope:      globalScope,
		BayesEnabled:    true,
		BayesThreshold:  1.0,
		BayesVocabGuess: 5000,
		DefaultAction:   domain.ActionBan,
		Hist:            &fakeHistory{},
	}
	msg := domain.Message{ChatID: -100, MessageID: 1, Text: "быстрый заработок пиши в личку", Sender: domain.Sender{UserID: 7, Kind: domain.SenderUser}}

	// LLM wired (band > 0): an untrained corpus must still produce the
	// borderline signal, otherwise the paid stage never runs on a fresh
	// deploy — exactly when it is needed most.
	withLLM := base
	withLLM.BayesBorderlineBand = 0.5
	v, actionable := withLLM.Decide(msg, false)
	if actionable {
		t.Fatal("untrained bayes must never be actionable on its own")
	}
	if len(v.Signals) != 1 || v.Signals[0].Name != "bayes_borderline" {
		t.Fatalf("signals = %+v, want one bayes_borderline", v.Signals)
	}

	// LLM not wired (band 0): nothing changes, no signal, no cost.
	v, actionable = base.Decide(msg, false)
	if actionable || len(v.Signals) != 0 {
		t.Fatalf("without the LLM stage: actionable=%v signals=%+v, want a silent pass", actionable, v.Signals)
	}
}

func TestUntrainedBayesSkipsEmptyTokenMessages(t *testing.T) {
	c := Cascade{
		Trust: &fakeTrustSource{counts: map[[2]int64]int{}}, TrustThreshold: 5,
		Bayes: emptyBayes{}, BayesScope: globalScope, BayesEnabled: true,
		BayesThreshold: 1.0, BayesVocabGuess: 5000, BayesBorderlineBand: 0.5,
		DefaultAction: domain.ActionBan, Hist: &fakeHistory{},
	}
	// A captionless photo still tokenizes to meta features like "len:short",
	// but it carries no text to judge — sending it to a paid API would buy
	// an opinion about the empty string.
	v, actionable := c.Decide(domain.Message{ChatID: -100, MessageID: 1, Sender: domain.Sender{UserID: 7, Kind: domain.SenderUser}}, false)
	if actionable || len(v.Signals) != 0 {
		t.Fatalf("empty message: actionable=%v signals=%+v, want a silent pass", actionable, v.Signals)
	}
}

// failingBayes reports an error on every read, standing in for a corrupted or
// locked database rather than an untrained corpus.
type failingBayes struct{}

func (failingBayes) TokenCounts(string, []string) (map[string]int, map[string]int, error) {
	return nil, nil, errors.New("db read failed")
}
func (failingBayes) Totals(string) (BayesCounts, error) {
	return BayesCounts{}, errors.New("db read failed")
}

func TestBayesReadErrorDoesNotBecomeAnLLMCall(t *testing.T) {
	c := Cascade{
		Trust: &fakeTrustSource{counts: map[[2]int64]int{}}, TrustThreshold: 5,
		Bayes: failingBayes{}, BayesScope: globalScope, BayesEnabled: true,
		BayesThreshold: 2.0, BayesVocabGuess: 5000, BayesBorderlineBand: 4.5,
		DefaultAction: domain.ActionBan, Hist: &fakeHistory{},
	}
	// A broken database must not read as "untrained": that would route every
	// message from every newcomer to a paid API until someone noticed.
	v, actionable := c.Decide(domain.Message{
		ChatID: -100, MessageID: 1, Text: "какое-то обычное сообщение",
		Sender: domain.Sender{UserID: 7, Kind: domain.SenderUser},
	}, false)
	if actionable {
		t.Fatal("a bayes read error must never be actionable")
	}
	if len(v.Signals) != 0 {
		t.Fatalf("signals = %+v, want none (no borderline signal on a read error)", v.Signals)
	}
}

// globalScope is the resolver every cascade test uses: these tests are about
// detection, not about which corpus a deployment picks.
func globalScope(int64) string { return "global" }

// A small corpus scores real spam far below the threshold, so a band-gated
// LLM stage never sees it. BayesAlwaysBorderline is the switch that says
// "ask anyway"; these cases pin what it does and does not change.
func TestCascadeAlwaysBorderline(t *testing.T) {
	trust := &fakeTrustSource{counts: map[[2]int64]int{}}
	bayes := fakeBayes{
		spam: map[string]int{"hello": 1},
		ham:  map[string]int{"hello": 80},
		c:    BayesCounts{SpamDocs: 100, HamDocs: 100, SpamTokenTotal: 500, HamTokenTotal: 500},
	}
	m := domain.Message{ChatID: -400, Sender: domain.Sender{UserID: 7}, Text: "hello hello"}

	base := Cascade{
		Trust:           trust,
		Hist:            &fakeHistory{},
		TrustThreshold:  5,
		DefaultAction:   domain.ActionDeleteMute,
		DefaultScope:    domain.ScopeChat,
		Bayes:           bayes,
		BayesScope:      globalScope,
		BayesThreshold:  2.0,
		BayesVocabGuess: 1000,
		BayesEnabled:    true,
		// Deliberately narrow: this is the configuration that let the miss
		// through, and the flag must rescue it without touching the band.
		BayesBorderlineBand: 0.5,
	}

	v, actionable := base.Decide(m, false)
	if actionable || len(v.Signals) != 1 || v.Signals[0].Name != "bayes_pass" {
		t.Fatalf("narrow band alone must pass silently, got actionable=%v %+v", actionable, v.Signals)
	}

	always := base
	always.BayesAlwaysBorderline = true
	v, actionable = always.Decide(m, false)
	if actionable {
		t.Fatalf("borderline must stay non-actionable, got %+v", v)
	}
	if len(v.Signals) != 1 || v.Signals[0].Name != "bayes_borderline" {
		t.Fatalf("flag must route the message to the LLM stage, got %+v", v.Signals)
	}

	// The trust gate still bounds the cost: a trusted sender never reaches
	// the Bayes stage at all, flag or not.
	trust.counts[[2]int64{-400, 7}] = 5
	if v, actionable := always.Decide(m, false); actionable || len(v.Signals) != 0 {
		t.Fatalf("trusted sender must not reach the paid stage, got %+v", v)
	}
}
