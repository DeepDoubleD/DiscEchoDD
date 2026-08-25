// Package wiiu implements pipelines.Handler for Nintendo Wii U game
// discs.
//
// Pipeline shape (6 active steps; transcode AND compress skipped):
//
//	detect → identify → rip (redumper wiiu, --bd-raw) → move → notify → eject
//
// Wii U discs are BD-form-factor media (single-layer 25GB or dual-layer
// 50GB), unlike Wii/GameCube's non-standard DVD variant -- but a stock
// BD-ROM drive still can't read them cleanly: per RibShark's OmniDrive
// firmware documentation, a stock read returns the disc's raw sectors
// still AES-encrypted, and only an OmniDrive-flashed drive's raw BD
// path (redumper's --bd-raw, mirroring --dvd-raw for Wii/Xbox 360)
// gets a clean sector-for-sector dump at all. So, same as
// pipelines/wii, classify.go can never recognise a Wii U disc pre-rip;
// discflow.go falls back to a DATA card on a BD/console-role drive,
// and the user manually overrides it to WIIU via the same dropdown
// Wii/CD32/FM Towns/Pippin already use.
//
// UNLIKE PS3 (pipelines/ps3), this package does NOT vendor or embed
// any decryption code or key material. A PS3 disc's real protection is
// per-file content encryption keyed by a small, disc-specific value
// the community treats as distributable sector-layout metadata (an
// IRD file) -- that's why ps3dumper-cli can fetch what it needs from a
// public IRD library and hand back a ready-to-use folder tree. A Wii U
// dump instead needs TWO keys to become usable: a per-disc title key
// AND Nintendo's single platform-wide "common key" -- the latter is
// not disc-specific metadata, it is one fixed DRM master secret shared
// by every Wii U ever made. This daemon never embeds that key and
// never fetches it from anywhere; the user supplies both files
// themselves (see tryDecrypt below).
//
// Decryption itself is delegated to wudecrypt
// (https://github.com/maki-chan/wudecrypt, AGPL-3.0) as an external
// tool DiscEcho shells out to at runtime -- installed as a separate
// binary in the Docker image (like MakeMKV/HandBrake/redumper/whipper,
// all of which are themselves proprietary or copyleft-licensed) rather
// than vendored/compiled into DiscEcho's own MIT-licensed source. Its
// AGPL-3.0 license stays scoped to that one binary; see NOTICE.md. No
// clean-room reimplementation is possible here: the WUD partition/H3
// hash-tree format has no independent public specification, only the
// existing (AGPL/GPL/unlicensed) community tools' own source -- and
// this is Nintendo disc-protection DRM regardless of whose code
// decrypts it, a DMCA §1201 question orthogonal to copyright licensing.
//
// If the user hasn't supplied both key files, RunTranscode falls back
// to moving the raw encrypted dump (plus whatever redumper's
// protection scan reports) straight to the library, same as Wii's ISO
// -- the raw dump is always a valid, Redump-cataloguable deliverable
// on its own; decryption is a bonus, never a requirement.
//
// This also means identification works exactly like Wii: nothing is
// readable pre-rip (Identify always returns ErrNoCandidates), and
// RunTranscode's post-rip MD5 lookup against a user-supplied Redump
// Wii U dat is the only identification path -- Redump catalogues Wii U
// by the hash of this same raw encrypted dump, not a decrypted image,
// so the BYO-dat convention every other console here uses applies
// unmodified.
//
// NOT YET VERIFIED AGAINST REAL HARDWARE: built from redumper's
// documented --bd-raw flag and OmniDrive's published Wii U support,
// but the author has no Wii U media to test against. Confirm the
// output is actually a sane raw dump (right size, redumper doesn't
// choke on the BD structure) before trusting this in production.
package wiiu

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/jumpingmushroom/DiscEcho/daemon/identify"
	"github.com/jumpingmushroom/DiscEcho/daemon/pipelines"
	"github.com/jumpingmushroom/DiscEcho/daemon/state"
	"github.com/jumpingmushroom/DiscEcho/daemon/tools"
)

// RedumperRipper is the subset of *tools.Redumper used at rip-time.
type RedumperRipper interface {
	Rip(ctx context.Context, devPath, outDir, name, mode string, sink tools.Sink) error
}

// WuDecryptTool is the subset of *tools.WuDecrypt used at transcode-time.
type WuDecryptTool interface {
	Decrypt(ctx context.Context, wudPath, outDir, commonKeyPath, discKeyPath string, sink tools.Sink) error
}

