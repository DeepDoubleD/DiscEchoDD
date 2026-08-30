package tools

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jumpingmushroom/DiscEcho/daemon/state"
)

// Redumper wraps the redumper binary. Used by the PSX, PS2, and Xbox
// pipelines for the rip step. Output is .bin/.cue (CD media) or .iso
// (DVD/Xbox media); the caller passes the media mode at Rip time.
type Redumper struct {
	bin string
}

// RedumperOutputExt returns the primary output file extension for the
// given redumper mode: ".cue" for cd, ".iso" for dvd and xbox.
func RedumperOutputExt(mode string) string {
	if mode == "cd" {
		return ".cue"
	}
	return ".iso"
}

const (
	// redumperRetries is the per-sector read-retry budget. The default is
	// 0 — any SCSI / C2 read error makes redumper refuse to split, aborting
	// near 100%. 50 retries recovers most surface scratches without dragging
	// out a clean disc (where the first read succeeds and the budget is never
	// spent). Bounded against runaways by redumperWatch below.
	redumperRetries = 50

	// redumperStallTimeout aborts a rip that makes no forward progress (the
	// percent never increases) for this long — a hung redumper or a drive
	// that spun down would otherwise hold the optical drive indefinitely.
	// Generous so a slow-but-advancing REFINE pass on a scratched disc is
	// never killed.
	redumperStallTimeout = 20 * time.Minute

	// redumperMaxAudioBoundaryRetries aborts when redumper logs this many
	// "unexpected read type on retry" lines. On some drives the data/audio
	// track boundary of a mixed-mode disc reports the wrong sector read type
	// on every retry, so REFINE burns the full retry budget per sector and
	// crawls (observed: 7% in 10h, ETA ~132h, on an ASUS SDRW-08D2S-U). The
	// disc is not rippable on this drive; fail fast with a remediation tip
	// rather than holding the drive for days.
	redumperMaxAudioBoundaryRetries = 256

	// redumperWatchInterval is how often the watchdog re-evaluates the abort
	// conditions. Cheap; the checks are a mutex + a couple of comparisons.
	redumperWatchInterval = 30 * time.Second
)

// NewRedumper returns a Redumper. Empty bin defaults to "redumper".
func NewRedumper(bin string) *Redumper {
	if bin == "" {
		bin = "redumper"
	}
	return &Redumper{bin: bin}
}

// Name returns the tool name. Used for logging only — Redumper is not
// registered in tools.Registry (its Rip signature doesn't fit
// tools.Tool.Run).
func (r *Redumper) Name() string { return "redumper" }

