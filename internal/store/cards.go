package store

import "database/sql"

// IncidentCard identifies the current verdict card, independently of evidence
// deletion. Missing records accept legacy cards until the next promotion.
type IncidentCard struct {
	ChatID    int64
	MessageID int
}

func (db *DB) SaveIncidentCard(id int64, card IncidentCard) error {
	return db.Write(func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO incident_cards VALUES(?,?,?) ON CONFLICT(incident_id)
DO UPDATE SET chat_id=excluded.chat_id, message_id=excluded.message_id`, id, card.ChatID, card.MessageID)
		return err
	})
}

func (db *DB) IsIncidentCard(id int64, card IncidentCard) (bool, error) {
	var current bool
	err := db.Read().QueryRow(`SELECT NOT EXISTS (SELECT 1 FROM incident_cards
WHERE incident_id=? AND (chat_id!=? OR message_id!=?))`, id, card.ChatID, card.MessageID).Scan(&current)
	return current, err
}

// DeleteEvidenceSet forgets only the copies Telegram actually deleted. New
// copies added while that request was in flight remain discoverable.
func (db *DB) DeleteEvidenceSet(id, chat int64, ids []int) error {
	return db.Write(func(tx *sql.Tx) error {
		for _, mid := range ids {
			if _, err := tx.Exec("DELETE FROM evidence WHERE incident_id=? AND admin_chat_id=? AND admin_message_id=?", id, chat, mid); err != nil {
				return err
			}
		}
		return nil
	})
}