// Deps bundles the handler's dependencies.
type Deps struct {
	Redumper RedumperRipper
	RedumpDB *identify.RedumpDB
	// WuDecrypt is optional. When nil (or KeysDir is empty), RunTranscode
	// always ships the raw dump -- see tryDecrypt.
	WuDecrypt      WuDecryptTool
	KeysDir        string          // holds common.key + per-disc <md5>.key files; see tryDecrypt
	Tools          *tools.Registry // looked up: apprise, eject
	LibraryRoot    string
	WorkRoot       string
	LibraryProbe   func(string) error
	URLsForTrigger func(ctx context.Context, trigger string) []string
	// ShouldEject gates the rip-end eject step; nil = always eject.
	ShouldEject func(ctx context.Context) bool
}

// Handler implements pipelines.Handler for Wii U.
type Handler struct{ deps Deps }

// New returns a Handler with the given dependencies.
func New(d Deps) *Handler {
	if d.LibraryProbe == nil {
		d.LibraryProbe = pipelines.ProbeWritable
	}
	return &Handler{deps: d}
}

func (h *Handler) DiscType() state.DiscType { return state.DiscTypeWIIU }

// Identify always returns the disc typed as WIIU with no candidates and
// no TOCHash -- see the package doc. A stock read gets nothing usable
// off a Wii U disc pre-rip (still AES-encrypted at the sector level),
// and the raw OmniDrive read redumper needs isn't something this
// daemon can do cheaply outside of an actual rip. RunTranscode's
// post-rip MD5 lookup does the real identification once the dump
// exists, same as Wii.
func (h *Handler) Identify(_ context.Context, drv *state.Drive) (*state.Disc, []state.Candidate, error) {
	disc := &state.Disc{Type: state.DiscTypeWIIU, DriveID: drv.ID}
	return disc, nil, pipelines.ErrNoCandidates
}

// Plan returns the 6-active-step plan; both transcode and compress are
// skipped. Used by the monolithic Run fallback.
func (h *Handler) Plan(_ *state.Disc, _ *state.Profile) []pipelines.StepPlan {
	skipped := map[state.StepID]bool{
		state.StepTranscode: true,
		state.StepCompress:  true,
	}
	out := make([]pipelines.StepPlan, 0, len(state.CanonicalSteps()))
	for _, sid := range state.CanonicalSteps() {
		out = append(out, pipelines.StepPlan{ID: sid, Skip: skipped[sid]})
	}
	return out
}

// PlanRip — rip-half: detect, identify, rip, eject. Transcode-half marked Skip.
func (h *Handler) PlanRip(_ *state.Disc, _ *state.Profile) []pipelines.StepPlan {
	transcodeHalf := map[state.StepID]bool{
		state.StepTranscode: true,
		state.StepCompress:  true,
		state.StepMove:      true,
		state.StepNotify:    true,
	}
	out := make([]pipelines.StepPlan, 0, 8)
	for _, sid := range state.CanonicalSteps() {
		out = append(out, pipelines.StepPlan{ID: sid, Skip: transcodeHalf[sid]})
	}
	return out
}

// PlanTranscode — transcode-half: Wii U has no transcode AND no
// compress (the raw encrypted dump is the deliverable, same as Wii's
// .iso). Only move + notify are active.
func (h *Handler) PlanTranscode(_ *state.Disc, _ *state.Profile) []pipelines.StepPlan {
	out := make([]pipelines.StepPlan, 0, 4)
	for _, sid := range state.CanonicalTranscodeSteps() {
		skip := sid == state.StepTranscode || sid == state.StepCompress
		out = append(out, pipelines.StepPlan{ID: sid, Skip: skip})
	}
	return out
}

const spoolName = "rip"

// Run is the monolithic fallback path.
func (h *Handler) Run(ctx context.Context, drv *state.Drive, disc *state.Disc, prof *state.Profile, sink pipelines.EventSink) error {
	tmpdir, err := h.createWorkDir(disc.ID)
	if err != nil {
		sink.OnStepStart(state.StepRip)
		sink.OnStepFailed(state.StepRip, err)
		return err
	}
	defer func() { _ = os.RemoveAll(tmpdir) }()
	result, err := h.RunRip(ctx, drv, disc, prof, tmpdir, sink)
	if err != nil {
		return err
	}
	return h.RunTranscode(ctx, result, disc, prof, sink)
}

