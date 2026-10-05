package store

import (
	"database/sql"
	"testing"

	"github.com/stufently/telegram-antispam/internal/domain"
)

func TestIncidentCardMigration(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		db := newMigrated(t)
		defer db.Close()
		id, _, err := db.InsertPending(-1, 1, 7, 0, true, domain.Verdict{Action: domain.ActionQuarantine})
		if err != nil {
			t.Fatal(err)
		}
		if legacy {
			if err := db.Write(func(tx *sql.Tx) error { _, err := tx.Exec("DROP TABLE incident_cards"); return err }); err != nil {
				t.Fatal(err)
			}
		}
		if err := db.Migrate(); err != nil {
			t.Fatal(err)
		}
		old := IncidentCard{ChatID: 99, MessageID: 1}
		if ok, err := db.IsIncidentCard(id, old); err != nil || !ok {
			t.Fatalf("legacy card: %v %v", ok, err)
		}
		current := IncidentCard{ChatID: 99, MessageID: 2}
		if err := db.SaveIncidentCard(id, current); err != nil {
			t.Fatal(err)
		}
		if err := db.Migrate(); err != nil {
			t.Fatal(err)
		}
		if ok, err := db.IsIncidentCard(id, old); err != nil || ok {
			t.Fatalf("obsolete card: %v %v", ok, err)
		}
		if ok, err := db.IsIncidentCard(id, current); err != nil || !ok {
			t.Fatalf("current card: %v %v", ok, err)
		}
	}
}
