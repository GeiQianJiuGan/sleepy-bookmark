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
	"sync"
	"time"
)

type Disk struct {
	dir string
	// KeptMax bounds the kept images, in bytes.
	KeptMax int64

	// mu guards the kept images and their total size, which is measured once and then tracked.
	mu       sync.Mutex
	kept     int64
	measured bool
}

func New(dir string) *Disk { return &Disk{dir: dir} }

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
// of favorited comics, up to KeptMax, until ReleaseKept hands them back to the cache.
func (d *Disk) imagePath(name string, kept bool) string {
	dir := "img"
	if kept {
		dir = "kept"
	}
	return filepath.Join(d.dir, dir, name[:2], name)
}

// ImageName is the file name an image is stored under.
func ImageName(url string) string {
	sum := sha256.Sum256([]byte(url))
	return hex.EncodeToString(sum[:])
}

// LoadImage returns a stored image and marks a cached one as recently used.
func (d *Disk) LoadImage(url string) ([]byte, bool) {
	name := ImageName(url)
	if data, err := os.ReadFile(d.imagePath(name, true)); err == nil {
		return data, true
	}
	path := d.imagePath(name, false)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	t := time.Now()
	os.Chtimes(path, t, t)
	return data, true
}

func (d *Disk) HasImage(url string) bool {
	name := ImageName(url)
	if _, err := os.Stat(d.imagePath(name, true)); err == nil {
		return true
	}
	_, err := os.Stat(d.imagePath(name, false))
	return err == nil
}

// SaveImage stores an image: among the kept ones if keep is set and there is room, otherwise
// in the cache.
func (d *Disk) SaveImage(url string, data []byte, keep bool) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	name := ImageName(url)
	if _, err := os.Stat(d.imagePath(name, true)); err == nil {
		return nil
	}
	keep = keep && d.room(int64(len(data)))
	path := d.imagePath(name, keep)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	if keep {
		d.kept += int64(len(data))
		os.Remove(d.imagePath(name, false))
	}
	return nil
}

// KeepImage moves a cached image out of reach of EvictImages if there is room for it.
// It reports whether the image is kept.
func (d *Disk) KeepImage(url string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	name := ImageName(url)
	to := d.imagePath(name, true)
	if _, err := os.Stat(to); err == nil {
		return true
	}
	from := d.imagePath(name, false)
	info, err := os.Stat(from)
	if err != nil || !d.room(info.Size()) || os.MkdirAll(filepath.Dir(to), 0o755) != nil || os.Rename(from, to) != nil {
		return false
	}
	d.kept += info.Size()
	return true
}

// ReleaseKept hands every kept image that is not wanted any more back to the cache, as one that
// was just used. want holds the names of the images to keep.
func (d *Disk) ReleaseKept(want map[string]bool) (released int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.measure()
	now := time.Now()
	filepath.WalkDir(filepath.Join(d.dir, "kept"), func(path string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() || want[e.Name()] {
			return nil
		}
		info, err := e.Info()
		to := d.imagePath(e.Name(), false)
		if err != nil || os.MkdirAll(filepath.Dir(to), 0o755) != nil || os.Rename(path, to) != nil {
			return nil
		}
		os.Chtimes(to, now, now)
		d.kept -= info.Size()
		released++
		return nil
	})
	return released
}

// KeptBytes is the size of the kept images.
func (d *Disk) KeptBytes() int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.measure()
	return d.kept
}

func (d *Disk) room(size int64) bool {
	d.measure()
	return d.kept+size <= d.KeptMax
}

func (d *Disk) measure() {
	if d.measured {
		return
	}
	d.measured = true
	filepath.WalkDir(filepath.Join(d.dir, "kept"), func(path string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() {
			return nil
		}
		if info, err := e.Info(); err == nil {
			d.kept += info.Size()
		}
		return nil
	})
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
