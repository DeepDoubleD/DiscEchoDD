// Package ps3 implements pipelines.Handler for PlayStation 3 game discs.
//
// Pipeline shape (6 active steps; transcode AND compress skipped):
//
//	detect → identify → rip (mount, ps3dumper-cli dump, unmount) → move → notify → eject
//
// PS3 is architecturally different from every other console this daemon
// handles: a PS3 disc is stock-mountable Blu-ray media -- the disc's
// real protection is per-file content encryption, not a drive-level
// read lockout the way Wii/GameCube's non-standard format is. That
// means PARAM.SFO (a small plaintext metadata file every retail disc
// carries) is readable with zero decryption at all, giving this
// pipeline something none of Xbox 360/Wii/the CD-only consoles ever
// had: a real pre-rip ProductCode + Title, no post-rip MD5 lookup
// needed. See daemon/internal/thirdparty/ps3-disc-dumper's vendored
// Dumper.DetectDisc for exactly what that reads.
//
// The actual dump is delegated entirely to ps3dumper-cli (DiscEcho's
// own wrapper around 13xforever/ps3-disc-dumper's Dumper -- see
// daemon/tools/ps3dumper.go), which needs the disc mounted as a normal
// filesystem (not a raw device read the way every other pipeline in
// this daemon works) -- this package owns that mount/unmount lifecycle
// since nothing else needs it. The dump itself is a decrypted PS3_GAME
// folder tree, not a single .iso -- unlike every other console
// pipeline, RunTranscode moves a DIRECTORY, not a file.
package ps3

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/jumpingmushroom/DiscEcho/daemon/pipelines"
	"github.com/jumpingmushroom/DiscEcho/daemon/state"
	"github.com/jumpingmushroom/DiscEcho/daemon/tools"
)

// PS3DumperTool is the subset of *tools.PS3Dumper used at rip-time and
// identify-time.
type PS3DumperTool interface {
	Detect(ctx context.Context, mountpoint string, sink tools.Sink) (*tools.PS3DetectResult, error)
	Dump(ctx context.Context, mountpoint, outputDir, keyCacheDir string, sink tools.Sink) (*tools.PS3DumpResult, error)
}

// Deps bundles the handler's dependencies.
type Deps struct {
	Dumper PS3DumperTool
	// KeyCacheDir persists looked-up disc keys across runs (mirrors
	// RedumpDataDir's role for the other consoles' Redump dats).
	KeyCacheDir    string
	Tools          *tools.Registry // looked up: apprise, eject
	LibraryRoot    string
	WorkRoot       string
	LibraryProbe   func(string) error
	URLsForTrigger func(ctx context.Context, trigger string) []string
	// ShouldEject gates the rip-end eject step; nil = always eject.
	ShouldEject func(ctx context.Context) bool
}

// Handler implements pipelines.Handler for PS3.
type Handler struct{ deps Deps }

// New returns a Handler with the given dependencies.
func New(d Deps) *Handler {
	if d.LibraryProbe == nil {
		d.LibraryProbe = pipelines.ProbeWritable
	}
	return &Handler{deps: d}
}

func (h *Handler) DiscType() state.DiscType { return state.DiscTypePS3 }

// Identify mounts the disc read-only, runs ps3dumper-cli's detect-only
// mode (PARAM.SFO read, no decryption), and unmounts. ProductCode/Title
// are real per-disc metadata (not a guess), but not necessarily
// Redump-clean naming -- unlike Xbox 360/Wii, there is no post-rip MD5
// fallback here: a ps3-disc-dumper output is a decrypted folder tree,
// not a single file a Redump ISO dat's hash would ever match anyway.
func (h *Handler) Identify(ctx context.Context, drv *state.Drive) (*state.Disc, []state.Candidate, error) {
	disc := &state.Disc{Type: state.DiscTypePS3, DriveID: drv.ID}
	if h.deps.Dumper == nil {
		return disc, nil, pipelines.ErrNoCandidates
	}

	mountpoint, cleanup, err := mountReadOnly(ctx, drv.DevPath, h.deps.WorkRoot, "identify")
	if err != nil {
		return disc, nil, pipelines.ErrNoCandidates
	}
	defer cleanup()

	result, err := h.deps.Dumper.Detect(ctx, mountpoint, nil)
	if err != nil || result == nil {
		return disc, nil, pipelines.ErrNoCandidates
	}

	disc.Title = result.Title
	disc.MetadataProvider = "PARAM.SFO"
	disc.MetadataID = result.ProductCode
	disc.Candidates = []state.Candidate{{
		Source:     "PARAM.SFO",
		Title:      result.Title,
		Confidence: 85,
	}}
	return disc, disc.Candidates, nil
}

