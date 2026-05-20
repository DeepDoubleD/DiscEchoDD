package api

import (
	"context"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/jumpingmushroom/DiscEcho/daemon/settings"
)

// librarySizeInterval is how often the background walk re-measures every
// configured library root. On-disk size changes slowly relative to this;
// a manual recalc covers the "I just ripped something" case.
const librarySizeInterval = 30 * time.Minute

// LibrarySize is the measured on-disk size of one configured library
// root. Bytes is a du-style sum of regular-file sizes; Exists is false
// when the root directory is missing (size renders as "—", not 0).
type LibrarySize struct {
	Media  string `json:"media"` // "movies" | "tv" | "music" | "games" | "data"
	Label  string `json:"label"` // "Movies", "TV", …
	Path   string `json:"path"`
	Bytes  int64  `json:"bytes"`
	Exists bool   `json:"exists"`
}

// LibrarySizer walks the configured library roots on a timer and caches
// the per-root on-disk sizes. The walk is expensive on a multi-TB NAS,
// so handlers serve the cached snapshot and never block on a walk. A
// daemon restart loses the cache; the first walk runs async on boot and
// the snapshot reports MeasuredAt == zero until it lands.
type LibrarySizer struct {
	settings *settings.Settings

	mu         sync.Mutex
	sizes      []LibrarySize
	measuredAt time.Time
	measuring  bool
}

// NewLibrarySizer constructs a sizer bound to the given settings. Call
// Start to spin up the background walk goroutine.
func NewLibrarySizer(s *settings.Settings) *LibrarySizer {
	return &LibrarySizer{settings: s}
}

// Start kicks an immediate walk in the background, then re-walks every
// librarySizeInterval until ctx is cancelled.
func (l *LibrarySizer) Start(ctx context.Context) {
	go l.walk()
	go func() {
		t := time.NewTicker(librarySizeInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				l.walk()
			}
		}
	}()
}

// Recalc triggers an out-of-band walk in the background. It is a no-op
// while a walk is already in flight, so spamming the recalc button can't
// pile up overlapping walks.
func (l *LibrarySizer) Recalc() {
	go l.walk()
}

// Snapshot returns a copy of the cached sizes plus the last-measured
// timestamp and whether a walk is currently running.
func (l *LibrarySizer) Snapshot() ([]LibrarySize, time.Time, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]LibrarySize, len(l.sizes))
	copy(out, l.sizes)
	return out, l.measuredAt, l.measuring
}

func (l *LibrarySizer) walk() {
	l.mu.Lock()
	if l.measuring {
		l.mu.Unlock()
		return
	}
	l.measuring = true
	l.mu.Unlock()

	sizes := measureLibraries(l.settings)

	l.mu.Lock()
	l.sizes = sizes
	l.measuredAt = time.Now()
	l.measuring = false
	l.mu.Unlock()
}

// measureLibraries walks each configured root and returns its size. Roots
// resolving to the same cleaned path are walked once and share the result
// (a pathological identical-path config would otherwise double-count the
// disk work). Nested roots — e.g. a "data" root that is the parent of the
// others — are NOT detected and would double-count; the default layout is
// sibling subdirs, so this is left as a known limitation.
func measureLibraries(s *settings.Settings) []LibrarySize {
	cache := map[string]struct {
		bytes  int64
		exists bool
	}{}
	out := make([]LibrarySize, 0, len(settings.AllMediaRoots))
	for _, m := range settings.AllMediaRoots {
		path := s.LibraryFor(m)
		row := LibrarySize{Media: string(m), Label: mediaLabel(m), Path: path}
		if path == "" {
			out = append(out, row)
			continue
		}
		key := filepath.Clean(path)
		if c, seen := cache[key]; seen {
			row.Bytes, row.Exists = c.bytes, c.exists
			out = append(out, row)
			continue
		}
		bytes, exists := dirSize(key)
		cache[key] = struct {
			bytes  int64
			exists bool
		}{bytes, exists}
		row.Bytes, row.Exists = bytes, exists
		out = append(out, row)
	}
	return out
}

// dirSize sums the sizes of all regular files under root. Per-entry errors
// (permission, transient) are skipped so a single bad file doesn't void the
// whole total. Returns exists=false only when the root itself is missing.
func dirSize(root string) (total int64, exists bool) {
	if _, err := os.Stat(root); err != nil {
		return 0, false
	}
	_ = filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries, keep walking
		}
		if !d.Type().IsRegular() {
			return nil
		}
		if info, ierr := d.Info(); ierr == nil {
			total += info.Size()
		}
		return nil
	})
	return total, true
}

func mediaLabel(m settings.MediaRoot) string {
	switch m {
	case settings.MediaMovies:
		return "Movies"
	case settings.MediaTV:
		return "TV"
	case settings.MediaMusic:
		return "Music"
	case settings.MediaGames:
		return "Games"
	case settings.MediaData:
		return "Data"
	}
	return string(m)
}

// ArraySummary is the whole-mount capacity for the library filesystem(s),
// deduped across distinct mounts. Mirrors what `df` reports.
type ArraySummary struct {
	UsedBytes      int64 `json:"used_bytes"`
	TotalBytes     int64 `json:"total_bytes"`
	AvailableBytes int64 `json:"available_bytes"`
}

// LibrariesInfo is the GET /api/system/libraries payload.
type LibrariesInfo struct {
	Libraries  []LibrarySize `json:"libraries"`   // sorted desc by Bytes
	MeasuredAt string        `json:"measured_at"` // RFC3339, "" if never measured
	Measuring  bool          `json:"measuring"`
	Array      ArraySummary  `json:"array"`
}

// GetSystemLibraries returns the cached per-library sizes plus a cheap
// statfs-based array summary. Safe to poll: it reads the cache and stats
// distinct mounts once.
func (h *Handlers) GetSystemLibraries(w http.ResponseWriter, r *http.Request) {
	info := LibrariesInfo{Libraries: []LibrarySize{}}
	if h.LibrarySizer != nil {
		sizes, measuredAt, measuring := h.LibrarySizer.Snapshot()
		sort.SliceStable(sizes, func(i, j int) bool { return sizes[i].Bytes > sizes[j].Bytes })
		info.Libraries = sizes
		info.Measuring = measuring
		if !measuredAt.IsZero() {
			info.MeasuredAt = measuredAt.UTC().Format(time.RFC3339)
		}
	}
	used, total, avail := h.libraryFSBytes(r.Context())
	info.Array = ArraySummary{UsedBytes: used, TotalBytes: total, AvailableBytes: avail}
	writeJSON(w, http.StatusOK, info)
}

// RecalcSystemLibraries kicks off a background re-walk and returns the
// current (likely measuring) snapshot.
func (h *Handlers) RecalcSystemLibraries(w http.ResponseWriter, r *http.Request) {
	if h.LibrarySizer != nil {
		h.LibrarySizer.Recalc()
	}
	h.GetSystemLibraries(w, r)
}
