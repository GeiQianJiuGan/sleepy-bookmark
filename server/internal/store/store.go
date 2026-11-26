// Package store is the SQLite persistence layer.
package store

import (
	"database/sql"
	"errors"
	"time"

	_ "modernc.org/sqlite"
)

var ErrNotFound = errors.New("not found")

const schema = `
CREATE TABLE IF NOT EXISTS users (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	openid     TEXT NOT NULL UNIQUE,
	created_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS sources (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	name       TEXT NOT NULL,
	type       TEXT NOT NULL,
	enabled    INTEGER NOT NULL DEFAULT 1,
	rule       TEXT NOT NULL,
	updated_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS subscriptions (
	id        INTEGER PRIMARY KEY AUTOINCREMENT,
	name      TEXT NOT NULL,
	url       TEXT NOT NULL UNIQUE,
	synced_at INTEGER NOT NULL DEFAULT 0,
	error     TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS books (
	id             INTEGER PRIMARY KEY AUTOINCREMENT,
	source_id      INTEGER NOT NULL REFERENCES sources(id) ON DELETE CASCADE,
	type           TEXT NOT NULL,
	url            TEXT NOT NULL,
	toc_url        TEXT NOT NULL DEFAULT '',
	title          TEXT NOT NULL,
	author         TEXT NOT NULL DEFAULT '',
	cover          TEXT NOT NULL DEFAULT '',
	intro          TEXT NOT NULL DEFAULT '',
	latest         TEXT NOT NULL DEFAULT '',
	detail_at      INTEGER NOT NULL DEFAULT 0,
	toc_at         INTEGER NOT NULL DEFAULT 0,
	UNIQUE (source_id, url)
);
CREATE TABLE IF NOT EXISTS chapters (
	id      INTEGER PRIMARY KEY AUTOINCREMENT,
	book_id INTEGER NOT NULL REFERENCES books(id) ON DELETE CASCADE,
	idx     INTEGER NOT NULL,
	title   TEXT NOT NULL,
	url     TEXT NOT NULL,
	cached  INTEGER NOT NULL DEFAULT 0,
	UNIQUE (book_id, idx)
);
CREATE TABLE IF NOT EXISTS favorites (
	user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	book_id    INTEGER NOT NULL REFERENCES books(id) ON DELETE CASCADE,
	created_at INTEGER NOT NULL,
	PRIMARY KEY (user_id, book_id)
);
CREATE TABLE IF NOT EXISTS history (
	user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	book_id    INTEGER NOT NULL REFERENCES books(id) ON DELETE CASCADE,
	chapter_id INTEGER NOT NULL,
	position   INTEGER NOT NULL DEFAULT 0,
	updated_at INTEGER NOT NULL,
	PRIMARY KEY (user_id, book_id)
);
CREATE TABLE IF NOT EXISTS source_metrics (
	source_id       INTEGER NOT NULL REFERENCES sources(id) ON DELETE CASCADE,
	action          TEXT NOT NULL,
	total           INTEGER NOT NULL DEFAULT 0,
	valid           INTEGER NOT NULL DEFAULT 0,
	empty           INTEGER NOT NULL DEFAULT 0,
	error           INTEGER NOT NULL DEFAULT 0,
	total_latency   INTEGER NOT NULL DEFAULT 0,
	last_latency    INTEGER NOT NULL DEFAULT 0,
	min_latency     INTEGER NOT NULL DEFAULT 0,
	max_latency     INTEGER NOT NULL DEFAULT 0,
	last_status     TEXT NOT NULL DEFAULT '',
	last_error      TEXT NOT NULL DEFAULT '',
	last_error_kind TEXT NOT NULL DEFAULT '',
	last_checked_at INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (source_id, action)
);
`

type DB struct{ *sql.DB }

