package watch

import (
	"context"
	"testing"

	"github.com/stufently/telegram-antispam/internal/detect"
	"github.com/stufently/telegram-antispam/internal/telegram/fake"
)

// fakeStore is a scripted IdentityStore that records how many times
// UpsertIdentity was called and always returns the same scripted values.
type fakeStore struct {
	calls        int
	prevUsername string
	prevDisplay  string
	changed      bool
	err          error
}

func (s *fakeStore) UpsertIdentity(chatID, userID int64, username, displayName string) (string, string, bool, error) {
	s.calls++
	return s.prevUsername, s.prevDisplay, s.changed, s.err
}

// fakeAdmins is a scripted detect.AdminSource.
type fakeAdmins struct {
	a []detect.AdminIdentity
}

func (f fakeAdmins) AdminIdentities(chatID int64) []detect.AdminIdentity {
	return f.a
}

func TestObserveAdminLikeRenameSendsOneNotice(t *testing.T) {
	store := &fakeStore{prevUsername: "bob", prevDisplay: "Bob", changed: true}
	admins := fakeAdmins{a: []detect.AdminIdentity{{Username: "owner"}}}
	port := fake.New()

	w := &MemberWatcher{
		Store:       store,
		Admins:      admins,
		AdminChatID: 999,
		Port:        port,
		MaxDistance: 1,
		Enabled:     true,
	}

	e := MemberEvent{ChatID: 1, UserID: 2, Username: "0wner", DisplayName: "0wner"}
	if err := w.Observe(context.Background(), e); err != nil {
		t.Fatalf("Observe returned error: %v", err)
	}

	count := 0
	for _, c := range port.Calls() {
		if c == "SendAdmin" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 SendAdmin call, got %d", count)
	}
	if store.calls != 1 {
		t.Fatalf("expected UpsertIdentity called once, got %d", store.calls)
	}
}

func TestObserveBenignRenameSendsNoNotice(t *testing.T) {
	store := &fakeStore{prevUsername: "bob", prevDisplay: "Bob", changed: true}
	admins := fakeAdmins{a: []detect.AdminIdentity{{Username: "owner"}}}
	port := fake.New()

	w := &MemberWatcher{
		Store:       store,
		Admins:      admins,
		AdminChatID: 999,
		Port:        port,
		MaxDistance: 1,
		Enabled:     true,
	}

	e := MemberEvent{ChatID: 1, UserID: 2, Username: "alice", DisplayName: "Alice"}
	if err := w.Observe(context.Background(), e); err != nil {
		t.Fatalf("Observe returned error: %v", err)
	}

	count := 0
	for _, c := range port.Calls() {
		if c == "SendAdmin" {
			count++
		}
	}
	if count != 0 {
		t.Fatalf("expected 0 SendAdmin calls for benign rename, got %d", count)
	}
}

func TestObserveNewRowSendsNoNotice(t *testing.T) {
	// changed=false simulates a brand-new row (first sighting) even though
	// the incoming name would otherwise match an admin.
	store := &fakeStore{changed: false}
	admins := fakeAdmins{a: []detect.AdminIdentity{{Username: "owner"}}}
	port := fake.New()

	w := &MemberWatcher{
		Store:       store,
		Admins:      admins,
		AdminChatID: 999,
		Port:        port,
		MaxDistance: 1,
		Enabled:     true,
	}

	e := MemberEvent{ChatID: 1, UserID: 2, Username: "owner", DisplayName: "Owner"}
	if err := w.Observe(context.Background(), e); err != nil {
		t.Fatalf("Observe returned error: %v", err)
	}

	count := 0
	for _, c := range port.Calls() {
		if c == "SendAdmin" {
			count++
		}
	}
	if count != 0 {
		t.Fatalf("expected 0 SendAdmin calls for a new row, got %d", count)
	}
}

func TestObserveSelfRenameByAdminSendsNoNotice(t *testing.T) {
	store := &fakeStore{prevUsername: "boss", prevDisplay: "Boss", changed: true}
	admins := fakeAdmins{a: []detect.AdminIdentity{{UserID: 7, Username: "owner"}}}
	port := fake.New()

	w := &MemberWatcher{
		Store:       store,
		Admins:      admins,
		AdminChatID: 999,
		Port:        port,
		MaxDistance: 1,
		Enabled:     true,
	}

	// Admin 7 renames themselves to a name that matches their own
	// admin-list entry — not impersonation.
	e := MemberEvent{ChatID: 1, UserID: 7, Username: "0wner", DisplayName: "0wner"}
	if err := w.Observe(context.Background(), e); err != nil {
		t.Fatalf("Observe returned error: %v", err)
	}

	count := 0
	for _, c := range port.Calls() {
		if c == "SendAdmin" {
			count++
		}
	}
	if count != 0 {
		t.Fatalf("expected 0 SendAdmin calls for admin self-rename, got %d", count)
	}
}

func TestObserveOtherUserRenameToMatchAdminSendsOneNotice(t *testing.T) {
	store := &fakeStore{prevUsername: "bob", prevDisplay: "Bob", changed: true}
	admins := fakeAdmins{a: []detect.AdminIdentity{{UserID: 7, Username: "owner"}}}
	port := fake.New()

	w := &MemberWatcher{
		Store:       store,
		Admins:      admins,
		AdminChatID: 999,
		Port:        port,
		MaxDistance: 1,
		Enabled:     true,
	}

	// A different user (8) renames to match admin 7's identity —
	// impersonation, should still notice.
	e := MemberEvent{ChatID: 1, UserID: 8, Username: "0wner", DisplayName: "0wner"}
	if err := w.Observe(context.Background(), e); err != nil {
		t.Fatalf("Observe returned error: %v", err)
	}

	count := 0
	for _, c := range port.Calls() {
		if c == "SendAdmin" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 SendAdmin call for other-user rename, got %d", count)
	}
}

func TestObserveDisabledStillRecordsButSendsNoNotice(t *testing.T) {
	store := &fakeStore{prevUsername: "bob", prevDisplay: "Bob", changed: true}
	admins := fakeAdmins{a: []detect.AdminIdentity{{Username: "owner"}}}
	port := fake.New()

	w := &MemberWatcher{
		Store:       store,
		Admins:      admins,
		AdminChatID: 999,
		Port:        port,
		MaxDistance: 1,
		Enabled:     false,
	}

	e := MemberEvent{ChatID: 1, UserID: 2, Username: "owner", DisplayName: "Owner"}
	if err := w.Observe(context.Background(), e); err != nil {
		t.Fatalf("Observe returned error: %v", err)
	}

	count := 0
	for _, c := range port.Calls() {
		if c == "SendAdmin" {
			count++
		}
	}
	if count != 0 {
		t.Fatalf("expected 0 SendAdmin calls when disabled, got %d", count)
	}
	if store.calls != 1 {
		t.Fatalf("expected UpsertIdentity still called once when disabled, got %d", store.calls)
	}
}
