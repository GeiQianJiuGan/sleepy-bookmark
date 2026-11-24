// Package cache stores chapter payloads and proxied images on disk.
package cache

import (
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"
)

type Disk struct{ dir string }

func New(dir string) *Disk { return &Disk{dir} }

func (d *Disk) chapterPath(bookID, chapterID int64) string {
	return filepath.Join(d.dir, "content", strconv.FormatInt(bookID, 10), strconv.FormatInt(chapterID, 10)+".json.gz")
}

func (d *Disk) LoadChapter(bookID, chapterID int64, v any) bool {
	f, err := os.Open(d.chapterPath(bookID, chapterID))
	if err != nil {
		return false
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return false
	}
	return json.NewDecoder(zr).Decode(v) == nil
}

func (d *Disk) SaveChapter(bookID, chapterID int64, v any) error {
	path := d.chapterPath(bookID, chapterID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), "tmp-*")
	if err != nil {
		return err
	}
	zw := gzip.NewWriter(f)
	err = json.NewEncoder(zw).Encode(v)
	if cerr := zw.Close(); err == nil {
		err = cerr
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(f.Name())
		return err
	}
	return os.Rename(f.Name(), path)
}

func (d *Disk) RemoveChapter(bookID, chapterID int64) {
	os.Remove(d.chapterPath(bookID, chapterID))
}

func (d *Disk) RemoveBook(bookID int64) {
	os.RemoveAll(filepath.Join(d.dir, "content", strconv.FormatInt(bookID, 10)))
}

// Images live in one of two places: img is a cache that EvictImages trims, kept holds the pages
// of favorited comics and is only emptied when the book leaves the favorites.
func (d *Disk) imagePath(url string, kept bool) string {
	sum := sha256.Sum256([]byte(url))
	name := hex.EncodeToString(sum[:])
	dir := "img"
	if kept {
		dir = "kept"
	}
	return filepath.Join(d.dir, dir, name[:2], name)
}

// LoadImage returns a stored image and marks a cached one as recently used.
func (d *Disk) LoadImage(url string) ([]byte, bool) {
	if data, err := os.ReadFile(d.imagePath(url, true)); err == nil {
		return data, true
	}
	path := d.imagePath(url, false)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	t := time.Now()
	os.Chtimes(path, t, t)
	return data, true
}

func (d *Disk) HasImage(url string) bool {
	if _, err := os.Stat(d.imagePath(url, true)); err == nil {
		return true
	}
	_, err := os.Stat(d.imagePath(url, false))
	return err == nil
}

func (d *Disk) SaveImage(url string, data []byte, kept bool) error {
	path := d.imagePath(url, kept)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// KeepImage moves a cached image out of reach of EvictImages. It reports whether the image is kept.
func (d *Disk) KeepImage(url string) bool {
	return d.move(d.imagePath(url, false), d.imagePath(url, true))
}

// ReleaseImage hands a kept image back to the cache, as one that was just used.
func (d *Disk) ReleaseImage(url string) {
	if path := d.imagePath(url, false); d.move(d.imagePath(url, true), path) {
		t := time.Now()
		os.Chtimes(path, t, t)
	}
}

// move reports whether the file is at to afterwards.
func (d *Disk) move(from, to string) bool {
	if _, err := os.Stat(to); err == nil {
		os.Remove(from)
		return true
	}
	if os.MkdirAll(filepath.Dir(to), 0o755) != nil {
		return false
	}
	return os.Rename(from, to) == nil
}

// KeptBytes is the size of the kept images.
func (d *Disk) KeptBytes() (total int64) {
	filepath.WalkDir(filepath.Join(d.dir, "kept"), func(path string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() {
			return nil
		}
		if info, err := e.Info(); err == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}

// EvictImages deletes least recently used images until the image cache fits in maxBytes.
func (d *Disk) EvictImages(maxBytes int64) (removed int) {
	type file struct {
		path string
		size int64
		at   time.Time
	}
	var files []file
	var total int64
	filepath.WalkDir(filepath.Join(d.dir, "img"), func(path string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() {
			return nil
		}
		if info, err := e.Info(); err == nil {
			files = append(files, file{path, info.Size(), info.ModTime()})
			total += info.Size()
		}
		return nil
	})
	sort.Slice(files, func(i, j int) bool { return files[i].at.Before(files[j].at) })
	for _, f := range files {
		if total <= maxBytes {
			break
		}
		if os.Remove(f.path) == nil {
			total -= f.size
			removed++
		}
	}
	return removed
}