func Open(path string) (*DB, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	// A single connection keeps writes serialized and makes :memory: databases usable in tests.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	// Columns added after the first release are missing from a database created before them.
	for _, col := range [][2]string{
		{"subscription_id", "INTEGER REFERENCES subscriptions(id) ON DELETE SET NULL"},
		{"listed", "INTEGER NOT NULL DEFAULT 0"},
		{"auto_disabled", "INTEGER NOT NULL DEFAULT 0"},
	} {
		var has int
		err = db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('sources') WHERE name = ?`, col[0]).Scan(&has)
		if err == nil && has == 0 {
			_, err = db.Exec(`ALTER TABLE sources ADD COLUMN ` + col[0] + ` ` + col[1])
		}
		if err != nil {
			db.Close()
			return nil, err
		}
	}
	return &DB{db}, nil
}

func now() int64 { return time.Now().Unix() }

func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// ---- users ----

type User struct {
	ID        int64  `json:"id"`
	OpenID    string `json:"-"`
	CreatedAt int64  `json:"-"`
}

type UserSummary struct {
	ID        int64
	OpenID    string
	CreatedAt int64
	Favorites int
	History   int
}

func (d *DB) UserByOpenID(openid string) (*User, error) {
	u := User{OpenID: openid}
	err := d.QueryRow(`SELECT id, created_at FROM users WHERE openid = ?`, openid).Scan(&u.ID, &u.CreatedAt)
	return &u, notFound(err)
}

func (d *DB) User(id int64) (*User, error) {
	u := User{ID: id}
	err := d.QueryRow(`SELECT openid, created_at FROM users WHERE id = ?`, id).Scan(&u.OpenID, &u.CreatedAt)
	return &u, notFound(err)
}

func (d *DB) EarliestUserID() (id int64, err error) {
	err = d.QueryRow(`SELECT COALESCE(MIN(id), 0) FROM users`).Scan(&id)
	return
}

func (d *DB) Users() ([]UserSummary, error) {
	rows, err := d.Query(`SELECT u.id, u.openid, u.created_at,
		(SELECT COUNT(*) FROM favorites WHERE user_id = u.id),
		(SELECT COUNT(*) FROM history WHERE user_id = u.id)
		FROM users u ORDER BY u.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []UserSummary{}
	for rows.Next() {
		var u UserSummary
		if err := rows.Scan(&u.ID, &u.OpenID, &u.CreatedAt, &u.Favorites, &u.History); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (d *DB) UserCount() (n int, err error) {
	err = d.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n)
	return
}

func (d *DB) CreateUser(openid string) (*User, error) {
	res, err := d.Exec(`INSERT INTO users (openid, created_at) VALUES (?, ?)`, openid, now())
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return &User{ID: id, OpenID: openid}, nil
}

// ---- sources ----

type SourceRow struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	Enabled   bool   `json:"enabled"`
	Rule      string `json:"-"`
	UpdatedAt int64  `json:"updatedAt"`
	// SubscriptionID is the subscription that the source was last imported from, 0 if none.
	SubscriptionID int64 `json:"subscriptionId,omitempty"`
	// Listed is set for good on a source that a subscribed list wrote, whether or not the
	// subscription is still there: such a source is read from public addresses only.
	Listed bool `json:"-"`
	// AutoDisabled is true when the source was automatically disabled because its valid rate dropped below 50%.
	AutoDisabled bool `json:"autoDisabled,omitempty"`
}

const sourceCols = `id, name, type, enabled, rule, updated_at, COALESCE(subscription_id, 0), listed, auto_disabled`

func scanSource(s interface{ Scan(...any) error }) (*SourceRow, error) {
	var r SourceRow
	err := s.Scan(&r.ID, &r.Name, &r.Type, &r.Enabled, &r.Rule, &r.UpdatedAt, &r.SubscriptionID, &r.Listed, &r.AutoDisabled)
	return &r, notFound(err)
}

// Sources lists sources; typ "" means every type.
func (d *DB) Sources(typ string, enabledOnly bool) ([]*SourceRow, error) {
	rows, err := d.Query(`SELECT `+sourceCols+` FROM sources
		WHERE (? = '' OR type = ?) AND (? = 0 OR enabled = 1) ORDER BY id`, typ, typ, enabledOnly)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*SourceRow{}
	for rows.Next() {
		r, err := scanSource(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (d *DB) Source(id int64) (*SourceRow, error) {
	return scanSource(d.QueryRow(`SELECT `+sourceCols+` FROM sources WHERE id = ?`, id))
}

func (d *DB) SourceByName(name string) (*SourceRow, error) {
	return scanSource(d.QueryRow(`SELECT `+sourceCols+` FROM sources WHERE name = ?`, name))
}

// CreateSource adds a source, as one of the subscription if that is not 0.
func (d *DB) CreateSource(name, typ, rule string, subscription int64) (int64, error) {
	res, err := d.Exec(`INSERT INTO sources (name, type, rule, updated_at, subscription_id, listed)
		VALUES (?, ?, ?, ?, NULLIF(?, 0), ?)`, name, typ, rule, now(), subscription, subscription != 0)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (d *DB) UpdateSource(id int64, name, typ, rule string) error {
	_, err := d.Exec(`UPDATE sources SET name = ?, type = ?, rule = ?, updated_at = ? WHERE id = ?`, name, typ, rule, now(), id)
	return err
}

func (d *DB) SetSourceEnabled(id int64, enabled bool) error {
	_, err := d.Exec(`UPDATE sources SET enabled = ?, auto_disabled = 0 WHERE id = ?`, enabled, id)
	return err
}

func (d *DB) SetSourceAutoState(id int64, enabled, autoDisabled bool) error {
	_, err := d.Exec(`UPDATE sources SET enabled = ?, auto_disabled = ? WHERE id = ?`, enabled, autoDisabled, id)
	return err
}

func (d *DB) DeleteSource(id int64) error {
	_, err := d.Exec(`DELETE FROM sources WHERE id = ?`, id)
	return err
}

// ---- subscriptions ----

// Subscription is a list of source rules published at a URL, imported and synced again from there.
type Subscription struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	URL      string `json:"url"`
	SyncedAt int64  `json:"syncedAt"`
	// Error is why the last sync failed, "" if it did not.
	Error string `json:"error"`
	// Sources counts the sources that came from the list.
	Sources int `json:"sources"`
}

const subscriptionCols = `id, name, url, synced_at, error,
	(SELECT COUNT(*) FROM sources WHERE subscription_id = subscriptions.id)`

func scanSubscription(s interface{ Scan(...any) error }) (*Subscription, error) {
	var r Subscription
	err := s.Scan(&r.ID, &r.Name, &r.URL, &r.SyncedAt, &r.Error, &r.Sources)
	return &r, notFound(err)
}

func (d *DB) Subscriptions() ([]*Subscription, error) {
	rows, err := d.Query(`SELECT ` + subscriptionCols + ` FROM subscriptions ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Subscription{}
	for rows.Next() {
		r, err := scanSubscription(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (d *DB) Subscription(id int64) (*Subscription, error) {
	return scanSubscription(d.QueryRow(`SELECT `+subscriptionCols+` FROM subscriptions WHERE id = ?`, id))
}

func (d *DB) SubscriptionByURL(url string) (*Subscription, error) {
	return scanSubscription(d.QueryRow(`SELECT `+subscriptionCols+` FROM subscriptions WHERE url = ?`, url))
}

func (d *DB) CreateSubscription(name, url string) (int64, error) {
	res, err := d.Exec(`INSERT INTO subscriptions (name, url) VALUES (?, ?)`, name, url)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// SetSubscriptionSynced records the outcome of a sync; errText is "" for one that worked.
func (d *DB) SetSubscriptionSynced(id int64, errText string) error {
	_, err := d.Exec(`UPDATE subscriptions SET synced_at = ?, error = ? WHERE id = ?`, now(), errText, id)
	return err
}

// DeleteSubscription leaves the sources that came from it in place, as sources of their own.
func (d *DB) DeleteSubscription(id int64) error {
	_, err := d.Exec(`DELETE FROM subscriptions WHERE id = ?`, id)
	return err
}

// SetSourceSubscription makes a source one of a subscribed list.
func (d *DB) SetSourceSubscription(sourceID, subscriptionID int64) error {
	_, err := d.Exec(`UPDATE sources SET subscription_id = ?, listed = 1 WHERE id = ?`, subscriptionID, sourceID)
	return err
}

func (d *DB) SourcesBySubscription(id int64) ([]*SourceRow, error) {
	all, err := d.Sources("", false)
	if err != nil {
		return nil, err
	}
	out := []*SourceRow{}
	for _, r := range all {
		// 0 is how a source of no subscription reads, and is not a subscription.
		if id != 0 && r.SubscriptionID == id {
			out = append(out, r)
		}
	}
	return out, nil
}

// ---- source metrics ----

// SourceMetricRow stores cumulative monitoring statistics for one (source, action) pair.
type SourceMetricRow struct {
	SourceID      int64
	Action        string
	Total         int64
	Valid         int64
	Empty         int64
	Error         int64
	TotalLatency  int64
	LastLatency   int64
	MinLatency    int64
	MaxLatency    int64
	LastStatus    string
	LastError     string
	LastErrorKind string
	LastCheckedAt int64
}

func (d *DB) LoadSourceMetrics() ([]*SourceMetricRow, error) {
	rows, err := d.Query(`SELECT source_id, action, total, valid, empty, error,
		total_latency, last_latency, min_latency, max_latency,
		last_status, last_error, last_error_kind, last_checked_at
		FROM source_metrics ORDER BY source_id, action`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*SourceMetricRow{}
	for rows.Next() {
		var r SourceMetricRow
		if err := rows.Scan(&r.SourceID, &r.Action, &r.Total, &r.Valid, &r.Empty, &r.Error,
			&r.TotalLatency, &r.LastLatency, &r.MinLatency, &r.MaxLatency,
			&r.LastStatus, &r.LastError, &r.LastErrorKind, &r.LastCheckedAt); err != nil {
			return nil, err
		}
		out = append(out, &r)
	}
	return out, rows.Err()
}

func (d *DB) UpsertSourceMetric(m *SourceMetricRow) error {
	_, err := d.Exec(`INSERT INTO source_metrics
		(source_id, action, total, valid, empty, error, total_latency, last_latency, min_latency, max_latency,
		 last_status, last_error, last_error_kind, last_checked_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(source_id, action) DO UPDATE SET
			total = excluded.total,
			valid = excluded.valid,
			empty = excluded.empty,
			error = excluded.error,
			total_latency = excluded.total_latency,
			last_latency = excluded.last_latency,
			min_latency = excluded.min_latency,
			max_latency = excluded.max_latency,
			last_status = excluded.last_status,
			last_error = excluded.last_error,
			last_error_kind = excluded.last_error_kind,
			last_checked_at = excluded.last_checked_at`,
		m.SourceID, m.Action, m.Total, m.Valid, m.Empty, m.Error,
		m.TotalLatency, m.LastLatency, m.MinLatency, m.MaxLatency,
		m.LastStatus, m.LastError, m.LastErrorKind, m.LastCheckedAt)
	return err
}

func (d *DB) DeleteSourceMetrics(sourceID int64) error {
	_, err := d.Exec(`DELETE FROM source_metrics WHERE source_id = ?`, sourceID)
	return err
}
