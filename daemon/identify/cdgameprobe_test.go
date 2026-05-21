package identify

import (
	"bytes"
	"testing"

	"github.com/jumpingmushroom/DiscEcho/daemon/state"
)

// sectorWith returns a 2048-byte buffer with magic placed at offset.
func sectorWith(offset int, magic string) []byte {
	buf := make([]byte, 2048)
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
