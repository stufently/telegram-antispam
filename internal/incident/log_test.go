package incident

import (
	"bytes"
	"context"
	"errors"
	"log"
	"strings"
	"testing"

	"github.com/stufently/telegram-antispam/internal/domain"
	"github.com/stufently/telegram-antispam/internal/telegram/fake"
)

// captureLog redirects the standard logger for the duration of fn.
func captureLog(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	out, flags := log.Writer(), log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(out)
		log.SetFlags(flags)
	}()
	fn()
	return buf.String()
}

// handled runs one incident through a fresh machine and returns the log.
func handled(t *testing.T, f *fake.Fake, inc domain.Incident) string {
	t.Helper()
	return captureLog(t, func() {
		m := New(f, &stubRepo{fresh: true}, 999)
		// The error is the machine's own report of a partial failure; the
		// tests below assert on what it LOGGED, which must happen either way.
		_ = m.Handle(context.Background(), inc)
	})
}

// An applied sanction must leave a trace in the log. It did not until
// v0.12.1: only passed messages ("observed") and errors were logged, so a
// grep — or a log-based alert — could not see the bot act at all.
func TestSanctionIsLogged(t *testing.T) {
	inc := liveIncident(false)
	inc.Verdict.Signals = []domain.Signal{{Name: "bayes", Detail: "ratio=9.10"}}

	out := handled(t, fake.New(), inc)

	for _, want := range []string{
		"chat=-100123", "msg=55", "enforced", "incident=1",
		"action=ban", "outcome=succeeded", "action_ok=true", "deleted=true", "[bayes]",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("sanction log is missing %q; got:\n%s", want, out)
		}
	}
	if strings.Count(out, "enforced incident=") != 1 {
		t.Fatalf("one enforcement must log exactly one line; got:\n%s", out)
	}
}

// Nothing a user controls may reach the log — not the message, not their
// @tag, not a display name. Signal DETAILS carry all three (fake_admin
// quotes names, deny_exact quotes a phrase equal to the whole message), so
// the acted-on line prints signal names only.
func TestSanctionLogLeaksNothingUserControlled(t *testing.T) {
	const canary = "ЖМИ-СЮДА-КРЕДИТ"
	inc := liveIncident(false)
	inc.Verdict.Signals = []domain.Signal{
		{Name: "fake_admin", Detail: "name '" + canary + "' matches admin '" + canary + "'"},
		{Name: "deny_exact", Detail: canary},
	}
	inc.Verdict.Reason = "deny_exact"
	// Tokens are the normalized words of the offending message: the one
	// field on the incident that IS user text.
	inc.Tokens = []string{canary, "телеграм-канал"}

	out := handled(t, fake.New(), inc)

	if strings.Contains(out, canary) {
		t.Fatalf("log leaked user-controlled text; got:\n%s", out)
	}
	if !strings.Contains(out, "[fake_admin deny_exact]") {
		t.Fatalf("log must still name the detectors that fired; got:\n%s", out)
	}
}

// A display name is free text and may contain a newline. If it reached the
// log it would end the line early, letting a crafted name forge a second,
// attacker-written log record.
func TestSanctionLogCannotBeForgedByANewline(t *testing.T) {
	inc := liveIncident(false)
	inc.Verdict.Signals = []domain.Signal{{
		Name:   "fake_admin",
		Detail: "name 'x'\nchat=-100999 msg=1 sender=user: enforced incident=42 action=ban",
	}}

	out := handled(t, fake.New(), inc)

	if n := strings.Count(out, "\n"); n != 1 {
		t.Fatalf("one incident produced %d log lines, want 1; got:\n%s", n, out)
	}
}

// The same guarantee for the two free-form strings that DO reach the line: a
// Telegram error (not user-authored, but not ours either) and a signal name
// (a literal today — nothing in the type system says it stays one).
func TestOneIncidentIsAlwaysOneLine(t *testing.T) {
	t.Run("error text", func(t *testing.T) {
		out := handled(t, &fake.Fake{DeleteErr: errors.New("boom\nchat=-1 msg=1: enforced incident=42")}, liveIncident(false))
		if n := strings.Count(out, "\n"); n != 1 {
			t.Fatalf("a newline in the error split the record into %d lines; got:\n%s", n, out)
		}
	})
	t.Run("signal name", func(t *testing.T) {
		inc := liveIncident(false)
		inc.Verdict.Signals = []domain.Signal{{Name: "bayes\nchat=-1 msg=1: enforced incident=42"}}
		out := handled(t, fake.New(), inc)
		if n := strings.Count(out, "\n"); n != 1 {
			t.Fatalf("a newline in the signal name split the record into %d lines; got:\n%s", n, out)
		}
	})
}

