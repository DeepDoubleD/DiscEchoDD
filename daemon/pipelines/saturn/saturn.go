// Package saturn implements pipelines.Handler for Sega Saturn game discs.
// The pipeline body lives in pipelines/cdgame; saturn supplies only the
// IP.BIN-based identifier.
package saturn

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jumpingmushroom/DiscEcho/daemon/identify"
	"github.com/jumpingmushroom/DiscEcho/daemon/pipelines"
	"github.com/jumpingmushroom/DiscEcho/daemon/pipelines/cdgame"
	"github.com/jumpingmushroom/DiscEcho/daemon/state"
	"github.com/jumpingmushroom/DiscEcho/daemon/tools"
)

// Handler is an alias so callers/tests referencing saturn.Handler keep working.
type Handler = cdgame.Handler

// Deps bundles the handler's dependencies. Shape preserved for callers.
type Deps struct {
	Redumper       cdgame.RedumperRipper
	CHDMan         cdgame.CHDManCompressor
	SaturnProber   identify.SaturnProber
	RedumpDB       *identify.RedumpDB
	BootCodeIndex  *identify.BootCodeIndex
	Tools          *tools.Registry
	LibraryRoot    string
	WorkRoot       string
	LibraryProbe   func(string) error
	URLsForTrigger func(ctx context.Context, trigger string) []string
	ShouldEject    func(ctx context.Context) bool
}

// New builds a cdgame.Handler wired with the Saturn identifier.
func New(d Deps) *cdgame.Handler {
	return cdgame.New(cdgame.Deps{
		DiscType:   state.DiscTypeSAT,
		WorkPrefix: "sat",
		Identifier: &identifier{
			prober:        d.SaturnProber,
			redumpDB:      d.RedumpDB,
			bootCodeIndex: d.BootCodeIndex,
		},
		Redumper:       d.Redumper,
		CHDMan:         d.CHDMan,
		RedumpDB:       d.RedumpDB,
		Tools:          d.Tools,
		LibraryRoot:    d.LibraryRoot,
		WorkRoot:       d.WorkRoot,
		LibraryProbe:   d.LibraryProbe,
		URLsForTrigger: d.URLsForTrigger,
		ShouldEject:    d.ShouldEject,
	})
}

type identifier struct {
	prober        identify.SaturnProber
	redumpDB      *identify.RedumpDB
	bootCodeIndex *identify.BootCodeIndex
}

func (id *identifier) Identify(ctx context.Context, drv *state.Drive) (*state.Disc, []state.Candidate, error) {
	disc := &state.Disc{Type: state.DiscTypeSAT, DriveID: drv.ID}

	if id.prober == nil {
		return nil, nil, errors.New("saturn: SaturnProber not configured")
	}
	info, err := id.prober.Probe(ctx, drv.DevPath)
	if err != nil {
		return nil, nil, fmt.Errorf("saturn: IP.BIN probe: %w", err)
	}
	if info == nil || info.ProductNumber == "" {
		return disc, nil, pipelines.ErrNoCandidates
	}

	code := strings.ToUpper(strings.TrimSpace(info.ProductNumber))
	if code == "" {
		return disc, nil, pipelines.ErrNoCandidates
	}

	// Tier 1: Redump dat.
	if id.redumpDB != nil {
		if entry := id.redumpDB.LookupByBootCode(code); entry != nil {
			disc.Title = entry.Title
			disc.Year = entry.Year
			disc.MetadataProvider = "Redump"
			disc.MetadataID = entry.BootCode
			disc.Candidates = []state.Candidate{{
				Source: "Redump", Title: entry.Title, Year: entry.Year,
				Region: entry.Region, Confidence: 100,
			}}
			return disc, disc.Candidates, nil
		}
	}

	// Tier 2: BootCodeIndex (Libretro).
	if id.bootCodeIndex != nil {
		if entry := id.bootCodeIndex.Lookup(state.DiscTypeSAT, code); entry != nil {
			region := entry.Region
			if region == "" {
				region = identify.InferRegion(code)
			}
			disc.Title = entry.Title
			disc.Year = entry.Year
			disc.MetadataProvider = id.bootCodeIndex.Source(state.DiscTypeSAT)
			disc.MetadataID = code
			disc.Candidates = []state.Candidate{{
				Source: disc.MetadataProvider, Title: entry.Title, Year: entry.Year,
				Region: region, Confidence: 90,
			}}
			return disc, disc.Candidates, nil
		}
	}

	slog.Info("sat: no Redump or BootCodeIndex match", "dev", drv.DevPath, "product", code)
	return disc, nil, pipelines.ErrNoCandidates
}
