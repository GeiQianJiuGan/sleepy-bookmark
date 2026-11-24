package cache

import (
	"os"
	"path/filepath"
	"testing"
)

func count(t *testing.T, dir string) (n int) {
	t.Helper()
	filepath.WalkDir(dir, func(_ string, e os.DirEntry, err error) error {
		if err == nil && !e.IsDir() {
			n++
		}
		return nil
	})
	return n
}

func TestKeptImagesStayWithinTheirLimit(t *testing.T) {
	dir := t.TempDir()
	d := New(dir)
	d.KeptMax = 10
	kept, cached := filepath.Join(dir, "kept"), filepath.Join(dir, "img")

	d.SaveImage("a", []byte("123456"), true)
	d.SaveImage("b", []byte("123456"), true) // no room left: cached instead
	d.SaveImage("c", []byte("1234"), false)
	if count(t, kept) != 1 || count(t, cached) != 2 || d.KeptBytes() != 6 {
		t.Fatalf("%d kept (%d bytes), %d cached", count(t, kept), d.KeptBytes(), count(t, cached))
	}
	if d.KeepImage("b") || !d.KeepImage("c") || !d.KeepImage("a") || d.KeptBytes() != 10 {
		t.Fatalf("keeping cached images: %d bytes kept", d.KeptBytes())
	}
	// A restart measures what is on disk.
	if again := New(dir); again.KeptBytes() != 10 {
		t.Fatalf("measured %d bytes", again.KeptBytes())
	}

	if n := d.EvictImages(0); n != 1 || count(t, kept) != 2 {
		t.Fatalf("eviction removed %d files, %d kept", n, count(t, kept))
	}
	if n := d.ReleaseKept(map[string]bool{ImageName("a"): true}); n != 1 || d.KeptBytes() != 6 {
		t.Fatalf("released %d, %d bytes kept", n, d.KeptBytes())
	}
	if _, ok := d.LoadImage("c"); !ok || count(t, cached) != 1 {
		t.Fatalf("the released image is not in the cache")
	}
	if !d.KeepImage("c") {
		t.Fatal("the room that was released is not used")
	}
}
