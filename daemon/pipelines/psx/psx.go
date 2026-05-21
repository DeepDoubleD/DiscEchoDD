// Package psx implements pipelines.Handler for PlayStation 1 game discs.
// The pipeline body lives in pipelines/cdgame; psx supplies only the
// SYSTEM.CNF-based identifier.
//
// Identify reads SYSTEM.CNF off the disc, then tries Redump dat (tier 1)
// and BootCodeIndex (tier 2, DuckStation gamedb — provides cover URLs).
// ErrNoCandidates surfaces when both miss.
package psx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jumpingmushroom/DiscEcho/daemon/identify"
	"github.com/jumpingmushroom/DiscEcho/daemon/pipelines"
	"github.com/jumpingmushroom/DiscEcho/daemon/pipelines/cdgame"
	"github.com/jumpingmushroom/DiscEcho/daemon/state"
	"github.com/jumpingmushroom/DiscEcho/daemon/tools"
)

// Handler is an alias for cdgame.Handler; preserved so existing callers and
// compile-time interface assertions (var _ pipelines.SplittableHandler = (*psx.Handler)(nil))
// continue to compile without modification.
type Handler = cdgame.Handler

// Deps bundles the handler's dependencies. Shape preserved for callers.
type Deps struct {
	Redumper       cdgame.RedumperRipper
	CHDMan         cdgame.CHDManCompressor
	SystemCNF      identify.SystemCNFProber
	RedumpDB       *identify.RedumpDB
	BootCodeIndex  *identify.BootCodeIndex // Tier-2 fallback; DuckStation provides cover URLs
	Tools          *tools.Registry         // looked up: apprise, eject
	LibraryRoot    string
	WorkRoot       string
	LibraryProbe   func(string) error
	URLsForTrigger func(ctx context.Context, trigger string) []string
	// ShouldEject gates the rip-end eject step; nil = always eject.
	ShouldEject func(ctx context.Context) bool
}

// New builds a cdgame.Handler wired with the PSX identifier.
func New(d Deps) *cdgame.Handler {
	return cdgame.New(cdgame.Deps{
		DiscType:   state.DiscTypePSX,
		WorkPrefix: "psx",
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

// identifier reads SYSTEM.CNF then resolves via Redump dat (tier 1) and
// BootCodeIndex (tier 2).
type identifier struct {
	systemCNF     identify.SystemCNFProber
	redumpDB      *identify.RedumpDB
	bootCodeIndex *identify.BootCodeIndex
}

func (id *identifier) Identify(ctx context.Context, drv *state.Drive) (*state.Disc, []state.Candidate, error) {
	if id.systemCNF == nil {
		return nil, nil, errors.New("psx: SystemCNF prober not configured")
	}
	disc := &state.Disc{Type: state.DiscTypePSX, DriveID: drv.ID}

	info, err := id.systemCNF.Probe(ctx, drv.DevPath)
	if err != nil {
		return nil, nil, fmt.Errorf("psx: SYSTEM.CNF probe: %w", err)
	}
	if info == nil || info.BootCode == "" {
		return disc, nil, pipelines.ErrNoCandidates
	}

	// Tier 1: Redump dat.
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

	// Tier 2: BootCodeIndex (DuckStation gamedb).
	if id.bootCodeIndex != nil {
		if entry := id.bootCodeIndex.Lookup(state.DiscTypePSX, info.BootCode); entry != nil {
			region := entry.Region
			if region == "" {
				region = identify.InferRegion(info.BootCode)
			}
			disc.Title = entry.Title
			disc.Year = entry.Year
			disc.MetadataProvider = id.bootCodeIndex.Source(state.DiscTypePSX)
			disc.MetadataID = info.BootCode
			// DuckStation provides cover URLs; persist directly into
			// disc.metadata_json so DiscArt picks it up on first paint
			// without a post-pick fetch.
			if entry.CoverURL != "" {
				blob, _ := json.Marshal(map[string]any{
					"system":    "Sony PlayStation",
					"serial":    info.BootCode,
					"cover_url": entry.CoverURL,
				})
				disc.MetadataJSON = string(blob)
			}
			cand := state.Candidate{
				Source: disc.MetadataProvider, Title: entry.Title, Year: entry.Year,
				Region: region, Confidence: 90,
			}
			disc.Candidates = []state.Candidate{cand}
			return disc, disc.Candidates, nil
		}
	}

	slog.Info("psx: no Redump or BootCodeIndex match", "dev", drv.DevPath, "boot", info.BootCode)
	return disc, nil, pipelines.ErrNoCandidates
}
