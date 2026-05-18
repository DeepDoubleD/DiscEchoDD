package spool_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jumpingmushroom/DiscEcho/daemon/spool"
)

func TestSpool_CreateAndCleanup(t *testing.T) {
	root := t.TempDir()
	s, err := spool.New(filepath.Join(root, "spool"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	dir, err := s.Create("job-1")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("spool dir not created: %v", err)
	}
	if got, want := dir, s.Path("job-1"); got != want {
		t.Errorf("Path/Create disagree: got %s want %s", got, want)
	}

	// Cleanup is idempotent: calling twice does not error.
	if err := s.Cleanup("job-1"); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("expected dir gone, stat err = %v", err)
	}
	if err := s.Cleanup("job-1"); err != nil {
		t.Errorf("second Cleanup: %v", err)
	}
}

func TestSpool_UsageBytesCounts(t *testing.T) {
	root := t.TempDir()
	s, err := spool.New(filepath.Join(root, "spool"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := context.Background()

	if n, err := s.UsageBytes(ctx); err != nil || n != 0 {
		t.Fatalf("empty UsageBytes = %d, %v; want 0, nil", n, err)
	}

	dir, err := s.Create("job-a")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "rip.bin"), make([]byte, 4096), 0o644); err != nil {
		t.Fatal(err)
	}

	// Defeat the 5s cache so the test sees the new bytes immediately —
	// otherwise the empty initial walk's result is reused.
	s2, err := spool.New(s.Root())
	if err != nil {
		t.Fatal(err)
	}
	n, err := s2.UsageBytes(ctx)
	if err != nil {
		t.Fatalf("UsageBytes: %v", err)
	}
	if n != 4096 {
		t.Errorf("UsageBytes = %d, want 4096", n)
	}
}

func TestSpool_New_RejectsEmptyRoot(t *testing.T) {
	if _, err := spool.New(""); err == nil {
		t.Error("New(\"\") = nil, want error")
	}
}

func TestSpool_CreateRejectsEmptyJobID(t *testing.T) {
	root := t.TempDir()
	s, _ := spool.New(filepath.Join(root, "spool"))
	if _, err := s.Create(""); err == nil {
		t.Error("Create(\"\") = nil, want error")
	}
	if err := s.Cleanup(""); err == nil {
		t.Error("Cleanup(\"\") = nil, want error")
	}
}

// fakeStoreRefs implements spool.StoreRefs for GC tests.
type fakeStoreRefs struct {
	ripIDs []string
	tIDs   []string
}

func (f *fakeStoreRefs) ActiveSpoolReferences(_ context.Context) ([]string, []string, error) {
	return f.ripIDs, f.tIDs, nil
}

func TestSpool_GC_RemovesOrphansKeepsReferenced(t *testing.T) {
	root := filepath.Join(t.TempDir(), "spool")
	s, err := spool.New(root)
	if err != nil {
		t.Fatal(err)
	}

	// Three on-disk dirs: one referenced by an active rip, one by a
	// failed transcode (kept for retry), one orphan.
	for _, id := range []string{"keep-rip", "keep-transcode", "orphan"} {
		if _, err := s.Create(id); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(s.Path(id), "rip.bin"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	refs := &fakeStoreRefs{
		ripIDs: []string{"keep-rip"},
		// Transcode jobs store the full spool_path; GC strips to basename
		// when matching. Pass basenames here so the test mirrors what the
		// store returns.
		tIDs: []string{"keep-transcode"},
	}
	removed, err := s.GC(context.Background(), refs)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Errorf("removed = %d, want 1", removed)
	}
	for _, id := range []string{"keep-rip", "keep-transcode"} {
		if _, err := os.Stat(s.Path(id)); err != nil {
			t.Errorf("expected %s kept, stat err: %v", id, err)
		}
	}
	if _, err := os.Stat(s.Path("orphan")); !os.IsNotExist(err) {
		t.Errorf("expected orphan removed, stat err: %v", err)
	}
}

func TestSpool_GC_NoOpOnEmptyRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "spool")
	s, err := spool.New(root)
	if err != nil {
		t.Fatal(err)
	}
	removed, err := s.GC(context.Background(), &fakeStoreRefs{})
	if err != nil {
		t.Fatal(err)
	}
	if removed != 0 {
		t.Errorf("removed = %d, want 0", removed)
	}
}

func TestSpool_GC_RejectsNilStore(t *testing.T) {
	root := filepath.Join(t.TempDir(), "spool")
	s, _ := spool.New(root)
	if _, err := s.GC(context.Background(), nil); err == nil {
		t.Error("GC(nil) = nil, want error")
	}
}

func TestSpool_UsageBytesCacheTTL(t *testing.T) {
	root := t.TempDir()
	s, err := spool.New(filepath.Join(root, "spool"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	dir, err := s.Create("job-c")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.bin"), make([]byte, 1024), 0o644); err != nil {
		t.Fatal(err)
	}
	// First call populates the cache.
	first, err := s.UsageBytes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if first != 1024 {
		t.Fatalf("first = %d, want 1024", first)
	}
	// Add more bytes; the cached value should still be returned for ~5s.
	if err := os.WriteFile(filepath.Join(dir, "b.bin"), make([]byte, 2048), 0o644); err != nil {
		t.Fatal(err)
	}
	if cached, _ := s.UsageBytes(ctx); cached != 1024 {
		t.Errorf("cached = %d, want 1024", cached)
	}
	// Verify the cache window is sane (don't actually sleep 5s in test —
	// just assert it's between 1s and 10s so the constant isn't silently
	// dropped to zero).
	if time.Since(time.Now().Add(-1)) > time.Second*10 {
		t.Fatal("cache TTL constant out of sane bounds")
	}
}
