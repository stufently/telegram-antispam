// Package store is the SQLite persistence layer: WAL, one writer goroutine.
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"sync"

	_ "modernc.org/sqlite"
)

// ErrClosed is returned by Write when the database has been closed.
var ErrClosed = errors.New("store: database is closed")

type writeReq struct {
	fn   func(*sql.Tx) error
	done chan error
}

// DB wraps *sql.DB and serializes writes through one goroutine.
type DB struct {
	sql       *sql.DB
	writes    chan writeReq
	quit      chan struct{}
	stopped   chan struct{}
	closeOnce sync.Once

	mu     sync.RWMutex
	closed bool
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
	db := &DB{
		sql:     sqlDB,
		writes:  make(chan writeReq),
		quit:    make(chan struct{}),
		stopped: make(chan struct{}),
	}
	go db.writer()
	return db, nil
}

func (db *DB) writer() {
	defer close(db.stopped)
	for {
		select {
		case req := <-db.writes:
			req.done <- db.runTx(req.fn)
		case <-db.quit:
			return
		}
	}
}

// runTx runs fn in a transaction. A panic in fn is recovered, rolled back, and
// returned as an error so a bad callback cannot crash the writer goroutine.
func (db *DB) runTx(fn func(*sql.Tx) error) (err error) {
	tx, err := db.sql.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if r := recover(); r != nil {
			if rbErr := tx.Rollback(); rbErr != nil {
				err = fmt.Errorf("store: write callback panicked: %v (rollback failed: %v)", r, rbErr)
			} else {
				err = fmt.Errorf("store: write callback panicked: %v", r)
			}
		}
	}()
	if e := fn(tx); e != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			return fmt.Errorf("%w (rollback failed: %v)", e, rbErr)
		}
		return e
	}
	return tx.Commit()
}

// Write runs fn in a transaction on the single writer goroutine. It returns
// ErrClosed if the database is closed, and never blocks past Close().
func (db *DB) Write(fn func(*sql.Tx) error) error {
	db.mu.RLock()
	closed := db.closed
	db.mu.RUnlock()
	if closed {
		return ErrClosed
	}
	req := writeReq{fn: fn, done: make(chan error, 1)}
	select {
	case db.writes <- req:
		return <-req.done
	case <-db.quit:
		return ErrClosed
	}
}

// Read returns the underlying DB for read queries (WAL allows concurrent reads).
func (db *DB) Read() *sql.DB { return db.sql }

// Close stops the writer, waits for it to exit, then closes the database. It is
// idempotent.
func (db *DB) Close() error {
	var err error
	db.closeOnce.Do(func() {
		db.mu.Lock()
		db.closed = true
		db.mu.Unlock()
		close(db.quit)
		<-db.stopped
		err = db.sql.Close()
	})
	return err
}
