package identify

import (
	"bytes"
	"testing"

	"github.com/jumpingmushroom/DiscEcho/daemon/state"
)

// sectorWith returns a 0x8800-byte buffer with magic placed at offset.
// The larger size ensures ProbeCDGameReader tests cover the full read.
func sectorWith(offset int, magic string) []byte {
	buf := make([]byte, 0x8800)
	copy(buf[offset:], []byte(magic))
	return buf
}

func TestProbeCDGameReader(t *testing.T) {
	tests := []struct {
		name    string
		buf     []byte
		wantDT  state.DiscType
		wantHit bool
	}{
		{
			name:    "SegaCD SEGADISCSYSTEM at offset 0",
			buf:     sectorWith(0, "SEGADISCSYSTEM"),
			wantDT:  state.DiscTypeSegaCD,
			wantHit: true,
		},
		{
			name:    "SegaCD SEGABOOTDISC at offset 0",
			buf:     sectorWith(0, "SEGABOOTDISC"),
			wantDT:  state.DiscTypeSegaCD,
			wantHit: true,
		},
		{
			name:    "SegaCD SEGADISCSYSTEM at offset 0x10 (raw sector)",
			buf:     sectorWith(0x10, "SEGADISCSYSTEM"),
			wantDT:  state.DiscTypeSegaCD,
			wantHit: true,
		},
		{
			name:    "SegaCD SEGABOOTDISC at offset 0x10 (raw sector)",
			buf:     sectorWith(0x10, "SEGABOOTDISC"),
			wantDT:  state.DiscTypeSegaCD,
			wantHit: true,
		},
		{
			name:    "3DO Opera filesystem magic at offset 0",
			buf:     sectorWith(0, "\x01ZZZZZ\x01"),
			wantDT:  state.DiscType3DO,
			wantHit: true,
		},
		{
			name:    "PC-FX signature somewhere in sector",
			buf:     sectorWith(64, "PC-FX:Hu_CD-ROM"),
			wantDT:  state.DiscTypePCFX,
			wantHit: true,
		},
		{
			name:    "Atari Jaguar CD signature somewhere in sector",
			buf:     sectorWith(128, "ATARI APPROVED DATA HEADER ATRI"),
			wantDT:  state.DiscTypeJaguarCD,
			wantHit: true,
		},
		{
			name:    "no match on zeroed sector",
			buf:     make([]byte, 2048),
			wantDT:  "",
			wantHit: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dt, ok := ProbeCDGameReader(bytes.NewReader(tc.buf))
			if ok != tc.wantHit {
				t.Fatalf("got ok=%v, want %v", ok, tc.wantHit)
			}
			if dt != tc.wantDT {
				t.Fatalf("got dt=%q, want %q", dt, tc.wantDT)
			}
		})
	}
}

// pvdBuf returns a 0x8800-byte buffer with the ISO9660 PVD region populated
// at 0x8000. stdID is the 5-byte standard identifier (bytes 1-5); sysID is
// written into the 32-byte system identifier field (bytes 8-39).
func pvdBuf(stdID, sysID string) []byte {
	buf := make([]byte, 0x8800)
	const pvd = 0x8000
	if stdID != "" {
		copy(buf[pvd+1:pvd+6], []byte(stdID))
	}
	if sysID != "" {
		copy(buf[pvd+8:pvd+40], []byte(sysID))
	}
	return buf
}

func TestProbeCDi_PVDSystemID(t *testing.T) {
	tests := []struct {
		name    string
		buf     []byte
		wantDT  state.DiscType
		wantHit bool
	}{
		{
			name:    "CD-RTOS in system identifier",
			buf:     pvdBuf("", "CD-RTOS"),
			wantDT:  state.DiscTypeCDi,
			wantHit: true,
		},
		{
			name:    "CD-I  as standard identifier",
			buf:     pvdBuf("CD-I ", ""),
			wantDT:  state.DiscTypeCDi,
			wantHit: true,
		},
		{
			name:    "zeroed buffer — no match",
			buf:     make([]byte, 0x8800),
			wantDT:  "",
			wantHit: false,
		},
		{
			name:    "buffer shorter than PVD region — no panic, no match",
			buf:     make([]byte, 0x100),
			wantDT:  "",
			wantHit: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dt, ok := probeCDi(tc.buf)
			if ok != tc.wantHit {
				t.Fatalf("got ok=%v, want %v", ok, tc.wantHit)
			}
			if dt != tc.wantDT {
				t.Fatalf("got dt=%q, want %q", dt, tc.wantDT)
			}
		})
	}
}

func TestProbeCDGameReader_CDi_Combined(t *testing.T) {
	// CD-i PVD only (no sector-0 magic) — must resolve to CDI.
	cdiOnly := pvdBuf("CD-I ", "")
	dt, ok := ProbeCDGameReader(bytes.NewReader(cdiOnly))
	if !ok || dt != state.DiscTypeCDi {
		t.Fatalf("CD-i PVD only: got (%q, %v), want (CDI, true)", dt, ok)
	}

	// Sega CD magic at sector 0 wins over any PVD content.
	segaWithCDiPVD := pvdBuf("CD-I ", "")
	copy(segaWithCDiPVD[0:], []byte("SEGADISCSYSTEM"))
	dt, ok = ProbeCDGameReader(bytes.NewReader(segaWithCDiPVD))
	if !ok || dt != state.DiscTypeSegaCD {
		t.Fatalf("SegaCD magic + CD-i PVD: got (%q, %v), want (SegaCD, true)", dt, ok)
	}
}
