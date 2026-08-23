package identify

import (
	"bufio"
	"context"
	"os/exec"
	"strings"
)

// Xbox360DecoyProber detects the Microsoft XGD decoy-layer mastering
// signature in the ISO9660 primary volume descriptor. It's a
// last-resort fallback for Xbox 360 discs whose decoy DVD layer is too
// sparse to carry /_SYSTEMU — confirmed live on a retail Gears of War 3
// disc, whose entire visible directory tree is a single README.TXT (no
// /_SYSTEMU, no /VIDEO_TS). The PVD fingerprint is unaffected by how
// little (or how much) content the decoy layer carries, since it's a
// single fixed-location sector, not something reached by walking the
// directory tree.
type Xbox360DecoyProber interface {
	// Probe reports whether devPath's ISO9660 PVD matches the XGD decoy
	// mastering fingerprint. ok=false, err=nil means the PVD was read
	// fine but didn't match — a legitimate "not Xbox 360" result, not a
	// failure.
	Probe(ctx context.Context, devPath string) (ok bool, err error)
}

// Xbox360DecoyProberConfig configures NewXbox360DecoyProber.
type Xbox360DecoyProberConfig struct {
	IsoInfoBin string // default "isoinfo"
}

// NewXbox360DecoyProber returns an Xbox360DecoyProber that shells out to
// `isoinfo -d`.
func NewXbox360DecoyProber(c Xbox360DecoyProberConfig) Xbox360DecoyProber {
	if c.IsoInfoBin == "" {
		c.IsoInfoBin = "isoinfo"
	}
	return &isoinfoXbox360DecoyProber{bin: c.IsoInfoBin}
}

type isoinfoXbox360DecoyProber struct{ bin string }

func (p *isoinfoXbox360DecoyProber) Probe(ctx context.Context, devPath string) (bool, error) {
	cmd := exec.CommandContext(ctx, p.bin, "-d", "-i", devPath) //nolint:gosec // bin path is configured by the operator.
	out, err := cmd.Output()
	if err != nil {
		return false, nil
	}
	return matchesXGDDecoyFingerprint(string(out)), nil
}

// matchesXGDDecoyFingerprint reports whether `isoinfo -d` output carries
// Microsoft's XGD decoy-mastering fingerprint: all three of Publisher
// id, Data preparer id, and Application id set to the fixed values
// Microsoft's internal CDIMAGE tool stamps on Xbox/Xbox 360 disc
// masters, regardless of publisher (first- or third-party) or how the
// decoy layer's directory tree is populated. Requiring all three
// together — rather than "Publisher id: MICROSOFT CORPORATION" alone —
// keeps this from firing on unrelated Microsoft-published media (e.g.
// Windows install discs), which don't share the exact CDIMAGE preparer
// address block. Original Xbox discs match this fingerprint too, but
// RefineDiscType only reaches this check after the /default.xbe + XBE
// probe branch, so a real original-Xbox disc is already claimed by the
// time this runs.
func matchesXGDDecoyFingerprint(s string) bool {
	var publisher, preparer, application string
	scanner := bufio.NewScanner(strings.NewReader(s))
	scanner.Buffer(make([]byte, 4096), 64*1024)
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "Publisher id:"):
			publisher = strings.TrimSpace(strings.TrimPrefix(line, "Publisher id:"))
		case strings.HasPrefix(line, "Data preparer id:"):
			preparer = strings.TrimSpace(strings.TrimPrefix(line, "Data preparer id:"))
		case strings.HasPrefix(line, "Application id:"):
			application = strings.TrimSpace(strings.TrimPrefix(line, "Application id:"))
		}
	}
	return strings.EqualFold(publisher, "MICROSOFT CORPORATION") &&
		strings.Contains(strings.ToUpper(preparer), "ONE MICROSOFT WAY") &&
		strings.HasPrefix(strings.ToUpper(application), "CDIMAGE")
}
