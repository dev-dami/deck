// Package queue is the durable store behind the offline queue (docs/
// mobile-contract.md §6): actions a phone left while the computer was away,
// the receipts the computer owes for them, and the ids it has already applied
// so a redelivered mail has no effect twice.
package queue

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"github.com/dev-dami/deck/internal/deck/peer"
	"github.com/dev-dami/deck/pkg/remote/wire"
)

// ErrFull is returned when a device already has wire.MaxQueuedPerDevice
// actions waiting.
var ErrFull = errors.New("queue: too many queued actions for this phone")

// Store is the queue database. It is safe for concurrent use.
type Store struct {
	mu sync.Mutex
	db *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS actions (
  qid         TEXT PRIMARY KEY,
  device_id   TEXT NOT NULL,
  created_at  INTEGER NOT NULL,
  expires_at  INTEGER NOT NULL,
  received_at INTEGER NOT NULL,
  seq         INTEGER NOT NULL,
  action      TEXT NOT NULL,
  state       TEXT NOT NULL DEFAULT 'pending'
);
CREATE INDEX IF NOT EXISTS actions_pending ON actions (state, device_id, created_at, seq);
CREATE TABLE IF NOT EXISTS receipts (
  qid       TEXT PRIMARY KEY,
  device_id TEXT NOT NULL,
  status    TEXT NOT NULL,
  reason    TEXT NOT NULL DEFAULT '',
  at        INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS receipts_device ON receipts (device_id, at);
`

// Open opens or creates dir/queue.db, readable only by the current user.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("queue: %w", err)
	}
	path := filepath.Join(dir, "queue.db")
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, fmt.Errorf("queue: %w", err)
	}
	// One connection: the queue is tiny and this keeps writes ordered.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("queue: %w", err)
	}
	for _, f := range []string{path, path + "-wal", path + "-shm"} {
		if _, err := os.Stat(f); err == nil {
			if err := peer.RestrictPrivate(f, false); err != nil {
				db.Close()
				return nil, fmt.Errorf("queue: %w", err)
			}
		}
	}
	return &Store{db: db}, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

func ms(t time.Time) int64 { return t.UnixMilli() }

// Enqueue stores an action. It reports false, with no error, when the qid is
// already known (waiting or applied): a redelivery. A phone with
// wire.MaxQueuedPerDevice actions waiting gets ErrFull.
func (s *Store) Enqueue(a wire.QueuedAction, now time.Time) (bool, error) {
	raw, err := json.Marshal(a)
	if err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	var known int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM actions WHERE qid = ?`, a.QID).Scan(&known); err != nil {
		return false, err
	}
	if known > 0 {
		return false, nil
	}
	var waiting int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM actions WHERE device_id = ? AND state = 'pending'`, a.DeviceID).Scan(&waiting); err != nil {
		return false, err
	}
	if waiting >= wire.MaxQueuedPerDevice {
		return false, ErrFull
	}
	var seq int64
	if err := tx.QueryRow(`SELECT COALESCE(MAX(seq), 0) + 1 FROM actions`).Scan(&seq); err != nil {
		return false, err
	}
	if _, err := tx.Exec(
		`INSERT INTO actions (qid, device_id, created_at, expires_at, received_at, seq, action) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		a.QID, a.DeviceID, ms(a.CreatedAt), ms(a.ExpiresAt), ms(now), seq, string(raw)); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// Pending returns the waiting actions, each device's in the order the phone
