package jobs

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jumpingmushroom/DiscEcho/daemon/pipelines"
	"github.com/jumpingmushroom/DiscEcho/daemon/state"
	"github.com/jumpingmushroom/DiscEcho/daemon/tools"
)

// notifyStore is the slice of *state.Store the notifier needs.
type notifyStore interface {
	GetJob(ctx context.Context, id string) (*state.Job, error)
}

// slogSink adapts apprise's tools.Sink to slog so a notification failure is
// visible in the daemon log without a per-step job sink (finalise has none).
type slogSink struct{ jobID string }

func (s slogSink) Progress(float64, string, int) {}
func (s slogSink) SubStep(string)                {}
func (s slogSink) Log(level state.LogLevel, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if level == state.LogLevelWarn || level == state.LogLevelError {
		slog.Warn("notify", "job", s.jobID, "msg", msg)
	} else {
		slog.Debug("notify", "job", s.jobID, "msg", msg)
	}
}

// sendTerminalNotification builds and sends the rich notification for a job
// that has reached a terminal state. Success → "done" trigger; real failure →
// "failed" trigger; anything else (cancelled) sends nothing. Best-effort and
// silent: nil registry / no apprise tool / no subscribed URLs all no-op, and
// apprise failures never propagate (matches the pre-existing contract).
//
// The job is re-read from the store so OutputBytes and step notes (written by
// the move step before the handler returned) are reflected in the message.
func sendTerminalNotification(
	reg *tools.Registry,
	urlsFor func(ctx context.Context, trigger string) []string,
	store notifyStore,
	jobID string, disc *state.Disc, prof *state.Profile, final state.JobState,
) {
	if reg == nil || urlsFor == nil || disc == nil {
		return
	}
	apprise, ok := reg.Get("apprise")
	if !ok {
		return
	}
	// Fresh context: a cancelled job's per-job ctx is dead, but we still want
	// the failure notification to go out.
	ctx := context.Background()

	job, err := store.GetJob(ctx, jobID)
	if err != nil || job == nil {
		return
	}

	var msg pipelines.NotifyMessage
	switch final {
	case state.JobStateDone:
		msg = pipelines.BuildSuccessNotification(disc, job, prof)
	case state.JobStateFailed:
		msg = pipelines.BuildFailureNotification(disc, job, prof)
	default:
		return // cancelled / interrupted: no notification
	}

	urls := urlsFor(ctx, msg.Trigger)
	if len(urls) == 0 {
		return
	}

	argv, err := tools.BuildAppriseRichArgs(msg.Title, msg.Body, "", urls, msg.Attachments)
	if err != nil {
		// A bad attachment URL must not suppress the message — retry text-only.
		argv, err = tools.BuildAppriseRichArgs(msg.Title, msg.Body, "", urls, nil)
		if err != nil {
			slog.Warn("notify: dropping notification", "job", jobID, "err", err)
			return
		}
	}
	_ = apprise.Run(ctx, argv, nil, "", slogSink{jobID: jobID})
}
