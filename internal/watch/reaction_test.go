package watch

import (
	"context"
	"errors"
	"testing"

	"github.com/stufently/telegram-antispam/internal/telegram/fake"
)

type fakeSpammers struct {
	known map[int64]bool
	err   error
}

func (f *fakeSpammers) HasSpamIncident(chatID, userID int64) (bool, error) {
	return f.known[userID], f.err
}

func TestReactionCleanerObserveDeletesKnownSpammerReaction(t *testing.T) {
	port := fake.New()
	spammers := &fakeSpammers{known: map[int64]bool{2: true}}
	rc := &ReactionCleaner{Spammers: spammers, Port: port, Enabled: true}

	err := rc.Observe(context.Background(), ReactionEvent{ChatID: 1, MessageID: 9, UserID: 2, Added: true})
	if err != nil {
		t.Fatalf("Observe returned error: %v", err)
	}

	calls := port.Calls()
	if len(calls) != 1 || calls[0] != "DeleteMessageReaction" {
		t.Fatalf("calls=%v want exactly one DeleteMessageReaction", calls)
	}
	want := struct {
		Chat      int64
		MessageID int
		UserID    int64
	}{1, 9, 2}
	if port.LastReactionDelete.Chat != want.Chat || port.LastReactionDelete.MessageID != want.MessageID || port.LastReactionDelete.UserID != want.UserID {
		t.Fatalf("LastReactionDelete=%+v want %+v", port.LastReactionDelete, want)
	}
}

func TestReactionCleanerObserveIgnoresUnknownUser(t *testing.T) {
	port := fake.New()
	spammers := &fakeSpammers{known: map[int64]bool{2: true}}
	rc := &ReactionCleaner{Spammers: spammers, Port: port, Enabled: true}

	err := rc.Observe(context.Background(), ReactionEvent{ChatID: 1, MessageID: 9, UserID: 3, Added: true})
	if err != nil {
		t.Fatalf("Observe returned error: %v", err)
	}

	if calls := port.Calls(); len(calls) != 0 {
		t.Fatalf("calls=%v want none", calls)
	}
}

func TestReactionCleanerObserveIgnoresRemovedReaction(t *testing.T) {
	port := fake.New()
	spammers := &fakeSpammers{known: map[int64]bool{2: true}}
	rc := &ReactionCleaner{Spammers: spammers, Port: port, Enabled: true}

	err := rc.Observe(context.Background(), ReactionEvent{ChatID: 1, MessageID: 9, UserID: 2, Added: false})
	if err != nil {
		t.Fatalf("Observe returned error: %v", err)
	}

	if calls := port.Calls(); len(calls) != 0 {
		t.Fatalf("calls=%v want none", calls)
	}
}

func TestReactionCleanerObserveDisabledIsNoop(t *testing.T) {
	port := fake.New()
	spammers := &fakeSpammers{known: map[int64]bool{2: true}}
	rc := &ReactionCleaner{Spammers: spammers, Port: port, Enabled: false}

	err := rc.Observe(context.Background(), ReactionEvent{ChatID: 1, MessageID: 9, UserID: 2, Added: true})
	if err != nil {
		t.Fatalf("Observe returned error: %v", err)
	}

	if calls := port.Calls(); len(calls) != 0 {
		t.Fatalf("calls=%v want none", calls)
	}
}

func TestReactionCleanerObservePropagatesSpammerSourceError(t *testing.T) {
	port := fake.New()
	wantErr := errors.New("db unavailable")
	spammers := &fakeSpammers{err: wantErr}
	rc := &ReactionCleaner{Spammers: spammers, Port: port, Enabled: true}

	err := rc.Observe(context.Background(), ReactionEvent{ChatID: 1, MessageID: 9, UserID: 2, Added: true})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Observe error=%v want %v", err, wantErr)
	}

	if calls := port.Calls(); len(calls) != 0 {
		t.Fatalf("calls=%v want none", calls)
	}
}

func TestReactionCleanerObserveIgnoresZeroUserID(t *testing.T) {
	port := fake.New()
	spammers := &fakeSpammers{known: map[int64]bool{0: true}}
	rc := &ReactionCleaner{Spammers: spammers, Port: port, Enabled: true}

	err := rc.Observe(context.Background(), ReactionEvent{ChatID: 1, MessageID: 9, UserID: 0, Added: true})
	if err != nil {
		t.Fatalf("Observe returned error: %v", err)
	}

	if calls := port.Calls(); len(calls) != 0 {
		t.Fatalf("calls=%v want none", calls)
	}
}
