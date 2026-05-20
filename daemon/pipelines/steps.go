package pipelines

import (
	"context"

	"github.com/jumpingmushroom/DiscEcho/daemon/state"
	"github.com/jumpingmushroom/DiscEcho/daemon/tools"
)

// RunNotifyStep emits the canonical notify step as a UI marker. The
// actual notification is sent at the job's finalise point (jobs package),
// where the full job context (output size, timing, step notes) and both
// the success AND failure paths are available — see jobs/notify.go and
// pipelines.BuildSuccessNotification. Keeping the step here preserves the
// "Notify" phase in the pipeline stepper.
func RunNotifyStep(_ context.Context, sink EventSink) {
	sink.OnStepStart(state.StepNotify)
	sink.OnStepDone(state.StepNotify, nil)
}

// EjectDeps groups everything RunEjectStep needs out of a handler's
// Deps struct. ShouldEject == nil falls back to "always eject" via
// ResolveShouldEject; nil Tools or missing eject registration is a
// silent no-op.
type EjectDeps struct {
	Tools       *tools.Registry
	ShouldEject func(ctx context.Context) bool
}

// RunEjectStep emits the canonical eject step. Failure of the underlying
// tool is reported via sink.OnStepFailed (the dashboard surfaces this)
// but never returns an error — the bits are already in the library and
// failing the whole job on a stuck tray would be wrong. Sink lifecycle
// events (start, done) bracket the call.
func RunEjectStep(ctx context.Context, sink EventSink, deps EjectDeps, drv *state.Drive) {
	sink.OnStepStart(state.StepEject)
	defer sink.OnStepDone(state.StepEject, nil)

	if deps.Tools == nil || drv == nil || drv.DevPath == "" {
		return
	}
	if !ResolveShouldEject(ctx, deps.ShouldEject) {
		return
	}
	eject, ok := deps.Tools.Get("eject")
	if !ok {
		return
	}
	if err := eject.Run(ctx, []string{drv.DevPath}, nil, "", NewStepSink(sink, state.StepEject)); err != nil {
		sink.OnStepFailed(state.StepEject, err)
	}
}
