package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/jumpingmushroom/DiscEcho/daemon/state"
)

// PS3Dumper drives ps3dumper-cli (DiscEcho's own wrapper around the
// vendored 13xforever/ps3-disc-dumper library, see
// daemon/internal/thirdparty/ps3-disc-dumper) -- built in the
// ps3dumper-build Docker stage. Unlike every other console this daemon
// handles, PS3 discs are stock-mountable Blu-ray media (per-file
// content encryption, not a drive-level read lockout the way
// Wii/GameCube's non-standard format is): the caller is responsible
// for mounting the device at mountpoint before calling Detect/Dump and
// unmounting afterward -- see pipelines/ps3, which owns that lifecycle
// since it's the one thing here that isn't just "shell out to a CLI".
type PS3Dumper struct {
	// Bin is the ps3dumper-cli binary name/path. Defaults to
	// "ps3dumper-cli" (on PATH, per the Dockerfile install).
	Bin string
}

func NewPS3Dumper(bin string) *PS3Dumper {
	if bin == "" {
		bin = "ps3dumper-cli"
	}
	return &PS3Dumper{Bin: bin}
}

func (p *PS3Dumper) Name() string { return "ps3dumper-cli" }

// PS3DetectResult mirrors ps3dumper-cli's `detect` PS3DUMPER_RESULT
// payload -- available with zero decryption, since ProductCode/Title
// come from PARAM.SFO, which is plaintext on every retail disc.
type PS3DetectResult struct {
	Success     bool   `json:"success"`
	ProductCode string `json:"product_code"`
	Title       string `json:"title"`
	TotalBytes  int64  `json:"total_bytes"`
	TotalFiles  int    `json:"total_files"`
	Error       string `json:"error"`
}

// PS3DumpResult mirrors ps3dumper-cli's `dump` PS3DUMPER_RESULT
// payload.
type PS3DumpResult struct {
	Success      bool   `json:"success"`
	ProductCode  string `json:"product_code"`
	Title        string `json:"title"`
	TotalBytes   int64  `json:"total_bytes"`
	TotalFiles   int    `json:"total_files"`
	BrokenFiles  int    `json:"broken_files"`
	OutputSubdir string `json:"output_subdir"`
	Error        string `json:"error"`
}

type ps3DumperProgress struct {
	ProcessedSectors int64 `json:"processed_sectors"`
	TotalSectors     int64 `json:"total_sectors"`
	CurrentFile      int   `json:"current_file"`
	TotalFiles       int   `json:"total_files"`
}

const (
	ps3DumperProgressPrefix = "PS3DUMPER_PROGRESS: "
	ps3DumperResultPrefix   = "PS3DUMPER_RESULT: "
)

// Detect runs `ps3dumper-cli detect <mountpoint>` -- pre-rip
// identification only, no key lookup, no dump. mountpoint must already
// be a mounted PS3 disc filesystem (see pipelines/ps3).
func (p *PS3Dumper) Detect(ctx context.Context, mountpoint string, sink Sink) (*PS3DetectResult, error) {
	cmd := exec.CommandContext(ctx, p.Bin, "detect", mountpoint) //nolint:gosec // bin/mountpoint are daemon-configured, not user input.
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	cmd.Stderr = cmd.Stdout // upstream Log.* also writes to stderr on some levels; keep it in the same stream for the job log.
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("ps3dumper-cli start: %w", err)
	}

	var result *PS3DetectResult
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if rest, ok := strings.CutPrefix(line, ps3DumperResultPrefix); ok {
			var r PS3DetectResult
			if jsonErr := json.Unmarshal([]byte(rest), &r); jsonErr == nil {
				result = &r
			}
			continue
		}
		if sink != nil && line != "" {
			sink.Log(state.LogLevelInfo, "ps3dumper-cli: %s", line)
		}
	}
	waitErr := cmd.Wait()
	if result == nil {
		if waitErr != nil {
			return nil, fmt.Errorf("ps3dumper-cli detect: %w", waitErr)
		}
		return nil, fmt.Errorf("ps3dumper-cli detect: no result line")
	}
	if !result.Success {
		return result, fmt.Errorf("ps3dumper-cli detect: %s", result.Error)
	}
	return result, nil
}

// Dump runs `ps3dumper-cli dump <mountpoint> <outputDir> <keyCacheDir>`,
// streaming progress into sink. mountpoint must already be mounted
// (see pipelines/ps3). keyCacheDir persists looked-up disc keys across
// runs (mirrors RedumpDataDir's role for the other consoles) so a
// re-rip of the same title doesn't need a fresh IRD/key-library lookup.
func (p *PS3Dumper) Dump(ctx context.Context, mountpoint, outputDir, keyCacheDir string, sink Sink) (*PS3DumpResult, error) {
	cmd := exec.CommandContext(ctx, p.Bin, "dump", mountpoint, outputDir, keyCacheDir) //nolint:gosec // args are daemon-configured, not user input.
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("ps3dumper-cli start: %w", err)
	}

	var result *PS3DumpResult
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, ps3DumperProgressPrefix):
			var pr ps3DumperProgress
			if jsonErr := json.Unmarshal([]byte(strings.TrimPrefix(line, ps3DumperProgressPrefix)), &pr); jsonErr == nil && sink != nil {
				pct := 0.0
				if pr.TotalSectors > 0 {
					pct = float64(pr.ProcessedSectors) / float64(pr.TotalSectors) * 100
				}
				sink.Progress(pct, "", 0)
				if pr.TotalFiles > 0 {
					sink.SubStep(fmt.Sprintf("file %d/%d", pr.CurrentFile, pr.TotalFiles))
				}
			}
		case strings.HasPrefix(line, ps3DumperResultPrefix):
			var r PS3DumpResult
			if jsonErr := json.Unmarshal([]byte(strings.TrimPrefix(line, ps3DumperResultPrefix)), &r); jsonErr == nil {
				result = &r
			}
		default:
			if sink != nil && line != "" {
				sink.Log(state.LogLevelInfo, "ps3dumper-cli: %s", line)
			}
		}
	}
	waitErr := cmd.Wait()
	if result == nil {
		if waitErr != nil {
			return nil, fmt.Errorf("ps3dumper-cli dump: %w", waitErr)
		}
		return nil, fmt.Errorf("ps3dumper-cli dump: no result line")
	}
	if !result.Success {
		if result.Error != "" {
			return result, fmt.Errorf("ps3dumper-cli dump: %s", result.Error)
		}
		return result, fmt.Errorf("ps3dumper-cli dump: %d broken file(s)", result.BrokenFiles)
	}
	return result, nil
}
