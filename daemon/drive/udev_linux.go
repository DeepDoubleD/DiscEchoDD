//go:build linux

package drive

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// Watch subscribes to kernel uevents and invokes onMediaChange for every
// optical-media-change event until ctx is cancelled. It supervises the
// underlying netlink session: a read on the socket returns an error on
// its first hiccup (e.g. ENOBUFS when a uevent burst overflows the
// socket buffer), so Watch reconnects after any non-shutdown exit —
// without that, one transient failure left the daemon permanently deaf
// to disc insertions. Returns nil on clean shutdown.
func Watch(ctx context.Context, onMediaChange func(Uevent)) error {
	return superviseWatch(ctx, func(c context.Context) error {
		return watchOnce(c, onMediaChange)
	}, time.Second, 30*time.Second)
}

// superviseWatch runs watch in a loop, reconnecting after every exit
// that isn't a clean ctx cancellation. Backoff between restarts grows
// exponentially from minBackoff to maxBackoff so a persistently broken
// netlink socket doesn't become a busy-loop. Returns nil once ctx is
// done.
func superviseWatch(ctx context.Context, watch func(context.Context) error, minBackoff, maxBackoff time.Duration) error {
	backoff := minBackoff
	for {
		err := watch(ctx)
		if ctx.Err() != nil {
			return nil
		}
		slog.Warn("udev watcher exited; reconnecting", "err", err, "backoff", backoff)
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return nil
		}
		if backoff < maxBackoff {
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		}
	}
}

// watchOnce runs a single netlink monitor session: connect, stream
// uevents, and return on the first read error or ctx cancellation. The
// blocking socket read can't be cancelled in place, so a session can't
// be resumed in-place either — superviseWatch reconnects, which drops
// the stuck read goroutine along with the closed socket.
func watchOnce(ctx context.Context, onMediaChange func(Uevent)) error {
	conn, err := dialUdevMonitor()
	if err != nil {
		return fmt.Errorf("netlink connect: %w", err)
	}
	defer func() { _ = conn.Close() }()

	queue := make(chan Uevent, 16)
	errs := make(chan error, 1)
	go func() {
		for {
			payload, err := conn.readPayload()
			if err != nil {
				errs <- fmt.Errorf("netlink read: %w", err)
				return
			}
			ev, ok := ParseUevent(payload)
			if !ok {
				continue
			}
			queue <- ev
		}
	}()

	slog.Info("udev watcher started")
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-errs:
			return err
		case ev := <-queue:
			if !ev.IsOpticalMediaChange() {
				continue
			}
			// Dispatch async: classify+identify (handle) can run for
			// many seconds — cd-info alone retries for up to ~13s.
			// Running it inline stalls this read loop, the netlink
			// socket buffer overflows, and the read goroutine dies
			// with ENOBUFS. handle is concurrency-safe;
			// ClaimDriveForIdentify dedups the uevent burst a single
			// insertion produces.
			go onMediaChange(ev)
		}
	}
}
