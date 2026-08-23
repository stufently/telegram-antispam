package detect

import (
	"errors"
	"testing"

	"github.com/stufently/telegram-antispam/internal/domain"
)

// reviewCascade builds a cascade whose ONLY enabled feature is the
// captionless-media review, so a hit can only come from that check.
func reviewCascade(captionMinLen int, trustCount int) Cascade {
	return Cascade{
		Trust:          &fakeTrustSource{counts: map[[2]int64]int{{-100, 7}: trustCount}},
		TrustThreshold: 5,
		DefaultScope:   domain.ScopeGlobal,
		CaptionMinLen:  captionMinLen,
	}
}

func mediaMsg(text string, kinds ...string) domain.Message {
	return domain.Message{
		ChatID:     -100,
		MessageID:  1,
		Sender:     domain.Sender{Kind: domain.SenderUser, UserID: 7},
		Text:       text,
		MediaKinds: kinds,
	}
}

func TestReviewCandidateFlagsCaptionlessMediaFromNewcomer(t *testing.T) {
	v, ok := reviewCascade(20, 0).ReviewCandidate(mediaMsg("", "photo"))
	if !ok {
		t.Fatal("a newcomer's captionless photo must be surfaced for review")
	}
	if !v.ReviewOnly {
		t.Fatal("the verdict must be review-only, never enforced")
	}
	if v.Action != domain.ActionQuarantine {
		t.Fatalf("action = %q, want quarantine so the audit row matches what happened", v.Action)
	}
	if len(v.Signals) != 1 || v.Signals[0].Name != "captionless_media" {
		t.Fatalf("signals = %+v", v.Signals)
	}
	if v.Signals[0].Detail != "kinds=photo len=0" {
		t.Fatalf("detail = %q, want the kinds and length a moderator needs", v.Signals[0].Detail)
	}
}

func TestReviewCandidateIgnoresCurrentAdmin(t *testing.T) {
	c := reviewCascade(20, 0)
	c.Admins = fakeAdminSrc{a: []AdminIdentity{{UserID: 7}}}
	if _, ok := c.ReviewCandidate(mediaMsg("", "photo")); ok {
		t.Fatal("an admin posting a screenshot must not get a card offering to mute themselves")
	}
}

func TestReviewCandidateDeclinesWhenTheAdminListIsUnknown(t *testing.T) {
	c := reviewCascade(20, 0)
	c.Admins = fakeAdminSrc{err: errors.New("telegram unreachable")}
	if _, ok := c.ReviewCandidate(mediaMsg("", "photo")); ok {
		t.Fatal("with no admin list we cannot prove the sender is not an admin: fail safe")
	}
}

func TestReviewCandidateIgnoresTrustedSender(t *testing.T) {
	if _, ok := reviewCascade(20, 5).ReviewCandidate(mediaMsg("", "photo")); ok {
		t.Fatal("a trusted member posting a picture is ordinary chat use")
	}
}

func TestReviewCandidateIgnoresRealCaption(t *testing.T) {
	long := "вот фотография кальяна который я продаю, пишите если интересно"
	if _, ok := reviewCascade(20, 0).ReviewCandidate(mediaMsg(long, "photo")); ok {
		t.Fatal("a captioned photo goes through the text detectors, not review")
	}
}

func TestReviewCandidateDisabledByDefault(t *testing.T) {
	if _, ok := reviewCascade(0, 0).ReviewCandidate(mediaMsg("", "photo")); ok {
		t.Fatal("caption_min_len 0 must keep the stage off")
	}
}

func TestReviewCandidateIgnoresPlainText(t *testing.T) {
	if _, ok := reviewCascade(20, 0).ReviewCandidate(mediaMsg("ок")); ok {
		t.Fatal("a short text message carries no attachment and is not this stage's business")
	}
}

// The stage must not preempt the detectors that CAN read the message: it is
// a fallback the caller runs after them, so Decide itself never returns it.
func TestDecideNeverReturnsReviewVerdict(t *testing.T) {
	c := reviewCascade(20, 0)
	v, ok := c.Decide(mediaMsg("", "photo"), false)
	if ok || v.ReviewOnly {
		t.Fatalf("Decide must stay silent on captionless media, got ok=%v verdict=%+v", ok, v)
	}
}