// Rip dumps the disc to outDir using the given base name. mode is
// "cd", "dvd", "xbox", "xbox360", "wii", or "wiiu"; selects the right
// --disc-type override and invokes the `disc` aggregate subcommand
// (which runs dump+refine+split in one pass).
//
//	cd      → redumper disc --disc-type=CD  --drive <devPath> --image-path <outDir> --image-name <name>
//	          → produces <outDir>/<name>.bin + <outDir>/<name>.cue (after the split phase)
//	dvd     → redumper disc --disc-type=DVD --drive <devPath> --image-path <outDir> --image-name <name>
//	          → produces <outDir>/<name>.iso
//	xbox    → redumper disc --disc-type=DVD --drive <devPath> --image-path <outDir> --image-name <name>
//	          → produces <outDir>/<name>.iso  (XGD1 discs are DVD media;
//	            redumper's security-sector handling kicks in automatically
//	            when it detects the XGD structure)
//	xbox360 → same as xbox, plus --dvd-raw (OmniDrive raw DVD sector
//	          reads) — XGD2/XGD3's security sectors need the raw path
//	          an OmniDrive-flashed drive exposes; a plain DVD read
//	          (what "xbox" mode does) doesn't reach them.
//	wii     → same as xbox360 (DVD disc-type + --dvd-raw). Confirmed
//	          live: a stock read gets nothing off a Wii disc at all --
//	          not even a TOC (cd-info fails outright), unlike Xbox 360's
//	          readable decoy layer -- so the raw OmniDrive path is
//	          mandatory here, not an accuracy nice-to-have.
//	wiiu    → redumper disc --disc-type=BLURAY --bd-raw. Wii U game
//	          discs are BD-form-factor media, not DVD, so this is the
//	          Blu-ray analogue of "wii"/"xbox360": a stock BD read
//	          returns the raw sectors already AES-encrypted (per
//	          RibShark's OmniDrive documentation — "raw BD reading,
//	          2052-byte sectors, including encrypted Wii U reading"),
//	          so --bd-raw is required the same way --dvd-raw is for
//	          Wii. This redumper output is always the raw encrypted
//	          dump -- daemon/pipelines/wiiu's RunTranscode optionally
//	          decrypts it afterward via an external tool if the user
//	          has supplied keys; see that package's doc for why.
//
// Older redumper releases shipped per-media subcommands (`redumper cd`,
// `redumper dvd`, `redumper xbox`); current builds (b720+) use a single
// `disc` aggregate. `--image-path` is the OUTPUT DIRECTORY (redumper
// creates it if missing) and `--image-name` is the file prefix the
// daemon uses to find the output afterwards. Streams progress to sink
// via ParseRedumperProgress.
func (r *Redumper) Rip(ctx context.Context, devPath, outDir, name, mode string, sink Sink) error {
	discType, ok := redumperDiscType(mode)
	if !ok {
		return fmt.Errorf("redumper: unknown mode %q (want cd|dvd|xbox|xbox360|wii|wiiu)", mode)
	}
	args := []string{
		"disc",
		"--disc-type=" + discType,
		"--drive", devPath,
		"--image-path", outDir,
		"--image-name", name,
		fmt.Sprintf("--retries=%d", redumperRetries),
	}
	switch mode {
	case "xbox360", "wii":
		args = append(args, "--dvd-raw")
	case "wiiu":
		args = append(args, "--bd-raw")
	}

	// Derive a cancelable context so the watchdog can kill a runaway rip
	// (audio-boundary thrash / stall) independently of the caller's ctx.
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	watch := newRedumperWatch(redumperNow())
	ws := redumperWatchSink{Sink: sink, watch: watch}

	cmd := exec.CommandContext(runCtx, r.bin, args...)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("redumper start: %w", err)
	}

	watchdogDone := make(chan struct{})
	go func() {
		ticker := time.NewTicker(redumperWatchInterval)
		defer ticker.Stop()
		for {
			select {
			case <-watchdogDone:
				return
			case <-ticker.C:
				if reason := watch.abortReason(redumperNow()); reason != "" {
					watch.trip(reason)
					cancel() // kills redumper via runCtx
					return
				}
			}
		}
	}()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); ParseRedumperProgress(stdout, ws) }()
	go func() { defer wg.Done(); ParseRedumperProgress(stderr, ws) }()
	wg.Wait()
	close(watchdogDone)

	waitErr := cmd.Wait()
	// A watchdog trip kills the process, so cmd.Wait reports a generic
	// "signal: killed". Surface the actionable reason instead.
	if reason := watch.trippedReason(); reason != "" {
		return fmt.Errorf("redumper aborted: %s", reason)
	}
	return waitErr
}

// redumperWatch bounds a redumper rip that would otherwise hold the optical
// drive indefinitely. Two independent triggers, both evaluated off the output
// stream so there's no reliance on subprocess timing:
//
//   - audio-boundary thrash: redumper logs "unexpected read type on retry" for
//     every retry of a sector whose read type the drive misreports at a
//     mixed-mode disc's data/audio boundary. Past audioRetryLimit the disc is
//     not rippable on this drive.
//   - stall: the percent never advances for stallTimeout (hung redumper /
//     spun-down drive).
//
// Decision methods take an explicit `now` so tests drive them with a fixed
// clock rather than wall-clock sleeps.
type redumperWatch struct {
	audioRetryLimit int
	stallTimeout    time.Duration

	mu             sync.Mutex
	audioRetries   int
	lastPct        float64
	lastProgressAt time.Time
	tripped        string
}

var redumperUnexpectedReadTypeRE = regexp.MustCompile(`unexpected read type on retry`)

func newRedumperWatch(now time.Time) *redumperWatch {
	return &redumperWatch{
		audioRetryLimit: redumperMaxAudioBoundaryRetries,
		stallTimeout:    redumperStallTimeout,
		lastProgressAt:  now,
	}
}

// noteLog counts the audio-boundary thrash marker in a forwarded log line.
func (w *redumperWatch) noteLog(line string) {
	if redumperUnexpectedReadTypeRE.MatchString(line) {
		w.mu.Lock()
		w.audioRetries++
		w.mu.Unlock()
	}
}

