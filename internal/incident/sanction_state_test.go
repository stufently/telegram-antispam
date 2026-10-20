package incident

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stufently/telegram-antispam/internal/domain"
	"github.com/stufently/telegram-antispam/internal/telegram"
	"github.com/stufently/telegram-antispam/internal/telegram/fake"
)

type holdStatePort struct {
	*fake.Fake
	started, release chan struct{}
}

func (p *holdStatePort) RestrictMember(ctx context.Context, chat, user int64, perms telegram.Perms, until int64) error {
	close(p.started)
	select {
	case <-p.release:
	case <-ctx.Done():
		return ctx.Err()
	}
	return p.Fake.RestrictMember(ctx, chat, user, perms, until)
}

func TestNoEvidenceAllowedSanctionRemainsInFlight(t *testing.T) {
	for _, signal := range []string{"manual_spam", "blocklist"} {
		t.Run(signal, func(t *testing.T) {
			db, f, _ := newOverrideRig(t)
			f.CopyErr = errors.New("copy unavailable")
			p := &holdStatePort{Fake: f, started: make(chan struct{}), release: make(chan struct{})}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			m := New(p, db, 999)
			inc := manualIncident(signal, domain.ActionDeleteMute)
			done := make(chan error, 1)
			go func() { done <- m.Handle(ctx, inc) }()
			select {
			case <-p.started:
			case err := <-done:
				t.Fatalf("did not reach sanction: %v", err)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			defer func() {
				close(p.release)
				if err := <-done; err != nil {
					t.Error(err)
				}
			}()
			var state string
			if err := db.Read().QueryRow("SELECT state FROM incidents WHERE chat_id=?", ovChat).Scan(&state); err != nil {
				t.Fatal(err)
			}
			if state != "pending" {
				t.Errorf("in-flight no-evidence enforcement is %s, want pending", state)
			}
			protected, err := db.SanctionSince(ovChat, ovUser, 0)
			if err != nil || !protected {
				t.Errorf("captcha not protected: %v %v", protected, err)
			}
		})
	}
}

func TestCompletedPreSanctionFailuresAreNotActedAndRetryable(t *testing.T) {
	for _, stage := range []string{"notify", "restrict"} {
		t.Run(stage, func(t *testing.T) {
			db, f, m := newOverrideRig(t)
			if stage == "notify" {
				f.SendAdminErr = errors.New("no card")
			} else {
				f.RestrictErr = errors.New("no rights")
			}
			if err := m.Handle(context.Background(), autoIncident(false)); err == nil {
				t.Fatal("expected failure")
			}
			var id int64
			var state string
			if err := db.Read().QueryRow("SELECT id,state FROM incidents WHERE chat_id=?", ovChat).Scan(&id, &state); err != nil {
				t.Fatal(err)
			}
			if state != "not_acted" {
				t.Errorf("state=%s, want terminal not_acted", state)
			}
			protected, err := db.SanctionSince(ovChat, ovUser, 0)
			if err != nil || protected {
				t.Errorf("finished failure protects captcha: %v %v", protected, err)
			}
			// Explicit later moderation must be able to retry this known no-sanction row.
			claimed, _, _, err := db.ClaimManualOverride(id)
			if err != nil || !claimed {
				t.Errorf("not_acted cannot be retried: %v %v", claimed, err)
			}
		})
	}
}
