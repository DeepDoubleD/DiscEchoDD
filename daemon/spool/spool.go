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
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

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
