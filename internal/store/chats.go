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

// RegisterChat inserts a chat with initial lifecycle state only if it is absent.
// Unlike UpsertChat it never overwrites enabled/dry_run/linked_chat_id for an
// existing chat, so calling it on every inbound message cannot clobber admin
// lifecycle changes (disable, dry-run promotion, discovered linked_chat_id).
func (db *DB) RegisterChat(chatID int64, dryRun bool) error {
	return db.Write(func(tx *sql.Tx) error {
		_, err := tx.Exec(
			`INSERT INTO chats(chat_id, enabled, dry_run) VALUES(?, 1, ?)
			 ON CONFLICT(chat_id) DO NOTHING`,
			chatID, b2i(dryRun))
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

// ListEnabledChats returns the chat_ids of every enabled chat, for startup
// self-checks that need to inspect the bot's rights in each active chat.
func (db *DB) ListEnabledChats() ([]int64, error) {
	rows, err := db.Read().Query("SELECT chat_id FROM chats WHERE enabled=1")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
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
