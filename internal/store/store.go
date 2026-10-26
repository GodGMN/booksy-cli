// Package store is the local state database: watches, which slots each watch
// has already seen, and the events it produced. SQLite via modernc (no cgo).
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

// Kind of watch target. Stored as text so the schema reads well in sqlite3.
const (
	KindSlots = "slots"
)

// Watch is the persisted form. Target is kind-specific JSON.
type Watch struct {
	ID          int64           `json:"id"`
	Name        string          `json:"name"`
	Profile     string          `json:"profile"`
	Kind        string          `json:"kind"`
	Target      json.RawMessage `json:"target"`
	Sinks       []string        `json:"sinks"`
	Interval    time.Duration   `json:"-"`
	CreatedAt   time.Time       `json:"created_at"`
	LastCheckAt time.Time       `json:"last_check_at,omitempty"`
	Baselined   bool            `json:"baselined"`
}

// MarshalJSON reports the interval as a Go duration string ("15m0s").
func (w Watch) MarshalJSON() ([]byte, error) {
	type plain Watch
	return json.Marshal(struct {
		plain
		Interval string `json:"interval"`
	}{plain(w), w.Interval.String()})
}

// Seen records that a watch has observed one key (a slot start) at least once.
type Seen struct {
	Key       string
	FirstSeen time.Time
	LastSeen  time.Time
}

type Event struct {
	ID      int64           `json:"-"`
	Type    string          `json:"type"`
	Watch   string          `json:"watch"`
	Profile string          `json:"profile"`
	At      time.Time       `json:"at"`
	Data    json.RawMessage `json:"data,omitempty"`
}

const schema = `
CREATE TABLE IF NOT EXISTS watches (
  id INTEGER PRIMARY KEY,
  name TEXT NOT NULL UNIQUE,
  profile TEXT NOT NULL,
  kind TEXT NOT NULL,
  target TEXT NOT NULL,
  sinks TEXT NOT NULL DEFAULT '[]',
  interval_sec INTEGER NOT NULL,
  created_at TEXT NOT NULL,
  last_check_at TEXT,
  baselined INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS seen (
  watch_id INTEGER NOT NULL REFERENCES watches(id) ON DELETE CASCADE,
  key TEXT NOT NULL,
  first_seen TEXT NOT NULL,
  last_seen TEXT NOT NULL,
  PRIMARY KEY (watch_id, key)
);
CREATE TABLE IF NOT EXISTS events (
  id INTEGER PRIMARY KEY,
  watch_id INTEGER NOT NULL REFERENCES watches(id) ON DELETE CASCADE,
  type TEXT NOT NULL,
  at TEXT NOT NULL,
  data TEXT
);
CREATE INDEX IF NOT EXISTS events_watch_at ON events(watch_id, at);
`

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	// One writer at a time keeps the service timer and a manual check from
	// tripping over each other.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("init state db: %w", err)
	}
	_ = os.Chmod(path, 0o600)
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

var (
	ErrExists   = errors.New("a watch with that name already exists")
	ErrNotFound = errors.New("no watch with that name")
)

func (s *Store) AddWatch(ctx context.Context, w Watch) (Watch, error) {
	sinks, _ := json.Marshal(w.Sinks)
	if w.Sinks == nil {
		sinks = []byte("[]")
	}
	w.CreatedAt = time.Now().UTC()
	res, err := s.db.ExecContext(ctx, `INSERT INTO watches(name,profile,kind,target,sinks,interval_sec,created_at) VALUES(?,?,?,?,?,?,?)`,
		w.Name, w.Profile, w.Kind, string(w.Target), string(sinks), int64(w.Interval/time.Second), w.CreatedAt.Format(time.RFC3339))
	if err != nil {
		if strings.Contains(strings.ToUpper(err.Error()), "UNIQUE") {
			return w, ErrExists
		}
		return w, err
	}
	w.ID, _ = res.LastInsertId()
	return w, nil
}

const watchCols = `id,name,profile,kind,target,sinks,interval_sec,created_at,COALESCE(last_check_at,''),baselined`

func scanWatch(row interface{ Scan(...any) error }) (Watch, error) {
	var w Watch
	var target, sinks, created, last string
	var interval int64
	var baselined int
	if err := row.Scan(&w.ID, &w.Name, &w.Profile, &w.Kind, &target, &sinks, &interval, &created, &last, &baselined); err != nil {
		return w, err
	}
	w.Target = json.RawMessage(target)
	_ = json.Unmarshal([]byte(sinks), &w.Sinks)
	w.Interval = time.Duration(interval) * time.Second
	w.CreatedAt, _ = time.Parse(time.RFC3339, created)
	if last != "" {
		w.LastCheckAt, _ = time.Parse(time.RFC3339, last)
	}
	w.Baselined = baselined == 1
	return w, nil
}

