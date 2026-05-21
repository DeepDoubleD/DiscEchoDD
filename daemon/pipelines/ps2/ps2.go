// Package ps2 implements pipelines.Handler for PlayStation 2 game discs.
// The pipeline body lives in pipelines/cdgame; ps2 supplies only the
// SYSTEM.CNF-based identifier (BootCodeIndex tier-2 = PCSX2 GameDB).
package ps2

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jumpingmushroom/DiscEcho/daemon/identify"
	"github.com/jumpingmushroom/DiscEcho/daemon/pipelines"
	"github.com/jumpingmushroom/DiscEcho/daemon/pipelines/cdgame"
	"github.com/jumpingmushroom/DiscEcho/daemon/state"
	"github.com/jumpingmushroom/DiscEcho/daemon/tools"
)

// Handler is an alias so callers/tests referencing ps2.Handler keep working.
type Handler = cdgame.Handler

// Deps bundles the handler's dependencies. Shape preserved for callers.
type Deps struct {
	Redumper       cdgame.RedumperRipper
	CHDMan         cdgame.CHDManCompressor
	SystemCNF      identify.SystemCNFProber
	RedumpDB       *identify.RedumpDB
	BootCodeIndex  *identify.BootCodeIndex
	Tools          *tools.Registry
	LibraryRoot    string
	WorkRoot       string
	LibraryProbe   func(string) error
	URLsForTrigger func(ctx context.Context, trigger string) []string
	ShouldEject    func(ctx context.Context) bool
}

// New builds a cdgame.Handler wired with the PS2 identifier.
func New(d Deps) *cdgame.Handler {
	return cdgame.New(cdgame.Deps{
		DiscType:   state.DiscTypePS2,
		WorkPrefix: "ps2",
		RipFormat:  cdgame.RipFormatDVD,
		Identifier: &identifier{
			systemCNF:     d.SystemCNF,
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
	systemCNF     identify.SystemCNFProber
	redumpDB      *identify.RedumpDB
	bootCodeIndex *identify.BootCodeIndex
}

func (id *identifier) Identify(ctx context.Context, drv *state.Drive) (*state.Disc, []state.Candidate, error) {
	if id.systemCNF == nil {
		return nil, nil, errors.New("ps2: SystemCNF prober not configured")
	}
	disc := &state.Disc{Type: state.DiscTypePS2, DriveID: drv.ID}

	info, err := id.systemCNF.Probe(ctx, drv.DevPath)
	if err != nil {
		return nil, nil, fmt.Errorf("ps2: SYSTEM.CNF probe: %w", err)
	}
	if info == nil || info.BootCode == "" {
		return disc, nil, pipelines.ErrNoCandidates
	}

	// Tier 1: Redump dat (rare hit with modern public dats, but when it
	// hits, post-rip MD5 verify at the compress step also works).
	if id.redumpDB != nil {
		if entry := id.redumpDB.LookupByBootCode(info.BootCode); entry != nil {
			disc.Title = entry.Title
			disc.Year = entry.Year
			disc.MetadataProvider = "Redump"
			disc.MetadataID = entry.BootCode
			cand := state.Candidate{
				Source: "Redump", Title: entry.Title, Year: entry.Year,
				Region: entry.Region, Confidence: 100,
			}
			disc.Candidates = []state.Candidate{cand}
			return disc, disc.Candidates, nil
		}
	}

	// Tier 2: BootCodeIndex (PCSX2 GameDB).
	if id.bootCodeIndex != nil {
		if entry := id.bootCodeIndex.Lookup(state.DiscTypePS2, info.BootCode); entry != nil {
			region := entry.Region
			if region == "" {
				region = identify.InferRegion(info.BootCode)
			}
			disc.Title = entry.Title
			disc.Year = entry.Year
			disc.MetadataProvider = id.bootCodeIndex.Source(state.DiscTypePS2)
			disc.MetadataID = info.BootCode
			cand := state.Candidate{
				Source: disc.MetadataProvider, Title: entry.Title, Year: entry.Year,
				Region: region, Confidence: 90,
			}
			disc.Candidates = []state.Candidate{cand}
			return disc, disc.Candidates, nil
		}
	}

	slog.Info("ps2: no Redump or BootCodeIndex match", "dev", drv.DevPath, "boot", info.BootCode)
	return disc, nil, pipelines.ErrNoCandidates
}
