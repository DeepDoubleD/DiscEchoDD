package identify

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// DVDInfo is the disc-level metadata read from the ISO9660 primary
// volume descriptor. TitleCount is populated later by HandBrake's
// title scan in the rip step (the prober only reads the label).
type DVDInfo struct {
	VolumeLabel string
	TitleCount  int
}

// DVDProber reads disc-level metadata from a DVD.
type DVDProber interface {
	Probe(ctx context.Context, devPath string) (*DVDInfo, error)
}

// DVDProberConfig configures NewDVDProber.
type DVDProberConfig struct {
	IsoInfoBin string // default "isoinfo"
}

// NewDVDProber returns a DVDProber that shells out to isoinfo.
func NewDVDProber(c DVDProberConfig) DVDProber {
	if c.IsoInfoBin == "" {
		c.IsoInfoBin = "isoinfo"
	}
	return &isoinfoProber{bin: c.IsoInfoBin}
}

type isoinfoProber struct{ bin string }

func (p *isoinfoProber) Probe(ctx context.Context, devPath string) (*DVDInfo, error) {
	cmd := exec.CommandContext(ctx, p.bin, "-d", "-i", devPath)
	out, err := cmd.Output()
	if err != nil {
		// isoinfo only reads the ISO9660 bridge volume descriptor. Many
		// Blu-rays (and some DVDs) are UDF-only with no ISO9660 bridge,
		// so isoinfo exits nonzero even though the disc is perfectly
		// readable. Fall back to udev's blkid-derived ID_FS_LABEL, the
		// same property classify.go already trusts for BD media detection,
		// rather than failing the whole identify pipeline over a label.
		if label, ok := udevFSLabel(ctx, devPath); ok {
			return &DVDInfo{VolumeLabel: label}, nil
		}
		return nil, fmt.Errorf("isoinfo: %w", err)
	}
	return ParseIsoInfoOutput(string(out))
}

// udevFSLabel reads ID_FS_LABEL for devPath via udevadm. ok is false only
// when udevadm itself fails or reports no filesystem at all (device not
// probed) — a present-but-empty label is a valid, ok=true result.
func udevFSLabel(ctx context.Context, devPath string) (label string, ok bool) {
	cmd := exec.CommandContext(ctx, "udevadm", "info", "--query=property", "--name="+devPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(string(out), "\n") {
		if v, found := strings.CutPrefix(line, "ID_FS_LABEL="); found {
			return strings.TrimSpace(v), true
		}
	}
	return "", false
}

// ParseIsoInfoOutput extracts DVDInfo fields from `isoinfo -d` stdout.
// Returns an error only if no `Volume id:` line is present at all
// (indicates malformed or non-ISO9660 output); a blank label is OK.
func ParseIsoInfoOutput(s string) (*DVDInfo, error) {
	scanner := bufio.NewScanner(strings.NewReader(s))
	scanner.Buffer(make([]byte, 4096), 64*1024)

	var info DVDInfo
	sawVolumeID := false

	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "Volume id:") {
			sawVolumeID = true
			info.VolumeLabel = strings.TrimSpace(strings.TrimPrefix(line, "Volume id:"))
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan: %w", err)
	}
	if !sawVolumeID {
		return nil, fmt.Errorf("no Volume id: line in isoinfo output")
	}
	return &info, nil
}
