package store

import "database/sql"

const schema = `
CREATE TABLE IF NOT EXISTS updates (
	update_id INTEGER PRIMARY KEY
);
CREATE TABLE IF NOT EXISTS chats (
	chat_id     INTEGER PRIMARY KEY,
	enabled     INTEGER NOT NULL DEFAULT 1,
	dry_run     INTEGER NOT NULL DEFAULT 1,
	linked_chat_id INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS chat_aliases (
	old_id INTEGER PRIMARY KEY,
	new_id INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS incidents (
	id           INTEGER PRIMARY KEY AUTOINCREMENT,
	chat_id      INTEGER NOT NULL,
	message_id   INTEGER NOT NULL,
	user_id      INTEGER NOT NULL DEFAULT 0,
	state        TEXT    NOT NULL,
	dry_run      INTEGER NOT NULL DEFAULT 1,
	created_at   INTEGER NOT NULL DEFAULT (strftime('%s','now')),
	UNIQUE(chat_id, message_id)
);
CREATE TABLE IF NOT EXISTS evidence (
	incident_id      INTEGER NOT NULL,
	admin_chat_id    INTEGER NOT NULL,
	admin_message_id INTEGER NOT NULL,
	FOREIGN KEY(incident_id) REFERENCES incidents(id)
);
CREATE TABLE IF NOT EXISTS audit (
	incident_id INTEGER NOT NULL,
	action      TEXT    NOT NULL,
	scope       TEXT    NOT NULL,
	reason      TEXT    NOT NULL,
	signals     TEXT    NOT NULL,
	created_at  INTEGER NOT NULL DEFAULT (strftime('%s','now'))
);
CREATE TABLE IF NOT EXISTS samples (
	scope           TEXT NOT NULL,
	label           TEXT NOT NULL,
	origin          TEXT NOT NULL,
	normalized_hash TEXT NOT NULL,
	UNIQUE(scope, label, normalized_hash)
);
`

// Migrate creates all tables if absent. It is idempotent.
func (db *DB) Migrate() error {
	return db.Write(func(tx *sql.Tx) error {
		_, err := tx.Exec(schema)
		return err
	})
}
