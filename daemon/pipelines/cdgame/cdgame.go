// Package cdgame implements pipelines.Handler + SplittableHandler for the
// family of CD-based game discs that rip to a single .bin/.cue via redumper
// and compress to .chd via chdman: PlayStation 1, PlayStation 2, Sega Saturn,
// and (in a later milestone) the Tier-1 CD consoles. The only per-system
// difference is identification, injected as an Identifier.
//
// Pipeline shape (7 active steps; transcode skipped):
//
//	detect → identify → rip (redumper) → compress (chdman) → move → notify → eject
package cdgame

import (
	"context"

	"github.com/jumpingmushroom/DiscEcho/daemon/identify"
	"github.com/jumpingmushroom/DiscEcho/daemon/pipelines"
	"github.com/jumpingmushroom/DiscEcho/daemon/state"
	"github.com/jumpingmushroom/DiscEcho/daemon/tools"
)

// RedumperRipper is the slice of *tools.Redumper used at rip-time.
type RedumperRipper interface {
	Rip(ctx context.Context, devPath, outDir, name, mode string, sink tools.Sink) error
}

// CHDManCompressor is the slice of *tools.CHDMan used at compress-time.
type CHDManCompressor interface {
	CreateCHD(ctx context.Context, input, output string, sink tools.Sink) error
}

// Identifier is the per-system pre-rip identify step. It is the only part
// of the CD-game pipeline that varies between consoles.
type Identifier interface {
	Identify(ctx context.Context, drv *state.Drive) (*state.Disc, []state.Candidate, error)
}

// Deps bundles the handler's dependencies.
type Deps struct {
	DiscType   state.DiscType // the disc type this handler serves
	WorkPrefix string         // work-dir prefix, e.g. "psx" / "sat" / "ps2"
	Identifier Identifier     // per-system pre-rip identify

	Redumper RedumperRipper
	CHDMan   CHDManCompressor
	RedumpDB *identify.RedumpDB // post-rip MD5 verify (boot-code keyed)
	Tools    *tools.Registry    // looked up: apprise, eject

	LibraryRoot    string
	WorkRoot       string
	LibraryProbe   func(string) error
	URLsForTrigger func(ctx context.Context, trigger string) []string
	// ShouldEject gates the rip-end eject step; nil = always eject.
	ShouldEject func(ctx context.Context) bool
}

// Handler implements pipelines.Handler and pipelines.SplittableHandler for
// the CD-game family.
type Handler struct{ deps Deps }

// New builds a Handler, defaulting LibraryProbe to pipelines.ProbeWritable.
func New(d Deps) *Handler {
	if d.LibraryProbe == nil {
		d.LibraryProbe = pipelines.ProbeWritable
	}
	return &Handler{deps: d}
}

func (h *Handler) DiscType() state.DiscType { return h.deps.DiscType }

// Identify delegates to the injected per-system Identifier.
func (h *Handler) Identify(ctx context.Context, drv *state.Drive) (*state.Disc, []state.Candidate, error) {
	return h.deps.Identifier.Identify(ctx, drv)
}
