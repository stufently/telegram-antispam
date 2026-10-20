package main

import (
	"log"
	"strconv"

	"github.com/stufently/telegram-antispam/internal/admin"
	"github.com/stufently/telegram-antispam/internal/store"
	"github.com/stufently/telegram-antispam/internal/telegram"
	"github.com/stufently/telegram-antispam/internal/train"
)

// adminCallbackRouter resolves the incident off the polling goroutine, then
// queues the decision with the source chat's moderation and command training.
// Its routing queue MUST drain before the source sequencer closes: accepted
// callbacks would otherwise be lost as late submissions during shutdown.
type adminCallbackRouter struct {
	routing *telegram.Sequencer
	source  *telegram.Sequencer
	db      *store.DB
	handle  func(admin.Callback)
}

func newAdminCallbackRouter(db *store.DB, source *telegram.Sequencer, handle func(admin.Callback)) *adminCallbackRouter {
	return &adminCallbackRouter{routing: telegram.NewSequencer(), source: source, db: db, handle: handle}
}

func (r *adminCallbackRouter) Submit(cb admin.Callback) {
	r.routing.Submit(0, func() {
		_, key, ok := admin.ParseCallback(cb.Data)
		id, err := strconv.ParseInt(key, 10, 64)
		if !ok || err != nil {
			r.handle(cb) // handler answers invalid payloads; no incident can be touched
			return
		}
		chatID, err := r.db.GetIncidentChat(id)
		if err != nil {
			log.Printf("admin callback scope lookup: %v", err)
			return
		}
		r.source.Submit(chatID, func() { r.handle(cb) })
	})
}

func (r *adminCallbackRouter) Wait() { r.routing.Wait() }

// feedbackTrainer replaces the previous opposite label, including /spam's
// earlier sample. Repeated confirmations remain idempotent within the scope.
func feedbackTrainer(db *store.DB, scopeFor func(int64) string, origin string) func(int64, string, []string) error {
	return func(chatID int64, label string, tokens []string) error {
		from := "spam"
		if label == "spam" {
			from = "ham"
		}
		_, _, err := train.RelabelTokens(db, scopeFor(chatID), from, label, origin, tokens)
		return err
	}
}
