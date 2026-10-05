package config

import (
	"os"
	"strings"
	"testing"
)

func TestFakeAdminNameMatchDefaultsAndValidates(t *testing.T) {
	const base = "bot_token: test\nadmin_chat_id: -1\nchats:\n  mode: auto\naction: delete_mute\n"
	for _, tc := range []struct{ key, want string }{
		{"", FakeAdminNameMatchReview},
		{"detection:\n  fake_admin_name_match: ''\n", FakeAdminNameMatchReview},
		{"detection:\n  fake_admin_name_match: review\n", FakeAdminNameMatchReview},
		{"detection:\n  fake_admin_name_match: sanction\n", FakeAdminNameMatchSanction},
	} {
		c, err := Parse([]byte(base + tc.key))
		if err != nil {
			t.Fatal(err)
		}
		if c.Detection.FakeAdminNameMatch != tc.want {
			t.Errorf("got %q, want %q", c.Detection.FakeAdminNameMatch, tc.want)
		}
	}
	if _, err := Parse([]byte(base + "detection:\n  fake_admin_name_match: ban\n")); err == nil || !strings.Contains(err.Error(), "detection.fake_admin_name_match") {
		t.Fatalf("invalid option error = %v", err)
	}
}

func TestFakeAdminNameMatchBackwardCompatible(t *testing.T) {
	if err := UnknownKeys([]byte("detection:\n  fake_admin_name_match: review\n")); err != nil {
		t.Fatal(err)
	}
	c, err := Parse([]byte(`bot_token: test
admin_chat_id: -1
chats:
  mode: auto
action: delete_mute
detection:
  fake_admin_enabled: true
  fake_admin_max_distance: 1
  fake_admin_min_fuzzy_len: 5
  trust_threshold: 5
  media_caption_min_len: 20
  meaningful_min_len: 10
  review_keyboard: true
`))
	if err != nil {
		t.Fatal(err)
	}
	d := c.Detection
	if d.FakeAdminNameMatch != FakeAdminNameMatchReview || !*d.FakeAdminEnabled || d.FakeAdminMaxDistance != 1 || d.FakeAdminMinFuzzyLen != 5 || len(d.FakeAdminSuspiciousTags) != 10 || *d.TrustThreshold != 5 || d.MediaCaptionMinLen != 20 || d.MeaningfulMinLen != 10 || !d.ReviewKeyboard {
		t.Fatalf("legacy config defaults changed: %+v", d)
	}
	b, err := os.ReadFile("../../config.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	c, err = Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	if c.Detection.FakeAdminNameMatch != FakeAdminNameMatchReview {
		t.Fatalf("example mode=%q", c.Detection.FakeAdminNameMatch)
	}
}