// Plan returns the 6-active-step plan; both transcode and compress are
// skipped.
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

// PlanTranscode — transcode-half: PS3 has no transcode AND no compress
// (the decrypted folder tree is the deliverable). Only move + notify
// are active.
func (h *Handler) PlanTranscode(_ *state.Disc, _ *state.Profile) []pipelines.StepPlan {
	out := make([]pipelines.StepPlan, 0, 4)
	for _, sid := range state.CanonicalTranscodeSteps() {
		skip := sid == state.StepTranscode || sid == state.StepCompress
		out = append(out, pipelines.StepPlan{ID: sid, Skip: skip})
	}
	return out
}

const spoolDirName = "rip"

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

// RunRip executes the drive-bound half: mount the disc read-only,
// ps3dumper-cli dump (detect + find key + decrypt-copy) →
// spoolDir/rip/, unmount, eject.
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
	if h.deps.Dumper == nil {
		err := errors.New("ps3: dumper not configured")
		sink.OnStepFailed(state.StepRip, err)
		return pipelines.RipResult{}, err
	}
	ripDir := filepath.Join(spoolDir, spoolDirName)
	if err := os.MkdirAll(ripDir, 0o755); err != nil {
		sink.OnStepFailed(state.StepRip, err)
		return pipelines.RipResult{}, fmt.Errorf("mkdir rip: %w", err)
	}
	keyCacheDir := h.deps.KeyCacheDir
	if keyCacheDir == "" {
		keyCacheDir = filepath.Join(spoolDir, "keycache")
	}
	if err := os.MkdirAll(keyCacheDir, 0o755); err != nil {
		sink.OnStepFailed(state.StepRip, err)
		return pipelines.RipResult{}, fmt.Errorf("mkdir key cache: %w", err)
	}

	mountpoint, cleanupMount, err := mountReadOnly(ctx, drv.DevPath, h.deps.WorkRoot, disc.ID)
	if err != nil {
		sink.OnStepFailed(state.StepRip, err)
		return pipelines.RipResult{}, fmt.Errorf("mount disc: %w", err)
	}
	defer cleanupMount()

	result, err := h.deps.Dumper.Dump(ctx, mountpoint, ripDir, keyCacheDir, pipelines.NewStepSink(sink, state.StepRip))
	if err != nil {
		sink.OnStepFailed(state.StepRip, err)
		return pipelines.RipResult{}, fmt.Errorf("ps3dumper-cli: %w", err)
	}
	// A pre-rip Identify() (manual override, or a re-identify) may not
	// have run -- fill in from the dump result if the disc is still
	// unidentified, matching every other pipeline's "don't leave a
	// blank title if we actually know it" behaviour.
	if disc.Title == "" && result.Title != "" {
		disc.Title = result.Title
		disc.MetadataProvider = "PARAM.SFO"
		disc.MetadataID = result.ProductCode
	}
	sink.OnStepDone(state.StepRip, map[string]any{"dir": filepath.Join(ripDir, result.OutputSubdir)})

	pipelines.RunEjectStep(ctx, sink, pipelines.EjectDeps{
		Tools:       h.deps.Tools,
		ShouldEject: h.deps.ShouldEject,
	}, drv)

	return pipelines.RipResult{SpoolPath: spoolDir}, nil
}

