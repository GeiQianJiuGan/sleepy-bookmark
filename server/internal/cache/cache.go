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

func (d *Disk) imagePath(url string) string {
	sum := sha256.Sum256([]byte(url))
	name := hex.EncodeToString(sum[:])
	return filepath.Join(d.dir, "img", name[:2], name)
}

// LoadImage returns a cached image and marks it as recently used.
func (d *Disk) LoadImage(url string) ([]byte, bool) {
	path := d.imagePath(url)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	t := time.Now()
	os.Chtimes(path, t, t)
	return data, true
}

func (d *Disk) HasImage(url string) bool {
	_, err := os.Stat(d.imagePath(url))
	return err == nil
}

func (d *Disk) SaveImage(url string, data []byte) error {
	path := d.imagePath(url)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
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
