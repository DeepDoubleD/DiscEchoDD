package identify_test

import (
	"context"
	"os"
	"testing"

	"github.com/jumpingmushroom/DiscEcho/daemon/identify"
	"github.com/jumpingmushroom/DiscEcho/daemon/state"
)

// TestXbox360DecoyProber_MatchesRealCapture uses the exact isoinfo -d
// output captured live from a retail Gears of War 3 disc: its decoy
// DVD layer is just a single README.TXT (no /_SYSTEMU), so the normal
// classify.go marker never fires and the disc was silently misfiled as
// a generic DATA disc. The PVD fingerprint below is what let it be
// recognised as Xbox 360 anyway.
func TestXbox360DecoyProber_MatchesRealCapture(t *testing.T) {
	out, err := os.ReadFile("testdata/isoinfo-xgd-gow3.txt")
	if err != nil {
		t.Fatal(err)
	}
	fakeBin(t, "isoinfo", "cat <<'EOF'\n"+string(out)+"EOF\n")

	p := identify.NewXbox360DecoyProber(identify.Xbox360DecoyProberConfig{IsoInfoBin: "isoinfo"})
	ok, err := p.Probe(context.Background(), "/dev/sr1")
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if !ok {
		t.Errorf("want match against real Gears of War 3 PVD capture")
	}
}

// TestXbox360DecoyProber_RejectsUnrelatedMicrosoftMedia guards against
// the obvious false-positive risk: any disc merely published by
// Microsoft (e.g. a Windows install disc) must NOT match just because
// "Publisher id: MICROSOFT CORPORATION" appears — only the full CDIMAGE
// preparer/application combo counts.
func TestXbox360DecoyProber_RejectsUnrelatedMicrosoftMedia(t *testing.T) {
	fakeBin(t, "isoinfo", `cat <<'EOF'
CD-ROM is in ISO 9660 format
Volume id: WIN2K3
Publisher id: MICROSOFT CORPORATION
Data preparer id: MICROSOFT CORPORATION
Application id:
NO Joliet present
NO Rock Ridge present
EOF
`)

	p := identify.NewXbox360DecoyProber(identify.Xbox360DecoyProberConfig{IsoInfoBin: "isoinfo"})
	ok, err := p.Probe(context.Background(), "/dev/sr0")
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if ok {
		t.Errorf("want no match: missing the ONE MICROSOFT WAY + CDIMAGE fingerprint")
	}
}

// TestXbox360DecoyProber_RejectsOrdinaryDVD covers the common case: a
// normal movie DVD published by a film studio.
func TestXbox360DecoyProber_RejectsOrdinaryDVD(t *testing.T) {
	fakeBin(t, "isoinfo", `cat <<'EOF'
CD-ROM is in ISO 9660 format
Volume id: ARRIVAL
Publisher id:
Data preparer id:
Application id:
NO Joliet present
NO Rock Ridge present
EOF
`)

	p := identify.NewXbox360DecoyProber(identify.Xbox360DecoyProberConfig{IsoInfoBin: "isoinfo"})
	ok, err := p.Probe(context.Background(), "/dev/sr0")
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if ok {
		t.Errorf("want no match for an ordinary movie DVD")
	}
}

// TestXbox360DecoyProber_ExecFailure covers a disc isoinfo can't read at
// all (e.g. UDF-only, no ISO9660 bridge) -- reported as a clean "no
// match", not an error, so RefineDiscType's last-resort check degrades
// to DATA rather than logging a spurious warning on every UDF disc.
func TestXbox360DecoyProber_ExecFailure(t *testing.T) {
	p := identify.NewXbox360DecoyProber(identify.Xbox360DecoyProberConfig{IsoInfoBin: "/usr/bin/false"})
	ok, err := p.Probe(context.Background(), "/dev/sr0")
	if err != nil {
		t.Fatalf("want no error on isoinfo exec failure, got %v", err)
	}
	if ok {
		t.Errorf("want no match on exec failure")
	}
}

// TestRefineDiscType_Xbox360DecoyFallback verifies the RefineDiscType
// wiring end-to-end: a disc whose fs listing is too sparse to carry
// /_SYSTEMU (just a lone README.TXT, matching the real Gears of War 3
// capture) still classifies as XBOX360 when the decoy prober matches.
func TestRefineDiscType_Xbox360DecoyFallback(t *testing.T) {
	got := identify.RefineDiscType(
		context.Background(),
		state.DiscTypeData,
		&fakeFSProber{files: []string{"/README.TXT"}},
		&fakeBDProber{},
		nil, nil, nil, nil, nil,
		&fakeXbox360DecoyProber{ok: true},
		"/dev/sr1",
	)
	if got != state.DiscTypeXBOX360 {
		t.Fatalf("got %q, want XBOX360 (decoy PVD fallback)", got)
	}
}

// TestRefineDiscType_Xbox360DecoyFallback_Miss verifies a genuinely
// unrecognised disc still falls to DATA when the decoy prober also
// misses.
func TestRefineDiscType_Xbox360DecoyFallback_Miss(t *testing.T) {
	got := identify.RefineDiscType(
		context.Background(),
		state.DiscTypeData,
		&fakeFSProber{files: []string{"/README.TXT"}},
		&fakeBDProber{},
		nil, nil, nil, nil, nil,
		&fakeXbox360DecoyProber{ok: false},
		"/dev/sr1",
	)
	if got != state.DiscTypeData {
		t.Fatalf("got %q, want DATA", got)
	}
}

type fakeXbox360DecoyProber struct {
	ok  bool
	err error
}

func (f *fakeXbox360DecoyProber) Probe(_ context.Context, _ string) (bool, error) {
	return f.ok, f.err
}