// RunTranscode executes the compute-bound half: move the decrypted
// folder tree to the library, notify. No compress, no MD5 identify --
// unlike Xbox 360/Wii's single-file ISOs, there's no Redump ISO dat a
// folder tree's hash would ever match, and identification already
// happened for free via PARAM.SFO (see Identify/RunRip above).
func (h *Handler) RunTranscode(ctx context.Context, result pipelines.RipResult, disc *state.Disc, prof *state.Profile, sink pipelines.EventSink) error {
	ripDir := filepath.Join(result.SpoolPath, spoolDirName)
	srcDir, err := soleSubdir(ripDir)
	if err != nil {
		sink.OnStepStart(state.StepMove)
		sink.OnStepFailed(state.StepMove, err)
		return err
	}

	sink.OnStepStart(state.StepMove)
	rel, err := pipelines.RenderOutputPath(prof.OutputPathTemplate, pipelines.OutputFields{
		Title: disc.Title,
	})
	if err != nil {
		sink.OnStepFailed(state.StepMove, err)
		return err
	}
	dst := filepath.Join(h.deps.LibraryRoot, rel)
	if err := moveDir(srcDir, dst); err != nil {
		sink.OnStepFailed(state.StepMove, err)
		return err
	}
	sink.OnStepDone(state.StepMove, map[string]any{"path": dst})

	pipelines.RunNotifyStep(ctx, sink)
	return nil
}

func (h *Handler) createWorkDir(discID string) (string, error) {
	return pipelines.CreateWorkDir(h.deps.WorkRoot, "ps3", discID)
}

// soleSubdir returns the one directory ps3dumper-cli's `dump` wrote
// under ripDir (named after the dumper's own OutputDir formatter --
// see Cli/Program.cs's RunDump, which passes ProductCode as the
// naming template). Errors if ripDir doesn't contain exactly one
// directory, since that means the dump didn't actually produce
// anything sane to move.
func soleSubdir(ripDir string) (string, error) {
	entries, err := os.ReadDir(ripDir)
	if err != nil {
		return "", fmt.Errorf("read rip dir: %w", err)
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, e.Name())
		}
	}
	if len(dirs) != 1 {
		return "", fmt.Errorf("expected exactly one output dir under %s, found %d", ripDir, len(dirs))
	}
	return filepath.Join(ripDir, dirs[0]), nil
}

// moveDir moves a directory tree, preferring a fast rename (same
// filesystem -- the common case, since this daemon's spool and
// library roots are typically the same mounted volume) and falling
// back to a recursive copy + RemoveAll for a cross-filesystem move.
// pipelines.AtomicMove is file-only (its copy fallback isn't
// recursive), hence this separate helper rather than reusing it.
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

// mountReadOnly mounts devPath at a fresh directory under
// <workRoot>/ps3-mounts/<label> and returns the mountpoint plus a
// cleanup func that unmounts and removes it. Shells out to mount/
// umount (util-linux, already in the runtime image) rather than raw
// syscalls, matching this daemon's convention of driving external
// tools rather than reimplementing them.
func mountReadOnly(ctx context.Context, devPath, workRoot, label string) (mountpoint string, cleanup func(), err error) {
	mountpoint = filepath.Join(workRoot, "ps3-mounts", label)
	if err := os.MkdirAll(mountpoint, 0o755); err != nil {
		return "", nil, fmt.Errorf("mkdir mountpoint: %w", err)
	}
	cmd := exec.CommandContext(ctx, "mount", "-o", "ro", devPath, mountpoint) //nolint:gosec // devPath/mountpoint are daemon-configured, not user input.
	if out, err := cmd.CombinedOutput(); err != nil {
		_ = os.RemoveAll(mountpoint)
		return "", nil, fmt.Errorf("mount %s: %w: %s", devPath, err, string(out))
	}
	cleanup = func() {
		_ = exec.Command("umount", mountpoint).Run() //nolint:gosec // mountpoint is daemon-configured, not user input.
		_ = os.RemoveAll(mountpoint)
	}
	return mountpoint, cleanup, nil
}
