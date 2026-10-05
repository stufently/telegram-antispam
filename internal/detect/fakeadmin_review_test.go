package detect

import (
	"errors"
	"testing"
	"time"

	"github.com/stufently/telegram-antispam/internal/domain"
)

func fakeAdminReviewFixture() (Cascade, domain.Message) {
	c := reviewCascade(0, 0)
	c.Hist = &fakeHistory{}
	c.DefaultAction = domain.ActionDeleteMute
	c.Admins = fakeAdminSrc{a: []AdminIdentity{{UserID: 500, Username: "boss_real", DisplayName: "Дмитрии", CustomTitle: "Владелец"}}}
	c.FakeAdmin = FakeAdminCfg{Enabled: true, MaxDistance: 1, MinFuzzyLen: 5}
	m := mediaMsg("обычный текст")
	m.Sender.DisplayName = "Дмитрий"
	return c, m
}

func TestFakeAdminDisplayNameOnlyIsReview(t *testing.T) {
	c, m := fakeAdminReviewFixture()
	if v, ok := c.Decide(m, false); ok || v.ReviewOnly {
		t.Fatalf("name-only match must leave Decide non-actionable: ok=%v verdict=%+v", ok, v)
	}
	v, ok := c.ReviewCandidate(m)
	if !ok || !v.ReviewOnly || v.Action != domain.ActionQuarantine || v.Reason != "fake_admin" || v.Confidence != 0 || v.Scope != c.DefaultScope {
		t.Fatalf("want fake_admin review verdict: ok=%v verdict=%+v", ok, v)
	}
	if len(v.Signals) != 1 || v.Signals[0].Name != "fake_admin" || v.Signals[0].Detail != "display_name~display_name admin_id=500 fuzzy" {
		t.Fatalf("unexpected signals: %+v", v.Signals)
	}
}

func assertFakeAdminSanction(t *testing.T, c Cascade, m domain.Message, detail string) {
	t.Helper()
	v, ok := c.Decide(m, false)
	if !ok || v.ReviewOnly || v.Action != c.DefaultAction || v.Reason != "fake_admin" || v.Confidence != 1 || len(v.Signals) != 1 || v.Signals[0].Detail != detail {
		t.Fatalf("want sanction with %q: ok=%v verdict=%+v", detail, ok, v)
	}
}

func TestFakeAdminUsernameMatchSanctions(t *testing.T) {
	for _, tc := range []struct{ name, username, display, detail string }{
		{"username fuzzy", "boss_rea1", "unrelated", "username~username admin_id=500 fuzzy"},
		{"display name exact", "", "BOSS_REAL", "display_name~username admin_id=500 exact"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, m := fakeAdminReviewFixture()
			m.Sender.Username, m.Sender.DisplayName = tc.username, tc.display
			assertFakeAdminSanction(t, c, m, tc.detail)
		})
	}
}

func TestFakeAdminCustomTitleMatchSanctions(t *testing.T) {
	c, m := fakeAdminReviewFixture()
	m.Sender.DisplayName = "ВладелеЦц"
	assertFakeAdminSanction(t, c, m, "display_name~custom_title admin_id=500 fuzzy")
}

func TestFakeAdminStrongMatchWinsOverNameMatch(t *testing.T) {
	a := AdminIdentity{UserID: 500, DisplayName: "Дмитрии"}
	b := AdminIdentity{UserID: 600, Username: "дмитрий"}
	for _, admins := range [][]AdminIdentity{{a, b}, {b, a}} {
		c, m := fakeAdminReviewFixture()
		c.Admins = fakeAdminSrc{a: admins}
		assertFakeAdminSanction(t, c, m, "display_name~username admin_id=600 exact")
	}
}

func TestFakeAdminSenderTagSanctions(t *testing.T) {
	for _, name := range []string{"unrelated", "Дмитрий"} {
		t.Run(name, func(t *testing.T) {
			c, m := fakeAdminReviewFixture()
			c.FakeAdmin.SuspiciousTags = []string{"admin"}
			m.Sender.DisplayName, m.SenderTag = name, "AdMiN"
			assertFakeAdminSanction(t, c, m, domain.FakeAdminTagDetail)
		})
	}
}

func TestFakeAdminNameMatchSanctionOption(t *testing.T) {
	c, m := fakeAdminReviewFixture()
	c.FakeAdmin.NameMatchSanction = true
	assertFakeAdminSanction(t, c, m, "display_name~display_name admin_id=500 fuzzy")
	if v, ok := c.ReviewCandidate(m); ok {
		t.Fatalf("sanction option must not also return name review: %+v", v)
	}
}

func TestFakeAdminNameReviewGuards(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*Cascade)
	}{
		{"trusted", func(c *Cascade) { c.Trust = &fakeTrustSource{counts: map[[2]int64]int{{-100, 7}: 5}} }},
		{"admin", func(c *Cascade) {
			c.Admins = fakeAdminSrc{a: []AdminIdentity{{UserID: 7, DisplayName: "Дмитрий"}}}
		}},
		{"lookup error", func(c *Cascade) {
			c.Admins = fakeAdminSrc{a: []AdminIdentity{{UserID: 500, DisplayName: "Дмитрий"}}, err: errors.New("unavailable")}
		}},
		{"disabled", func(c *Cascade) { c.FakeAdmin.Enabled = false }},
		{"no source", func(c *Cascade) { c.Admins = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, m := fakeAdminReviewFixture()
			tc.setup(&c)
			if v, ok := c.ReviewCandidate(m); ok {
				t.Fatalf("guard allowed review: %+v", v)
			}
			// Trust and admin guards must suppress the existing review signal too.
			if tc.name == "trusted" || tc.name == "admin" || tc.name == "lookup error" {
				c.CaptionMinLen = 20
				m.Text, m.MediaKinds = "", []string{"photo"}
				if v, ok := c.ReviewCandidate(m); ok {
					t.Fatalf("guard allowed combined review: %+v", v)
				}
			}
		})
	}
}

