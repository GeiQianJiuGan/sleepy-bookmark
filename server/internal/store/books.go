package store

import "database/sql"

type Book struct {
	ID         int64  `json:"id"`
	SourceID   int64  `json:"sourceId"`
	SourceName string `json:"sourceName"`
	Type       string `json:"type"`
	URL        string `json:"url"`
	TocURL     string `json:"-"`
	Title      string `json:"title"`
	Author     string `json:"author"`
	Cover      string `json:"cover"`
	Intro      string `json:"intro"`
	Latest     string `json:"latest"`
	DetailAt   int64  `json:"-"`
	TocAt      int64  `json:"tocAt"`
}

type Chapter struct {
	ID     int64  `json:"id"`
	BookID int64  `json:"bookId"`
	Idx    int    `json:"idx"`
	Title  string `json:"title"`
	URL    string `json:"-"`
	Cached bool   `json:"cached"`
}

// Progress is where a user stopped reading a book.
type Progress struct {
	ChapterID    int64  `json:"chapterId"`
	ChapterIdx   int    `json:"chapterIdx"`
	ChapterTitle string `json:"chapterTitle"`
	Position     int    `json:"position"`
	UpdatedAt    int64  `json:"updatedAt"`
}

// ShelfBook is a book as seen by one user.
type ShelfBook struct {
	Book
	Favorite     bool      `json:"favorite"`
	ChapterCount int       `json:"chapterCount"`
	Progress     *Progress `json:"progress"`
}

const bookCols = `b.id, b.source_id, s.name, b.type, b.url, b.toc_url, b.title, b.author, b.cover, b.intro, b.latest, b.detail_at, b.toc_at`

func scanBook(s interface{ Scan(...any) error }, extra ...any) (*Book, error) {
	var b Book
	dest := append([]any{&b.ID, &b.SourceID, &b.SourceName, &b.Type, &b.URL, &b.TocURL, &b.Title, &b.Author,
		&b.Cover, &b.Intro, &b.Latest, &b.DetailAt, &b.TocAt}, extra...)
	return &b, notFound(s.Scan(dest...))
}

func (d *DB) Book(id int64) (*Book, error) {
	return scanBook(d.QueryRow(`SELECT `+bookCols+` FROM books b JOIN sources s ON s.id = b.source_id WHERE b.id = ?`, id))
}

// UpsertBook stores a search hit and returns its id; empty fields never overwrite known values.
func (d *DB) UpsertBook(b *Book) (int64, error) {
	var id int64
	err := d.QueryRow(`INSERT INTO books (source_id, type, url, title, author, cover, intro, latest)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (source_id, url) DO UPDATE SET
			title  = CASE WHEN excluded.title  != '' THEN excluded.title  ELSE title  END,
			author = CASE WHEN excluded.author != '' THEN excluded.author ELSE author END,
			cover  = CASE WHEN excluded.cover  != '' THEN excluded.cover  ELSE cover  END,
			intro  = CASE WHEN excluded.intro  != '' THEN excluded.intro  ELSE intro  END,
			latest = CASE WHEN excluded.latest != '' THEN excluded.latest ELSE latest END
		RETURNING id`, b.SourceID, b.Type, b.URL, b.Title, b.Author, b.Cover, b.Intro, b.Latest).Scan(&id)
	return id, err
}

func (d *DB) SetBookDetail(b *Book) error {
	_, err := d.Exec(`UPDATE books SET title = ?, author = ?, cover = ?, intro = ?, latest = ?, toc_url = ?, detail_at = ?
		WHERE id = ?`, b.Title, b.Author, b.Cover, b.Intro, b.Latest, b.TocURL, now(), b.ID)
	return err
}