// noteProgress resets the stall timer on forward (increasing-percent) progress.
func (w *redumperWatch) noteProgress(pct float64, now time.Time) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if pct > w.lastPct {
		w.lastPct = pct
		w.lastProgressAt = now
	}
}

// abortReason returns a non-empty remediation tip when the rip should be
// killed, else "".
func (w *redumperWatch) abortReason(now time.Time) string {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.audioRetries >= w.audioRetryLimit {
		return "disc unreadable on this drive: redumper cannot resolve the data/audio track boundary (mixed-mode disc). Try a different optical drive or clean the disc."
	}
	if now.Sub(w.lastProgressAt) >= w.stallTimeout {
		return fmt.Sprintf("rip stalled: no progress for %s. The drive may have spun down or the disc may be unreadable.", w.stallTimeout)
	}
	return ""
}

func (w *redumperWatch) trip(reason string) {
	w.mu.Lock()
	w.tripped = reason
	w.mu.Unlock()
}

func (w *redumperWatch) trippedReason() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.tripped
}

// redumperWatchSink tees the parser's events into a redumperWatch, forwarding
// everything to the real sink unchanged.
type redumperWatchSink struct {
	Sink
	watch *redumperWatch
}

func (s redumperWatchSink) Progress(pct float64, speed string, etaSeconds int) {
	s.watch.noteProgress(pct, redumperNow())
	s.Sink.Progress(pct, speed, etaSeconds)
}

func (s redumperWatchSink) Log(level state.LogLevel, format string, args ...any) {
	s.watch.noteLog(fmt.Sprintf(format, args...))
	s.Sink.Log(level, format, args...)
}

// redumperDiscType maps the daemon's pipeline-side mode string to
// redumper's --disc-type value. Xbox uses DVD media (XGD); redumper
// detects the XGD security structure on its own.
func redumperDiscType(mode string) (string, bool) {
	switch mode {
	case "cd":
		return "CD", true
	case "dvd":
		return "DVD", true
	case "xbox":
		return "DVD", true
	case "xbox360":
		return "DVD", true
	case "wii":
		return "DVD", true
	case "wiiu":
		return "BLURAY", true
	}
	return "", false
}

var (
	// Modern redumper (b720+) emits progress as:
	//   `/ [ 2%] LBA: 60928/2161648, errors: { SCSI: 0, EDC: 0 }`
	// The leading `/`/`-`/`\`/`|` is a spinner that cycles on
	// in-place `\r` updates. The percent in `[ NN%]` is pre-computed,
	// and the `LBA: cur/max` pair follows mid-line. We capture the
	// percent directly when present (cheap, accurate), and fall back
	// to dividing cur/max when only the LBA pair is present (some
	// phase headers print `LBA: 0/N` without the percent prefix).
	redumperPercentRE = regexp.MustCompile(`\[\s*(\d+)%\]`)
	redumperLBARE     = regexp.MustCompile(`LBA:\s*(\d+)/(\d+)`)
	redumperSpeedRE   = regexp.MustCompile(`Speed:\s*([0-9.]+)x`)

	// redumperPhaseRE matches redumper's phase-marker lines:
	//   *** DUMP (time check: 0s)
	//   *** DUMP::EXTRA (time check: 2613s)
	//   *** REFINE (time check: 10s)
	// Capturing group 1 is the phase name (uppercase letters and colons).
	redumperPhaseRE = regexp.MustCompile(`^\*\*\*\s+([A-Z:]+)\s+\(time check:`)
)

