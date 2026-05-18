// Package spool owns the on-disk staging area where rip-job outputs
// live between the drive-bound rip phase and the compute-bound
// transcode phase of the split pipeline.
//
// Layout: ${rootDir}/<rip_job_id>/<rip outputs>
//
// The rip handler writes its intermediate artefact (raw MKV, redumper
// dump, …) into Path(jobID). The compute worker then reads from that
// directory, runs the transcode step, and on success calls Cleanup.
// On transcode failure the spool stays intact so the user can retry
// without re-ripping the disc — Cleanup is explicit, not deferred.
//
// Audio CD and DATA pipelines stay monolithic for v1 and don't use
// the spool at all; their tmpdir is still owned by the handler and
// auto-removed at end of Run.
package spool

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// StoreRefs is the slice of state.Store the spool GC needs. Defined as
// an interface to keep the spool package free of state-import.
type StoreRefs interface {
	ActiveSpoolReferences(ctx context.Context) (ripIDs []string, transcodeSpoolBasenames []string, err error)
}

// Spool is the on-disk staging area accessor. Construct via New.
type Spool struct {
	rootDir string

	// usageBytes caches the result of the most recent root-dir walk so
	// the dashboard widget and the drive-worker backpressure check
	// (Phase 5) don't blow up I/O at high frequency. The cached value is
	// recomputed lazily on UsageBytes() calls past the TTL.
	mu          sync.Mutex
	cachedBytes int64
	cachedAt    time.Time

	// gen ticks every Cleanup / Create so concurrent UsageBytes callers
	// can invalidate stale caches without racing on the mutex.
	gen atomic.Uint64
}

// usageTTL is how long a UsageBytes() result is reused before a fresh
// filesystem walk happens. 5s is short enough that the dashboard widget
// feels live; long enough that 10 concurrent SSE clients won't fan out
// 10 walks per second.
const usageTTL = 5 * time.Second

// New returns a Spool rooted at rootDir. The root is created with mode
// 0o755 if missing. Callers typically pass ${DISCECHO_DATA}/spool.
func New(rootDir string) (*Spool, error) {
	if rootDir == "" {
		return nil, errors.New("spool: rootDir is required")
	}
	if err := os.MkdirAll(rootDir, 0o755); err != nil {
		return nil, fmt.Errorf("spool: mkdir root: %w", err)
	}
	return &Spool{rootDir: rootDir}, nil
}

// Root returns the configured root directory. Useful for tests + the
// settings UI.
func (s *Spool) Root() string { return s.rootDir }

// Path returns the absolute path of the spool subdirectory for the
// given rip-job ID. Does not create it; pair with Create() when you
// need the directory on disk.
func (s *Spool) Path(jobID string) string {
	return filepath.Join(s.rootDir, jobID)
}

// Create makes the spool subdirectory for jobID and returns its path.
// Idempotent: re-creating an existing dir is a no-op.
func (s *Spool) Create(jobID string) (string, error) {
	if jobID == "" {
		return "", errors.New("spool: jobID is required")
	}
	dir := s.Path(jobID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("spool: mkdir %s: %w", dir, err)
	}
	s.gen.Add(1)
	return dir, nil
}

// Cleanup removes the spool subdirectory for jobID. Missing dir is not
// an error — repeated cleanups are safe.
func (s *Spool) Cleanup(jobID string) error {
	if jobID == "" {
		return errors.New("spool: jobID is required")
	}
	if err := os.RemoveAll(s.Path(jobID)); err != nil {
		return fmt.Errorf("spool: rm %s: %w", s.Path(jobID), err)
	}
	s.gen.Add(1)
	return nil
}

// GC scans the spool root and removes any subdirectory whose basename
// isn't in the set the store considers in-use (active rip job IDs +
// transcode jobs' spool_path basenames). Called at daemon startup so
// a crash mid-pipeline doesn't leak the spool dir; on a clean shutdown
// the running jobs flip to interrupted via MarkInterruptedJobs and
// their spool stays for the user's retry-transcode flow.
//
// Files (not directories) directly under the root are also removed —
// nothing should write loose files at the root.
//
// Returns the number of entries removed (best-effort: individual
// remove errors are logged + skipped, not returned).
func (s *Spool) GC(ctx context.Context, store StoreRefs) (int, error) {
	if store == nil {
		return 0, errors.New("spool: GC needs a StoreRefs")
	}
	ripIDs, transcodeBases, err := store.ActiveSpoolReferences(ctx)
	if err != nil {
		return 0, fmt.Errorf("spool: load active references: %w", err)
	}
	keep := make(map[string]bool, len(ripIDs)+len(transcodeBases))
	for _, id := range ripIDs {
		keep[id] = true
	}
	for _, p := range transcodeBases {
		keep[p] = true
	}

	entries, err := os.ReadDir(s.rootDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("spool: read root: %w", err)
	}
	removed := 0
	for _, e := range entries {
		if keep[e.Name()] {
			continue
		}
		full := filepath.Join(s.rootDir, e.Name())
		if err := os.RemoveAll(full); err != nil {
			slog.Warn("spool: GC remove failed", "path", full, "err", err)
			continue
		}
		removed++
	}
	if removed > 0 {
		s.gen.Add(1)
	}
	return removed, nil
}

// UsageBytes returns the total bytes consumed under rootDir. Cached
// for usageTTL between successive calls; pass a cancellable ctx so
// callers (HTTP handlers) can bail out of an in-progress walk.
func (s *Spool) UsageBytes(ctx context.Context) (int64, error) {
	s.mu.Lock()
	if !s.cachedAt.IsZero() && time.Since(s.cachedAt) < usageTTL {
		v := s.cachedBytes
		s.mu.Unlock()
		return v, nil
	}
	s.mu.Unlock()

	var total int64
	err := filepath.WalkDir(s.rootDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			// Missing entries (concurrent Cleanup) are a soft skip; any
			// other error stops the walk.
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			return nil
		}
		fi, statErr := d.Info()
		if statErr != nil {
			if os.IsNotExist(statErr) {
				return nil
			}
			return statErr
		}
		total += fi.Size()
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("spool: walk: %w", err)
	}
	s.mu.Lock()
	s.cachedBytes = total
	s.cachedAt = time.Now()
	s.mu.Unlock()
	return total, nil
}