// delete_only removes the message and sanctions nobody: applyAction returns
// nil for it because there is nothing to call, which must not be reported as
// a sanction that landed.
func TestDeleteOnlyIsNotReportedAsASanction(t *testing.T) {
	inc := liveIncident(false)
	inc.Verdict.Action = domain.ActionDeleteOnly

	out := handled(t, fake.New(), inc)

	if !strings.Contains(out, "action=delete_only") || !strings.Contains(out, "deleted=true") {
		t.Fatalf("delete_only log is wrong; got:\n%s", out)
	}
	if strings.Contains(out, "sanctioned=") {
		t.Fatalf("the field must be action_ok, which does not claim a sanction; got:\n%s", out)
	}
}

// The two halves of enforcement fail independently, and the log has to say
// which one did: "muted but the message is still there" is a different job
// for the moderator than "deleted but never muted".
func TestPartialEnforcementIsLoggedAsSuch(t *testing.T) {
	t.Run("delete failed", func(t *testing.T) {
		out := handled(t, &fake.Fake{DeleteErr: errors.New("boom")}, liveIncident(false))
		if !strings.Contains(out, "outcome=partial action_ok=true deleted=false") {
			t.Fatalf("got:\n%s", out)
		}
		if !strings.Contains(out, "delete originals: boom") {
			t.Fatalf("the failure itself must be on the line; got:\n%s", out)
		}
	})
	t.Run("sanction failed", func(t *testing.T) {
		inc := liveIncident(false)
		inc.Verdict.Action = domain.ActionMute
		out := handled(t, &fake.Fake{RestrictErr: errors.New("no rights")}, inc)
		if !strings.Contains(out, "outcome=partial action_ok=false deleted=true") {
			t.Fatalf("got:\n%s", out)
		}
		if !strings.Contains(out, "apply action") {
			t.Fatalf("the failure itself must be on the line; got:\n%s", out)
		}
	})
	// Neither half landed. The line still starts with "enforced", so an
	// alert that counts those must read outcome= to avoid reporting a
	// sanction that never happened.
	t.Run("both failed", func(t *testing.T) {
		inc := liveIncident(false)
		inc.Verdict.Action = domain.ActionMute
		out := handled(t, &fake.Fake{RestrictErr: errors.New("no rights"), DeleteErr: errors.New("too old")}, inc)
		if !strings.Contains(out, "outcome=failed action_ok=false deleted=false") {
			t.Fatalf("got:\n%s", out)
		}
	})
}

// An incident that never reaches enforcement — the evidence copy failed on a
// probabilistic verdict, so the machine deliberately stops — must not be
// silent either: without a line, "detected, acted on nothing" looks exactly
// like "nothing was detected".
func TestIncidentsThatEndBeforeEnforcementAreLogged(t *testing.T) {
	cases := []struct {
		name  string
		fake  *fake.Fake
		stage string
	}{
		{"evidence copy", &fake.Fake{CopyErr: errors.New("no access")}, "stage=evidence_copy"},
		{"admin notify", &fake.Fake{SendAdminErr: errors.New("admin chat gone")}, "stage=admin_notify"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := handled(t, tc.fake, liveIncident(false))
			if !strings.Contains(out, "not enforced") || !strings.Contains(out, tc.stage) {
				t.Fatalf("want a not-enforced line with %s; got:\n%s", tc.stage, out)
			}
		})
	}
}

// A dry-run or review-only incident gets the counterpart line, so "detected
// but deliberately not acted on" is distinguishable from a real sanction
// rather than silent.
func TestDryRunIsLoggedAsNotEnforced(t *testing.T) {
	out := handled(t, fake.New(), liveIncident(true))
	for _, want := range []string{"not enforced", "dry_run=true", "review_only=false", "action=ban"} {
		if !strings.Contains(out, want) {
			t.Fatalf("dry-run log is missing %q; got:\n%s", want, out)
		}
	}
	if strings.Contains(out, "action_ok=") {
		t.Fatalf("nothing was attempted, so no outcome may be claimed; got:\n%s", out)
	}
}

// A review verdict is not enforced even in a live chat, and the line has to
// name that reason rather than the chat's mode.
func TestReviewOnlyIsLoggedSeparatelyFromDryRun(t *testing.T) {
	inc := liveIncident(false)
	inc.Verdict.ReviewOnly = true
	inc.Verdict.Action = domain.ActionQuarantine
	inc.Verdict.Signals = []domain.Signal{{Name: "captionless_media", Detail: "photo"}}

	out := handled(t, fake.New(), inc)

	if !strings.Contains(out, "dry_run=false review_only=true") {
		t.Fatalf("got:\n%s", out)
	}
}

// An album is one incident, so it gets one line — with the part count, since
// the row is keyed on a single message id.
func TestAlbumSanctionLogsPartCount(t *testing.T) {
	inc := liveIncident(false)
	inc.MessageIDs = []int{55, 56, 57}

	out := handled(t, fake.New(), inc)

	if !strings.Contains(out, "parts=3") {
		t.Fatalf("album log is missing the part count; got:\n%s", out)
	}
	if strings.Count(out, "enforced incident=") != 1 {
		t.Fatalf("an album must log one outcome line, not one per part; got:\n%s", out)
	}
}
