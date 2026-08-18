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
CREATE TABLE IF NOT EXISTS users (
	chat_id          INTEGER NOT NULL,
	user_id          INTEGER NOT NULL,
	meaningful_count INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY(chat_id, user_id)
);
`

// Migrate creates all tables if absent, then applies any additive column
// migrations needed by pre-existing databases. It is idempotent.
func (db *DB) Migrate() error {
	return db.Write(func(tx *sql.Tx) error {
		if _, err := tx.Exec(schema); err != nil {
			return err
		}
		return addColumnIfMissing(tx, "users", "meaningful_count",
			"INTEGER NOT NULL DEFAULT 0")
	})
}

// addColumnIfMissing adds a column to table via ALTER TABLE if it is not
// already present, using PRAGMA table_info to check first. SQLite's ALTER
// TABLE ... ADD COLUMN has no "IF NOT EXISTS" form, and re-running it on a
// column that already exists (e.g. one created fresh by the CREATE TABLE
// above) errors with "duplicate column name" — this guard makes the
// operation safe to run on both fresh and pre-existing databases.
func addColumnIfMissing(tx *sql.Tx, table, column, columnDef string) error {
	rows, err := tx.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return err
	}
	defer rows.Close()

	found := false
	for rows.Next() {
		var (
			cid       int
			name      string
			ctype     string
			notNull   int
			dfltValue sql.NullString
			pk        int
		)
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &dfltValue, &pk); err != nil {
			return err
		}
		if name == column {
			found = true
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if found {
		return nil
	}
	_, err = tx.Exec("ALTER TABLE " + table + " ADD COLUMN " + column + " " + columnDef)
	return err
}
