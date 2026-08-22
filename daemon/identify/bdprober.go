package identify

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// BDInfo is the disc-level metadata read from `bd_info`. Only the
// AACS2 marker is consulted by the classifier; other fields are kept
// for future use and unit-test assertions.
type BDInfo struct {
	VolumeID      string
	Profile       string
	HasAACS2      bool
	AACSEncrypted bool
	// DiscName is bd_info's "Disc name" field, read from the disc's own
	// BDMV/META/DL/bdmt_*.xml. Retail Blu-rays populate this with the
	// real title for on-screen menus; unlike VolumeID (the raw
	// ISO9660/UDF volume label), it's rarely a generic authoring-tool
	// placeholder, so identify should prefer it as the TMDB search
	// query when present. Empty when the disc carries no library
	// metadata (bd_info omits the whole "Disc library metadata"
	// section in that case).
	DiscName string
}

// BDProber reads disc-level metadata from a Blu-ray.
type BDProber interface {
	Probe(ctx context.Context, devPath string) (*BDInfo, error)
}

// BDProberConfig configures NewBDProber.
type BDProberConfig struct {
	BDInfoBin string // default "bd_info"
}

// NewBDProber returns a BDProber that shells out to bd_info.
func NewBDProber(c BDProberConfig) BDProber {
	if c.BDInfoBin == "" {
		c.BDInfoBin = "bd_info"
	}
	return &bdInfoProber{bin: c.BDInfoBin}
}

type bdInfoProber struct{ bin string }

func (p *bdInfoProber) Probe(ctx context.Context, devPath string) (*BDInfo, error) {
	cmd := exec.CommandContext(ctx, p.bin, devPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("bd_info: %w (output: %s)", err, strings.TrimSpace(string(out)))
	}
	return ParseBDInfoOutput(string(out))
}

// ParseBDInfoOutput extracts BDInfo fields from `bd_info` stdout.
//
// Recognised lines (case-insensitive on the key, ":" separator):
//
//	Volume Identifier   : MOVIE_NAME
//	Profile             : Profile 5
//	AACS encrypted      : yes
//	AACS2 disc          : yes        ← UHD marker
//	Disc name           : V For Vendetta  ← from bdmt_*.xml, in the
//	                                        "Disc library metadata"
//	                                        section bd_info prints when
//	                                        present
//
// Returns an error only on empty input. Lines that don't match are
// ignored, so older bd_info versions still parse without spurious
// failures.
func ParseBDInfoOutput(s string) (*BDInfo, error) {
	if strings.TrimSpace(s) == "" {
		return nil, fmt.Errorf("ParseBDInfoOutput: empty input")
	}
	scanner := bufio.NewScanner(strings.NewReader(s))
	scanner.Buffer(make([]byte, 4096), 64*1024)

	info := &BDInfo{}
	for scanner.Scan() {
		line := scanner.Text()
		key, val, ok := splitBDInfoLine(line)
		if !ok {
			continue
		}
		switch strings.ToLower(key) {
		case "volume identifier":
			info.VolumeID = val
		case "profile":
			info.Profile = val
		case "aacs encrypted":
			info.AACSEncrypted = parseYesNo(val)
		case "aacs2 disc":
			info.HasAACS2 = parseYesNo(val)
		case "disc name":
			info.DiscName = val
		}
	}
	return info, nil
}

// BDSearchLabel picks the best available TMDB search query for a BD/UHD
// disc: bd_info's disc-library "Disc name" (from bdmt_*.xml) when a
// BDProber is configured and reports one, otherwise volLabel (the raw
// ISO9660/UDF volume label read by the DVDProber both pipelines already
// call). Disc name wins because retail Blu-rays populate it with the
// real title for on-screen menus, while the volume label is frequently
// a generic authoring-tool placeholder (e.g. "VOLUME_ID") that produces
// a confident-looking but wrong TMDB match. bd is nil-safe so callers
// that don't wire a BDProber keep the old volume-label-only behaviour.
func BDSearchLabel(ctx context.Context, bd BDProber, devPath, volLabel string) string {
	if bd != nil {
		if info, err := bd.Probe(ctx, devPath); err == nil && info != nil {
			if name := strings.TrimSpace(info.DiscName); name != "" {
				return name
			}
		}
	}
	return volLabel
}

func splitBDInfoLine(line string) (string, string, bool) {
	idx := strings.Index(line, ":")
	if idx < 0 {
		return "", "", false
	}
	key := strings.TrimSpace(line[:idx])
	val := strings.TrimSpace(line[idx+1:])
	if key == "" {
		return "", "", false
	}
	return key, val, true
}

func parseYesNo(v string) bool {
	return strings.EqualFold(strings.TrimSpace(v), "yes")
}