// ParseRedumperProgress reads a redumper output stream and emits sink
// events.
//
// Recognised lines:
//
//	"/ [ NN%] LBA: <cur>/<max>"  → sink.Progress(pct, speed, etaSeconds)
//	"LBA: <cur>/<max>"           → same, computing percent from cur/max
//	"Speed: <N.N>x"              → legacy redumper format; carries forward
//
// Speed and ETA are derived because b720+ doesn't print either on the
// progress line. Speed = (deltaSectors × 2048) / deltaWallTime, formatted
// as "X.X MB/s". ETA = elapsedWallTime × (100-pct) / (pct-firstPct),
// where firstPct is the percent when we first saw a progress line — this
// extrapolates from real elapsed time rather than a static read-speed
// assumption.
//
// All other non-empty lines are forwarded to sink.Log so they appear
// in the job's log tail. The scanner treats both '\r' and '\n' as line
// terminators because redumper b720+ overwrites its progress line with
// carriage returns; the default ScanLines would buffer the entire rip
// phase as a single token.
func ParseRedumperProgress(r io.Reader, sink Sink) {
	drainAfterScan(r, func(scanner *bufio.Scanner) {
		scanner.Split(splitCROrLF)
		state := newRedumperRate()
		var legacySpeed string
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			// Phase-marker lines (*** DUMP, *** REFINE, *** SPLIT, …)
			// signal a long-running sub-phase. Emit SubStep first so the UI
			// updates immediately, then forward the line to the log tail.
			if m := redumperPhaseRE.FindStringSubmatch(line); m != nil {
				sink.SubStep(m[1])
				if len(line) > 400 {
					line = line[:400]
				}
				sink.Log(stateLogLevelInfoConst, "redumper: %s", line)
				continue
			}

			if m := redumperSpeedRE.FindStringSubmatch(line); m != nil {
				legacySpeed = m[1] + "x"
				// don't continue — single line may carry Speed AND progress.
			}

			lbaMatch := redumperLBARE.FindStringSubmatch(line)
			var cur int
			if lbaMatch != nil {
				cur, _ = strconv.Atoi(lbaMatch[1])
			}

			emit := func(pct float64) {
				now := redumperNow()
				speed := legacySpeed
				if lbaMatch != nil {
					if s := state.observeLBA(cur, now); s != "" {
						speed = s
					}
				}
				eta := state.observePercent(pct, now)
				sink.Progress(pct, speed, eta)
			}

			// Prefer the pre-computed `[ NN%]` percent.
			if m := redumperPercentRE.FindStringSubmatch(line); m != nil {
				pct, _ := strconv.Atoi(m[1])
				emit(float64(pct))
				continue
			}
			if lbaMatch != nil {
				max, _ := strconv.Atoi(lbaMatch[2])
				if max <= 0 {
					continue
				}
				emit(float64(cur) / float64(max) * 100)
				continue
			}
			if len(line) > 400 {
				line = line[:400]
			}
			sink.Log(stateLogLevelInfoConst, "redumper: %s", line)
		}
	})
}

// redumperNow is a package var so tests can substitute a deterministic clock.
var redumperNow = func() time.Time { return time.Now() }

// stateLogLevelInfoConst aliases the state.LogLevelInfo constant. Pulled
// out into a var so the import stays in one place and tests can reference
// the same constant without a circular import.
var stateLogLevelInfoConst = state.LogLevelInfo

// redumperRateTracker derives speed (MB/s) and ETA seconds from a stream
// of LBA + percent samples. Zero-value is unusable; call newRedumperRate.
type redumperRateTracker struct {
	firstSeen      time.Time
	firstPct       float64
	lastSampleTime time.Time
	lastLBA        int
}

func newRedumperRate() *redumperRateTracker {
	return &redumperRateTracker{}
}

// observeLBA computes an instantaneous MB/s from the LBA delta since the
// previous sample. Returns the empty string when there's no usable delta
// (first sample, or no time has elapsed). 2048 bytes per DVD/CD sector.
func (r *redumperRateTracker) observeLBA(cur int, now time.Time) string {
	defer func() { r.lastLBA = cur; r.lastSampleTime = now }()
	if r.lastSampleTime.IsZero() {
		return ""
	}
	dt := now.Sub(r.lastSampleTime).Seconds()
	if dt <= 0 {
		return ""
	}
	dSec := cur - r.lastLBA
	if dSec <= 0 {
		return ""
	}
	bytesPerSec := float64(dSec) * 2048 / dt
	return fmt.Sprintf("%.1f MB/s", bytesPerSec/(1024*1024))
}

// observePercent extrapolates ETA seconds from wall-time elapsed since
// the first observation. Returns 0 until we have a measurable percent
// delta (avoids divide-by-zero and noisy first-sample ETAs).
func (r *redumperRateTracker) observePercent(pct float64, now time.Time) int {
	if r.firstSeen.IsZero() {
		r.firstSeen = now
		r.firstPct = pct
		return 0
	}
	pctDone := pct - r.firstPct
	if pctDone <= 0 {
		return 0
	}
	elapsed := now.Sub(r.firstSeen).Seconds()
	remaining := 100 - pct
	if remaining <= 0 {
		return 0
	}
	return int(elapsed * remaining / pctDone)
}
