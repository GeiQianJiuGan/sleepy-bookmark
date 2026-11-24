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
	ID     int64  `json:"id"`
	OpenID string `json:"-"`
}

func (d *DB) UserByOpenID(openid string) (*User, error) {
	u := User{OpenID: openid}
	err := d.QueryRow(`SELECT id FROM users WHERE openid = ?`, openid).Scan(&u.ID)
	return &u, notFound(err)
}

func (d *DB) User(id int64) (*User, error) {
	u := User{ID: id}
	err := d.QueryRow(`SELECT openid FROM users WHERE id = ?`, id).Scan(&u.OpenID)
	return &u, notFound(err)
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
}

const sourceCols = `id, name, type, enabled, rule, updated_at, COALESCE(subscription_id, 0), listed`

func scanSource(s interface{ Scan(...any) error }) (*SourceRow, error) {
	var r SourceRow
	err := s.Scan(&r.ID, &r.Name, &r.Type, &r.Enabled, &r.Rule, &r.UpdatedAt, &r.SubscriptionID, &r.Listed)
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
	_, err := d.Exec(`UPDATE sources SET enabled = ? WHERE id = ?`, enabled, id)
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
