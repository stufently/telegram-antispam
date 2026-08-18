package detect

import (
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
		BayesScope:      "global",
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