// Watches lists watches, optionally for one profile, ordered by name.
func (s *Store) Watches(ctx context.Context, profile string) ([]Watch, error) {
	q := `SELECT ` + watchCols + ` FROM watches`
	var args []any
	if profile != "" {
		q += ` WHERE profile = ?`
		args = append(args, profile)
	}
	q += ` ORDER BY name`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Watch{}
	for rows.Next() {
		w, err := scanWatch(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

func (s *Store) Watch(ctx context.Context, name string) (Watch, error) {
	w, err := scanWatch(s.db.QueryRowContext(ctx, `SELECT `+watchCols+` FROM watches WHERE name = ?`, name))
	if errors.Is(err, sql.ErrNoRows) {
		return w, ErrNotFound
	}
	return w, err
}

func (s *Store) RemoveWatch(ctx context.Context, name string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM watches WHERE name = ?`, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// RemoveProfileWatches drops every watch that ran under a profile.
func (s *Store) RemoveProfileWatches(ctx context.Context, profile string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM watches WHERE profile = ?`, profile)
	return err
}

func (s *Store) SeenFor(ctx context.Context, watchID int64) (map[string]Seen, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT key,first_seen,last_seen FROM seen WHERE watch_id = ?`, watchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]Seen{}
	for rows.Next() {
		var sn Seen
		var first, last string
		if err := rows.Scan(&sn.Key, &first, &last); err != nil {
			return nil, err
		}
		sn.FirstSeen, _ = time.Parse(time.RFC3339, first)
		sn.LastSeen, _ = time.Parse(time.RFC3339, last)
		out[sn.Key] = sn
	}
	return out, rows.Err()
}

// CommitCheck stores one check's outcome atomically: present keys upserted,
// vanished keys deleted, events appended, the watch marked checked and
// baselined. Callers deliver events to sinks first, so a crash in between
// re-emits rather than loses.
func (s *Store) CommitCheck(ctx context.Context, watchID int64, present, removed []string, events []Event) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339)
	for _, k := range present {
		if _, err := tx.ExecContext(ctx, `INSERT INTO seen(watch_id,key,first_seen,last_seen) VALUES(?,?,?,?)
			ON CONFLICT(watch_id,key) DO UPDATE SET last_seen=excluded.last_seen`, watchID, k, now, now); err != nil {
			return err
		}
	}
	for _, k := range removed {
		if _, err := tx.ExecContext(ctx, `DELETE FROM seen WHERE watch_id = ? AND key = ?`, watchID, k); err != nil {
			return err
		}
	}
	for _, ev := range events {
		var data any
		if len(ev.Data) > 0 {
			data = string(ev.Data)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO events(watch_id,type,at,data) VALUES(?,?,?,?)`,
			watchID, ev.Type, ev.At.UTC().Format(time.RFC3339Nano), data); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE watches SET last_check_at = ?, baselined = 1 WHERE id = ?`, now, watchID); err != nil {
		return err
	}
	return tx.Commit()
}

// Events returns stored events, newest first. watchName "" means all watches.
func (s *Store) Events(ctx context.Context, profile, watchName string, since time.Time, limit int) ([]Event, error) {
	q := `SELECT e.id, e.type, w.name, w.profile, e.at, COALESCE(e.data,'') FROM events e JOIN watches w ON w.id = e.watch_id WHERE 1=1`
	var args []any
	if profile != "" {
		q += ` AND w.profile = ?`
		args = append(args, profile)
	}
	if watchName != "" {
		q += ` AND w.name = ?`
		args = append(args, watchName)
	}
	if !since.IsZero() {
		q += ` AND e.at >= ?`
		args = append(args, since.UTC().Format(time.RFC3339Nano))
	}
	q += ` ORDER BY e.at DESC, e.id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var ev Event
		var at, data string
		if err := rows.Scan(&ev.ID, &ev.Type, &ev.Watch, &ev.Profile, &at, &data); err != nil {
			return nil, err
		}
		ev.At, _ = time.Parse(time.RFC3339Nano, at)
		if data != "" {
			ev.Data = json.RawMessage(data)
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}
