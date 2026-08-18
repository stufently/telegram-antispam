// Package store is the SQLite persistence layer: WAL, one writer goroutine.
package store

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

type writeReq struct {
	fn   func(*sql.Tx) error
	done chan error
}

// DB wraps *sql.DB and serializes writes through one goroutine.
type DB struct {
	sql    *sql.DB
	writes chan writeReq
	quit   chan struct{}
}

// Open opens the database with the mandated pragmas and starts the writer.
func Open(path string) (*DB, error) {
	dsn := fmt.Sprintf(
		"file:%s?_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)",
		path,
	)
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	if err := sqlDB.Ping(); err != nil {
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	db := &DB{sql: sqlDB, writes: make(chan writeReq), quit: make(chan struct{})}
	go db.writer()
	return db, nil
}

func (db *DB) writer() {
	for {
		select {
		case req := <-db.writes:
			req.done <- db.runTx(req.fn)
		case <-db.quit:
			return
		}
	}
}

func (db *DB) runTx(fn func(*sql.Tx) error) error {
	tx, err := db.sql.Begin()
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// Write runs fn in a transaction on the single writer goroutine.
func (db *DB) Write(fn func(*sql.Tx) error) error {
	req := writeReq{fn: fn, done: make(chan error, 1)}
	db.writes <- req
	return <-req.done
}

// Read returns the underlying DB for read queries (WAL allows concurrent reads).
func (db *DB) Read() *sql.DB { return db.sql }

// Close stops the writer and closes the database.
func (db *DB) Close() error {
	close(db.quit)
	return db.sql.Close()
}
