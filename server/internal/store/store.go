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
}

const sourceCols = `id, name, type, enabled, rule, updated_at`

func scanSource(s interface{ Scan(...any) error }) (*SourceRow, error) {
	var r SourceRow
	err := s.Scan(&r.ID, &r.Name, &r.Type, &r.Enabled, &r.Rule, &r.UpdatedAt)
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

func (d *DB) CreateSource(name, typ, rule string) (int64, error) {
	res, err := d.Exec(`INSERT INTO sources (name, type, rule, updated_at) VALUES (?, ?, ?, ?)`, name, typ, rule, now())
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