// made them (created_at, then arrival), devices interleaved by that order.
func (s *Store) Pending() ([]wire.QueuedAction, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`SELECT action FROM actions WHERE state = 'pending' ORDER BY created_at, seq`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []wire.QueuedAction
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var a wire.QueuedAction
		if err := json.Unmarshal([]byte(raw), &a); err != nil {
			return nil, fmt.Errorf("queue: stored action is not valid: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// LastReceived is when the newest waiting action arrived, for settling: the
// engine waits for a quiet moment so the whole mailbox is drained first.
func (s *Store) LastReceived() (time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var v sql.NullInt64
	if err := s.db.QueryRow(`SELECT MAX(received_at) FROM actions WHERE state = 'pending'`).Scan(&v); err != nil {
		return time.Time{}, err
	}
	if !v.Valid {
		return time.Time{}, nil
	}
	return time.UnixMilli(v.Int64), nil
}

// Complete records the outcome of a waiting action and removes it from the
// waiting set. The receipt is stored in the same transaction, so a receipt
// exists exactly when the action is no longer waiting. Completing an action
// that is not waiting is a no-op that returns false.
func (s *Store) Complete(r wire.Receipt, deviceID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE actions SET state = 'done' WHERE qid = ? AND state = 'pending'`, r.QID)
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return false, nil
	}
	if _, err := tx.Exec(`INSERT OR REPLACE INTO receipts (qid, device_id, status, reason, at) VALUES (?, ?, ?, ?, ?)`,
		r.QID, deviceID, r.Status, r.Reason, ms(r.At)); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// Reject records a receipt for an action that was never stored, such as one
// that arrived when the phone's queue was full.
func (s *Store) Reject(r wire.Receipt, deviceID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`INSERT OR REPLACE INTO receipts (qid, device_id, status, reason, at) VALUES (?, ?, ?, ?, ?)`,
		r.QID, deviceID, r.Status, r.Reason, ms(r.At))
	return err
}

// Cancel drops a waiting action for the device that queued it. It reports
// whether there was one.
func (s *Store) Cancel(deviceID, qid string, now time.Time) (bool, error) {
	s.mu.Lock()
	var owner string
	err := s.db.QueryRow(`SELECT device_id FROM actions WHERE qid = ? AND state = 'pending'`, qid).Scan(&owner)
	s.mu.Unlock()
	if errors.Is(err, sql.ErrNoRows) || (err == nil && owner != deviceID) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return s.Complete(wire.Receipt{QID: qid, Status: wire.ReceiptDropped, Reason: wire.ReasonCancelled, At: now}, deviceID)
}

// CancelAny drops a waiting action whoever queued it, for the person at the
// computer. It reports the device that queued it and whether there was one.
func (s *Store) CancelAny(qid string, now time.Time) (deviceID string, ok bool, err error) {
	s.mu.Lock()
	err = s.db.QueryRow(`SELECT device_id FROM actions WHERE qid = ? AND state = 'pending'`, qid).Scan(&deviceID)
	s.mu.Unlock()
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	ok, err = s.Complete(wire.Receipt{QID: qid, Status: wire.ReceiptDropped, Reason: wire.ReasonCancelled, At: now}, deviceID)
	return deviceID, ok, err
}

// Receipts returns a device's newest receipts, newest first.
func (s *Store) Receipts(deviceID string, limit int) ([]wire.Receipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`SELECT qid, status, reason, at FROM receipts WHERE device_id = ? ORDER BY at DESC, qid LIMIT ?`, deviceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []wire.Receipt{}
	for rows.Next() {
		var r wire.Receipt
		var at int64
		if err := rows.Scan(&r.QID, &r.Status, &r.Reason, &at); err != nil {
			return nil, err
		}
		r.At = time.UnixMilli(at).UTC()
		out = append(out, r)
	}
	return out, rows.Err()
}

// ForgetDevice removes everything about a revoked device.
func (s *Store) ForgetDevice(deviceID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{`DELETE FROM actions WHERE device_id = ?`, `DELETE FROM receipts WHERE device_id = ?`} {
		if _, err := tx.Exec(q, deviceID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Prune forgets applied qids and receipts older than wire.AppliedQIDRetention.
func (s *Store) Prune(now time.Time) error {
	cutoff := ms(now.Add(-wire.AppliedQIDRetention))
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM actions WHERE state = 'done' AND received_at < ?`, cutoff); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM receipts WHERE at < ?`, cutoff); err != nil {
		return err
	}
	return tx.Commit()
}