// RunRip executes the drive-bound half: detect, identify, redumper
// wiiu mode (--bd-raw) → spoolDir/rip/rip.iso, eject.
func (h *Handler) RunRip(ctx context.Context, drv *state.Drive, disc *state.Disc, prof *state.Profile, spoolDir string, sink pipelines.EventSink) (pipelines.RipResult, error) {
	sink.OnStepStart(state.StepDetect)
	sink.OnStepDone(state.StepDetect, nil)
	sink.OnStepStart(state.StepIdentify)
	sink.OnStepDone(state.StepIdentify, nil)

	sink.OnStepStart(state.StepRip)
	if err := h.deps.LibraryProbe(h.deps.LibraryRoot); err != nil {
		sink.OnStepFailed(state.StepRip, err)
		return pipelines.RipResult{}, fmt.Errorf("library probe: %w", err)
	}
	if h.deps.Redumper == nil {
		err := errors.New("wiiu: redumper not configured")
		sink.OnStepFailed(state.StepRip, err)
		return pipelines.RipResult{}, err
	}
	ripDir := filepath.Join(spoolDir, "rip")
	if err := os.MkdirAll(ripDir, 0o755); err != nil {
		sink.OnStepFailed(state.StepRip, err)
		return pipelines.RipResult{}, fmt.Errorf("mkdir rip: %w", err)
	}
	if err := h.deps.Redumper.Rip(ctx, drv.DevPath, ripDir, spoolName, "wiiu", pipelines.NewStepSink(sink, state.StepRip)); err != nil {
		sink.OnStepFailed(state.StepRip, err)
		return pipelines.RipResult{}, fmt.Errorf("redumper: %w", err)
	}
	isoPath := filepath.Join(ripDir, spoolName+".iso")
	sink.OnStepDone(state.StepRip, map[string]any{"file": isoPath})

	pipelines.RunEjectStep(ctx, sink, pipelines.EjectDeps{
		Tools:       h.deps.Tools,
		ShouldEject: h.deps.ShouldEject,
	}, drv)

	return pipelines.RipResult{SpoolPath: spoolDir}, nil
}

// RunTranscode executes the compute-bound half: post-rip MD5 identify,
// a best-effort decrypt attempt (see tryDecrypt), then move to the
// library, notify. No compress. When decryption succeeds the
// deliverable is the decrypted GM-partition folder tree (raw encrypted
// dump discarded, same as PS3's decrypted-only deliverable); otherwise
// it's the raw, still-encrypted dump -- see the package doc for why
// both are legitimate outcomes here.
func (h *Handler) RunTranscode(ctx context.Context, result pipelines.RipResult, disc *state.Disc, prof *state.Profile, sink pipelines.EventSink) error {
	ripDir := filepath.Join(result.SpoolPath, "rip")
	isoPath := filepath.Join(ripDir, spoolName+".iso")

	md5sum := h.md5Identify(isoPath, disc)

	sink.OnStepStart(state.StepMove)
	region := ""
	if len(disc.Candidates) > 0 {
		region = disc.Candidates[0].Region
	}
	rel, err := pipelines.RenderOutputPath(prof.OutputPathTemplate, pipelines.OutputFields{
		Title: disc.Title, Year: disc.Year, Region: region,
	})
	if err != nil {
		sink.OnStepFailed(state.StepMove, err)
		return err
	}

	if decryptedDir, ok := h.tryDecrypt(ctx, isoPath, md5sum, sink); ok {
		dst := filepath.Join(h.deps.LibraryRoot, strings.TrimSuffix(rel, filepath.Ext(rel)))
		if err := moveDir(decryptedDir, dst); err != nil {
			sink.OnStepFailed(state.StepMove, err)
			return err
		}
		sink.OnStepDone(state.StepMove, map[string]any{"path": dst, "decrypted": true})
		pipelines.RunNotifyStep(ctx, sink)
		return nil
	}

	dst := filepath.Join(h.deps.LibraryRoot, rel)
	if err := pipelines.AtomicMove(isoPath, dst); err != nil {
		sink.OnStepFailed(state.StepMove, err)
		return err
	}
	sink.OnStepDone(state.StepMove, map[string]any{"path": dst})

	pipelines.RunNotifyStep(ctx, sink)
	return nil
}

func (h *Handler) createWorkDir(discID string) (string, error) {
	return pipelines.CreateWorkDir(h.deps.WorkRoot, "wiiu", discID)
}