func (d *DB) Chapters(bookID int64) ([]*Chapter, error) {
	rows, err := d.Query(`SELECT id, book_id, idx, title, url, cached FROM chapters WHERE book_id = ? ORDER BY idx`, bookID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Chapter{}
	for rows.Next() {
		var c Chapter
		if err := rows.Scan(&c.ID, &c.BookID, &c.Idx, &c.Title, &c.URL, &c.Cached); err != nil {
			return nil, err
		}
		out = append(out, &c)
	}
	return out, rows.Err()
}

func (d *DB) Chapter(id int64) (*Chapter, error) {
	var c Chapter
	err := d.QueryRow(`SELECT id, book_id, idx, title, url, cached FROM chapters WHERE id = ?`, id).
		Scan(&c.ID, &c.BookID, &c.Idx, &c.Title, &c.URL, &c.Cached)
	return &c, notFound(err)
}

func (d *DB) ChapterAt(bookID int64, idx int) (*Chapter, error) {
	var c Chapter
	err := d.QueryRow(`SELECT id, book_id, idx, title, url, cached FROM chapters WHERE book_id = ? AND idx = ?`, bookID, idx).
		Scan(&c.ID, &c.BookID, &c.Idx, &c.Title, &c.URL, &c.Cached)
	return &c, notFound(err)
}

type TocEntry struct{ Title, URL string }

// ReplaceToc syncs the chapter list of a book. Chapters are matched by URL, so a chapter keeps
// its id (which reading progress and the content cache refer to) when the source inserts,
// removes or reorders chapters. It returns the ids of removed chapters that had cached content.
func (d *DB) ReplaceToc(bookID int64, toc []TocEntry) (stale []int64, err error) {
	old, err := d.Chapters(bookID)
	if err != nil {
		return nil, err
	}
	byURL := map[string][]*Chapter{}
	for _, c := range old {
		byURL[c.URL] = append(byURL[c.URL], c)
	}

	tx, err := d.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	// Park the existing rows on negative positions so that moving them cannot collide.
	if _, err := tx.Exec(`UPDATE chapters SET idx = -id WHERE book_id = ?`, bookID); err != nil {
		return nil, err
	}
	for i, t := range toc {
		if same := byURL[t.URL]; len(same) > 0 {
			byURL[t.URL] = same[1:]
			_, err = tx.Exec(`UPDATE chapters SET idx = ?, title = ? WHERE id = ?`, i, t.Title, same[0].ID)
		} else {
			_, err = tx.Exec(`INSERT INTO chapters (book_id, idx, title, url) VALUES (?, ?, ?, ?)`, bookID, i, t.Title, t.URL)
		}
		if err != nil {
			return nil, err
		}
	}
	for _, gone := range byURL {
		for _, c := range gone {
			if c.Cached {
				stale = append(stale, c.ID)
			}
		}
	}
	if _, err := tx.Exec(`DELETE FROM chapters WHERE book_id = ? AND idx < 0`, bookID); err != nil {
		return nil, err
	}
	latest := ""
	if len(toc) > 0 {
		latest = toc[len(toc)-1].Title
	}
	if _, err := tx.Exec(`UPDATE books SET toc_at = ?, latest = ? WHERE id = ?`, now(), latest, bookID); err != nil {
		return nil, err
	}
	return stale, tx.Commit()
}

func (d *DB) SetChapterCached(id int64, cached bool) error {
	_, err := d.Exec(`UPDATE chapters SET cached = ? WHERE id = ?`, cached, id)
	return err
}

// ---- shelf: favorites and history ----

const shelfSelect = `SELECT ` + bookCols + `,
		f.book_id IS NOT NULL,
		(SELECT COUNT(*) FROM chapters WHERE book_id = b.id),
		h.chapter_id, c.idx, c.title, h.position, h.updated_at
	FROM books b
	JOIN sources s ON s.id = b.source_id
	LEFT JOIN favorites f ON f.book_id = b.id AND f.user_id = ?
	LEFT JOIN history h   ON h.book_id = b.id AND h.user_id = ?
	LEFT JOIN chapters c  ON c.id = h.chapter_id `

func scanShelf(s interface{ Scan(...any) error }) (*ShelfBook, error) {
	var sb ShelfBook
	var chID, chIdx, pos, at sql.NullInt64
	var chTitle sql.NullString
	b, err := scanBook(s, &sb.Favorite, &sb.ChapterCount, &chID, &chIdx, &chTitle, &pos, &at)
	if err != nil {
		return nil, err
	}
	sb.Book = *b
	if chID.Valid {
		sb.Progress = &Progress{chID.Int64, int(chIdx.Int64), chTitle.String, int(pos.Int64), at.Int64}
	}
	return &sb, nil
}

func (d *DB) shelf(query string, args ...any) ([]*ShelfBook, error) {
	rows, err := d.Query(shelfSelect+query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*ShelfBook{}
	for rows.Next() {
		sb, err := scanShelf(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sb)
	}
	return out, rows.Err()
}

func (d *DB) ShelfBook(userID, bookID int64) (*ShelfBook, error) {
	return scanShelf(d.QueryRow(shelfSelect+`WHERE b.id = ?`, userID, userID, bookID))
}

// Favorites lists a user's favorites, most recently read (or added) first.
func (d *DB) Favorites(userID int64, typ string) ([]*ShelfBook, error) {
	return d.shelf(`WHERE f.book_id IS NOT NULL AND (? = '' OR b.type = ?)
		ORDER BY MAX(f.created_at, COALESCE(h.updated_at, 0)) DESC`, userID, userID, typ, typ)
}

func (d *DB) History(userID int64, typ string) ([]*ShelfBook, error) {
	return d.shelf(`WHERE h.book_id IS NOT NULL AND (? = '' OR b.type = ?) ORDER BY h.updated_at DESC`, userID, userID, typ, typ)
}

func (d *DB) AddFavorite(userID, bookID int64) error {
	_, err := d.Exec(`INSERT OR IGNORE INTO favorites (user_id, book_id, created_at) VALUES (?, ?, ?)`, userID, bookID, now())
	return err
}

func (d *DB) RemoveFavorite(userID, bookID int64) error {
	_, err := d.Exec(`DELETE FROM favorites WHERE user_id = ? AND book_id = ?`, userID, bookID)
	return err
}

// FavoriteBookIDs returns every book that at least one user has in their favorites.
func (d *DB) FavoriteBookIDs() ([]int64, error) {
	rows, err := d.Query(`SELECT DISTINCT book_id FROM favorites`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (d *DB) SaveProgress(userID, bookID, chapterID int64, position int) error {
	_, err := d.Exec(`INSERT INTO history (user_id, book_id, chapter_id, position, updated_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (user_id, book_id) DO UPDATE SET
			chapter_id = excluded.chapter_id, position = excluded.position, updated_at = excluded.updated_at`,
		userID, bookID, chapterID, position, now())
	return err
}

func (d *DB) RemoveHistory(userID, bookID int64) error {
	_, err := d.Exec(`DELETE FROM history WHERE user_id = ? AND (? = 0 OR book_id = ?)`, userID, bookID, bookID)
	return err
}

// ResetSourceBooks makes the books of a source re-read their detail and catalog the next time
// they are opened, and with content also forgets which chapters are cached.
func (d *DB) ResetSourceBooks(sourceID int64, content bool) error {
	if _, err := d.Exec(`UPDATE books SET detail_at = 0, toc_at = 0 WHERE source_id = ?`, sourceID); err != nil || !content {
		return err
	}
	_, err := d.Exec(`UPDATE chapters SET cached = 0 WHERE book_id IN (SELECT id FROM books WHERE source_id = ?)`, sourceID)
	return err
}

func (d *DB) BookIDsBySource(sourceID int64) ([]int64, error) {
	rows, err := d.Query(`SELECT id FROM books WHERE source_id = ?`, sourceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
