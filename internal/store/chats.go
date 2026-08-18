package store

import "database/sql"

type ChatRow struct {
	ChatID       int64
	Enabled      bool
	DryRun       bool
	LinkedChatID int64
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (db *DB) UpsertChat(row ChatRow) error {
	return db.Write(func(tx *sql.Tx) error {
		_, err := tx.Exec(`
INSERT INTO chats(chat_id, enabled, dry_run, linked_chat_id)
VALUES(?,?,?,?)
ON CONFLICT(chat_id) DO UPDATE SET
	enabled=excluded.enabled,
	dry_run=excluded.dry_run,
	linked_chat_id=excluded.linked_chat_id`,
			row.ChatID, b2i(row.Enabled), b2i(row.DryRun), row.LinkedChatID)
		return err
	})
}

func (db *DB) GetChat(chatID int64) (ChatRow, bool, error) {
	var r ChatRow
	var en, dry int
	err := db.Read().QueryRow(
		"SELECT chat_id, enabled, dry_run, linked_chat_id FROM chats WHERE chat_id=?", chatID,
	).Scan(&r.ChatID, &en, &dry, &r.LinkedChatID)
	if err == sql.ErrNoRows {
		return ChatRow{}, false, nil
	}
	if err != nil {
		return ChatRow{}, false, err
	}
	r.Enabled, r.DryRun = en == 1, dry == 1
	return r, true, nil
}

func (db *DB) DisableChat(chatID int64) error {
	return db.Write(func(tx *sql.Tx) error {
		_, err := tx.Exec("UPDATE chats SET enabled=0 WHERE chat_id=?", chatID)
		return err
	})
}

func (db *DB) AddAlias(oldID, newID int64) error {
	return db.Write(func(tx *sql.Tx) error {
		_, err := tx.Exec(
			"INSERT OR REPLACE INTO chat_aliases(old_id, new_id) VALUES(?,?)", oldID, newID)
		return err
	})
}

// ResolveChat follows an alias if present, else returns id unchanged.
func (db *DB) ResolveChat(id int64) int64 {
	var newID int64
	err := db.Read().QueryRow("SELECT new_id FROM chat_aliases WHERE old_id=?", id).Scan(&newID)
	if err != nil {
		return id
	}
	return newID
}
