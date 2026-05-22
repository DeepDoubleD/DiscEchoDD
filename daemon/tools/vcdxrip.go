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

	"github.com/jumpingmushroom/DiscEcho/daemon/state"
)

// VCDXRip wraps the vcdxrip binary (from the vcdimager suite). Used by
// the VCD pipeline to extract the MPEG-1/MPEG-2 tracks from a Video CD
// or Super Video CD. VCD video lives in CD-ROM XA Mode-2-Form-2
// sectors, so the on-disc .DAT files can't be copied as plain files —
// vcdxrip is the canonical extractor.
//
// Like Redumper, CHDMan, and MakeMKV, this is a typed-deps wrapper, not
// a tools.Registry entry: the VCD pipeline holds it directly.
type VCDXRip struct {
	Bin string // "" → "vcdxrip"
}

func (v *VCDXRip) bin() string {
	if v.Bin == "" {
		return "vcdxrip"
	}
	return v.Bin
}

// Name returns the tool name. Used for logging only.
func (v *VCDXRip) Name() string { return "vcdxrip" }

// Rip extracts every MPEG track from the (S)VCD in devPath into outDir.
// vcdxrip writes the tracks as avseqNN.mpg in its working directory and
// a videocd.xml descriptor; running with Dir=outDir lands the .mpg
// files there. Progress lines on stdout are streamed to sink.
func (v *VCDXRip) Rip(ctx context.Context, devPath, outDir string, sink Sink) error {
	args := []string{
		"--cdrom-device", devPath,
		"--progress",
		"--output-file", "videocd.xml",
	}
	cmd := exec.CommandContext(ctx, v.bin(), args...)
	cmd.Dir = outDir

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("vcdxrip start: %w", err)
	}

	// vcdxrip prints its #extract progress to stdout and INFO/WARN
	// diagnostics to stderr; parse both so the dashboard fills regardless
	// of which stream a given build uses.
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); ParseVCDXRipProgress(stdout, sink) }()
	go func() { defer wg.Done(); ParseVCDXRipProgress(stderr, sink) }()
	wg.Wait()

	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("vcdxrip: %w", err)
	}
	return nil
}

// vcdxripProgressRE matches vcdxrip --progress lines such as
// "#extract[avseq05.mpg]: 11523/217803 ( 5%)". Group 1 is the current
// output file; group 2 is the integer percentage.
var vcdxripProgressRE = regexp.MustCompile(`#extract\[([^\]]+)\]:\s*\d+/\d+\s*\(\s*(\d+)%\)`)

// ParseVCDXRipProgress reads vcdxrip output and emits sink.Progress on
// "#extract[...]" lines. The current output file is reported as a
// sub-step the first time it appears (deduped across the per-file
// progress refresh). All other non-empty lines are forwarded to
// sink.Log. The scanner treats '\r' and '\n' alike because vcdxrip
// overwrites its progress line with carriage returns.
//
// Per-file percentage resets to 0 at each track boundary; for the
// common single-feature VCD this is a single 0→100 sweep, and the
// sub-step label makes multi-track discs legible without aggregation.
func ParseVCDXRipProgress(r io.Reader, sink Sink) {
	drainAfterScan(r, func(scanner *bufio.Scanner) {
		scanner.Split(splitCROrLF)
		lastFile := ""
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			if m := vcdxripProgressRE.FindStringSubmatch(line); m != nil {
				if m[1] != lastFile {
					sink.SubStep(m[1])
					lastFile = m[1]
				}
				if pct, err := strconv.ParseFloat(m[2], 64); err == nil {
					sink.Progress(pct, "", 0)
				}
				continue
			}
			if len(line) > 400 {
				line = line[:400]
			}
			sink.Log(state.LogLevelInfo, "vcdxrip: %s", line)
		}
	})
}