func TestFakeAdminNameMatchDoesNotPreemptLaterStages(t *testing.T) {
	t.Run("bayes spam", func(t *testing.T) {
		c, m := fakeAdminReviewFixture()
		c.BayesEnabled = true
		c.BayesVocabGuess = 1000
		c.Bayes = fakeBayes{spam: map[string]int{"casino": 50}, ham: map[string]int{"casino": 0}, c: BayesCounts{SpamDocs: 100, HamDocs: 100, SpamTokenTotal: 500, HamTokenTotal: 500}}
		m.Text = "casino casino casino"
		if v, ok := c.Decide(m, false); !ok || v.Reason != "bayes" {
			t.Fatalf("name match preempted Bayes: ok=%v verdict=%+v", ok, v)
		}
	})
	t.Run("LLM borderline", func(t *testing.T) {
		c, m := fakeAdminReviewFixture()
		c.BayesEnabled, c.BayesBorderlineBand = true, 0.5
		c.Bayes = emptyBayes{}
		if v, ok := c.Decide(m, false); ok || len(v.Signals) != 1 || v.Signals[0].Name != "bayes_borderline" {
			t.Fatalf("name match blocked LLM signal: ok=%v verdict=%+v", ok, v)
		}
	})
	t.Run("behavior", func(t *testing.T) {
		c, m := fakeAdminReviewFixture()
		c.Hist = &fakeHistory{defaultDupCount: 5}
		c.Behavior = BehaviorCfg{DupThreshold: 5, DupWindow: time.Minute}
		if v, ok := c.Decide(m, false); !ok || v.Reason != "duplicate_flood" {
			t.Fatalf("name match preempted behavior: ok=%v verdict=%+v", ok, v)
		}
	})
}

func TestFakeAdminNameReviewWithCaptionlessMedia(t *testing.T) {
	c, m := fakeAdminReviewFixture()
	c.CaptionMinLen = 20
	m.Text, m.MediaKinds = "", []string{"photo"}
	v, ok := c.ReviewCandidate(m)
	if !ok || !v.ReviewOnly || v.Action != domain.ActionQuarantine || v.Confidence != 0 || v.Reason != "captionless_media" || len(v.Signals) != 2 || v.Signals[0].Name != "captionless_media" || v.Signals[1].Name != "fake_admin" || v.Signals[1].Detail != "display_name~display_name admin_id=500 fuzzy" {
		t.Fatalf("want ordered combined review: ok=%v verdict=%+v", ok, v)
	}
}

func TestFakeAdminClassifyPriorityAndCompatibility(t *testing.T) {
	for _, tc := range []struct {
		name     string
		sender   domain.Sender
		admins   []AdminIdentity
		tag      string
		strength FakeAdminStrength
		detail   string
	}{
		{"first strong pair", domain.Sender{Username: "boss", DisplayName: "owner"}, []AdminIdentity{{UserID: 1, Username: "boss", CustomTitle: "owner"}, {UserID: 2, Username: "boss"}}, "admin", FakeAdminStrong, "username~username admin_id=1 exact"},
		{"custom title before display name", domain.Sender{Username: "owner"}, []AdminIdentity{{UserID: 1, DisplayName: "owner", CustomTitle: "OWNER"}}, "", FakeAdminStrong, "username~custom_title admin_id=1 exact"},
		{"later sender strong", domain.Sender{Username: "person", DisplayName: "owner"}, []AdminIdentity{{UserID: 1, DisplayName: "person", Username: "owner"}}, "", FakeAdminStrong, "display_name~username admin_id=1 exact"},
		{"first weak pair", domain.Sender{Username: "person", DisplayName: "PERSON"}, []AdminIdentity{{UserID: 1, DisplayName: "person"}, {UserID: 2, DisplayName: "person"}}, "", FakeAdminNameOnly, "username~display_name admin_id=1 exact"},
		{"empty identities", domain.Sender{}, []AdminIdentity{{UserID: 1}}, "", FakeAdminNoMatch, ""},
		{"unrelated", domain.Sender{DisplayName: "unrelated"}, []AdminIdentity{{UserID: 1, DisplayName: "person"}}, "member", FakeAdminNoMatch, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := FakeAdminCfg{Enabled: true, MaxDistance: 1, MinFuzzyLen: 5, SuspiciousTags: []string{"admin"}}
			m := domain.Message{Sender: tc.sender, SenderTag: tc.tag}
			sig, s := ClassifyFakeAdmin(m, tc.admins, cfg)
			if s != tc.strength || sig.Detail != tc.detail {
				t.Fatalf("got strength=%v sig=%+v", s, sig)
			}
			wrapped, hit := CheckFakeAdmin(m, tc.admins, cfg)
			if hit != (tc.strength != FakeAdminNoMatch) || wrapped != sig {
				t.Fatalf("wrapper changed hit semantics: hit=%v sig=%+v", hit, wrapped)
			}
			cfg.Enabled = false
			if sig, s := ClassifyFakeAdmin(m, tc.admins, cfg); s != FakeAdminNoMatch || sig != (domain.Signal{}) {
				t.Fatalf("disabled classifier matched: %v %+v", s, sig)
			}
		})
	}
}