// md5Identify hashes the ripped dump and looks it up in the Redump
// dat, filling disc title/year/region in place on a hit. This is the
// only identification path for Wii U -- see the package doc. Returns
// the computed MD5 regardless of whether it matched, so tryDecrypt can
// reuse it as the per-disc key filename without re-hashing a
// potentially 25-50GB dump a second time.
func (h *Handler) md5Identify(path string, disc *state.Disc) string {
	got, err := pipelines.MD5File(path)
	if err != nil {
		slog.Warn("wiiu: md5 hash failed", "err", err)
		return ""
	}
	if h.deps.RedumpDB == nil {
		slog.Warn("wiiu: no RedumpDB; skipping post-rip identify")
		return got
	}
	entry := h.deps.RedumpDB.LookupByMD5(got)
	if entry == nil {
		slog.Warn("wiiu: no Redump match", "md5", got)
		return got
	}
	slog.Info("wiiu: md5 identify ok", "title", entry.Title, "md5", got)
	disc.Title = entry.Title
	disc.Year = entry.Year
	disc.MetadataProvider = "Redump"
	disc.MetadataID = entry.BootCode
	disc.Candidates = []state.Candidate{{
		Source:     "Redump",
		Title:      entry.Title,
		Year:       entry.Year,
		Region:     entry.Region,
		Confidence: 100,
	}}
	return got
}

// tryDecrypt attempts a best-effort Wii U decrypt when the user has
// supplied both key files -- never required, never embedded/fetched by
// DiscEcho itself (see the package doc). commonKeyPath is
// platform-wide (<KeysDir>/common.key); discKeyPath is looked up by
// the raw dump's own MD5 (<KeysDir>/<md5>.key) -- the same identifier
// Redump itself catalogues Wii U discs by, so a user maintaining a
// personal key collection alongside their Redump dat can name files
// the same way. Any miss (no WuDecrypt configured, no KeysDir, no
// common key, no matching per-disc key, or the decrypt binary itself
// failing) returns ok=false so the caller falls back to shipping the
// raw dump -- decryption is a bonus, never a requirement.
func (h *Handler) tryDecrypt(ctx context.Context, isoPath, md5sum string, sink pipelines.EventSink) (outDir string, ok bool) {
	if h.deps.WuDecrypt == nil || h.deps.KeysDir == "" || md5sum == "" {
		return "", false
	}
	commonKeyPath := filepath.Join(h.deps.KeysDir, "common.key")
	discKeyPath := filepath.Join(h.deps.KeysDir, md5sum+".key")
	if _, err := os.Stat(commonKeyPath); err != nil {
		return "", false
	}
	if _, err := os.Stat(discKeyPath); err != nil {
		return "", false
	}

	outDir = filepath.Join(filepath.Dir(isoPath), "decrypted")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		slog.Warn("wiiu: decrypt skipped, mkdir failed", "err", err)
		return "", false
	}
	if err := h.deps.WuDecrypt.Decrypt(ctx, isoPath, outDir, commonKeyPath, discKeyPath, pipelines.NewStepSink(sink, state.StepMove)); err != nil {
		slog.Warn("wiiu: decrypt failed, shipping raw dump instead", "err", err)
		_ = os.RemoveAll(outDir)
		return "", false
	}
	entries, err := os.ReadDir(outDir)
	if err != nil || len(entries) == 0 {
		slog.Warn("wiiu: decrypt produced no output, shipping raw dump instead")
		_ = os.RemoveAll(outDir)
		return "", false
	}
	return outDir, true
}

// moveDir moves a directory tree, preferring a fast rename (same
// filesystem -- the common case, since this daemon's spool and library
// roots are typically the same mounted volume) and falling back to a
// recursive copy + RemoveAll for a cross-filesystem move.
// pipelines.AtomicMove is file-only, hence this separate helper
// (mirrors pipelines/ps3's private moveDir).
func moveDir(src, dst string) error {
	if _, err := os.Stat(dst); err == nil {
		return fmt.Errorf("moveDir: destination exists: %s", dst)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("moveDir: stat dst: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("moveDir: mkdir parent: %w", err)
	}
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	if err := copyDirRecursive(src, dst); err != nil {
		return err
	}
	return os.RemoveAll(src)
}

func copyDirRecursive(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer func() { _ = in.Close() }()
		out, err := os.Create(target)
		if err != nil {
			return err
		}
		defer func() { _ = out.Close() }()
		_, err = out.ReadFrom(in)
		return err
	})
}
