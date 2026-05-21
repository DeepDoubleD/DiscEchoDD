package identify

import (
	"bytes"
	"context"
	"io"
	"os"

	"github.com/jumpingmushroom/DiscEcho/daemon/state"
)

type cdGameMagic struct {
	dt      state.DiscType
	magic   []byte
	exactAt int // >=0: exact offset match; <0: bytes.Contains scan
}

// cdGameMagicTable holds clean-signature CD consoles, checked in order.
// Sources: file(1) magic/Magdir/console + emulation docs.
// Sega CD supports both cooked-sector layout (magic at 0) and raw 2352-byte
// sector layout (magic at 0x10, after the 16-byte sync header).
// PC-FX and Atari Jaguar CD use a Contains scan because exact sector offsets
// are unverified without sample disc images.
//
// Known real-disc limitations to revisit when sample discs are available
// (probers are otherwise fixture-tested only):
//   - Jaguar CD: the boot header lives in the disc's SECOND session, not
//     sector 0, so this matches Jaguar .bin/.iso images but will miss a
//     physical Jaguar disc read from /dev/sr0 at offset 0. Manual override
//     covers the miss until a session-2 seek is added.
//   - PC-FX: "PC-FX:Hu_CD-ROM" is narrower than the bare "PC-FX" token some
//     discs carry; broaden if real discs slip through to DATA.
var cdGameMagicTable = []cdGameMagic{
	{state.DiscTypeSegaCD, []byte("SEGADISCSYSTEM"), 0},
	{state.DiscTypeSegaCD, []byte("SEGABOOTDISC"), 0},
	{state.DiscTypeSegaCD, []byte("SEGADISCSYSTEM"), 0x10},
	{state.DiscTypeSegaCD, []byte("SEGABOOTDISC"), 0x10},
	{state.DiscType3DO, []byte("\x01ZZZZZ\x01"), 0},
	{state.DiscTypePCFX, []byte("PC-FX:Hu_CD-ROM"), -1},
	{state.DiscTypeJaguarCD, []byte("ATARI APPROVED DATA HEADER ATRI"), -1},
}

// matchMagicTable checks buf against the sector-0 magic table.
func matchMagicTable(buf []byte) (state.DiscType, bool) {
	for _, m := range cdGameMagicTable {
		if m.exactAt >= 0 {
			end := m.exactAt + len(m.magic)
			if len(buf) >= end && bytes.Equal(buf[m.exactAt:end], m.magic) {
				return m.dt, true
			}
		} else if bytes.Contains(buf, m.magic) {
			return m.dt, true
		}
	}
	return "", false
}

// probeCDi checks the ISO9660/CD-RTOS volume descriptor at byte offset
// 0x8000 (LBA 16 × 2048). CD-i (Green Book) discs use "CD-I " as the
// standard identifier (bytes 1-5) and/or "CD-RTOS" in the system identifier
// (bytes 8-39). A short buffer (e.g. tiny test fixture) never panics.
func probeCDi(buf []byte) (state.DiscType, bool) {
	const pvd = 0x8000
	if len(buf) < pvd+40 {
		return "", false
	}
	std := buf[pvd+1 : pvd+6]  // standard identifier (5 bytes)
	sys := buf[pvd+8 : pvd+40] // system identifier (32 bytes)
	if bytes.Equal(std, []byte("CD-I ")) ||
		bytes.Contains(sys, []byte("CD-RTOS")) ||
		bytes.Contains(sys, []byte("CD-I")) {
		return state.DiscTypeCDi, true
	}
	return "", false
}

// ProbeCDGameReader reads a fixed prefix large enough to cover both sector-0
// magic table signatures and the ISO9660 Primary Volume Descriptor region at
// LBA 16 (byte offset 0x8000). Returns ("", false) when nothing matches (disc
// falls through to plain DATA classification). Partial reads from scratched
// discs are still matched against whatever bytes were returned.
func ProbeCDGameReader(r io.Reader) (state.DiscType, bool) {
	// 0x8800 = LBA 16 (0x8000) + one full sector (0x800) — covers both the
	// sector-0 magic table and the PVD at 0x8000 in a single read.
	buf := make([]byte, 0x8800)
	n, _ := io.ReadFull(r, buf) // partial read is fine
	buf = buf[:n]
	if dt, ok := matchMagicTable(buf); ok {
		return dt, ok
	}
	return probeCDi(buf)
}

// ProbeCDGame opens devPath and probes sector 0 of the data track.
func ProbeCDGame(devPath string) (state.DiscType, bool) {
	f, err := os.Open(devPath)
	if err != nil {
		return "", false
	}
	defer func() { _ = f.Close() }()
	return ProbeCDGameReader(f)
}

// DevCDGameProber implements a device-backed CD console prober.
type DevCDGameProber struct{}

// NewDevCDGameProber returns a DevCDGameProber ready for use.
func NewDevCDGameProber() *DevCDGameProber { return &DevCDGameProber{} }

// Probe reads the first data sector of devPath and returns the matched
// console disc type, or ("", false) if no magic matched.
func (*DevCDGameProber) Probe(_ context.Context, devPath string) (state.DiscType, bool) {
	return ProbeCDGame(devPath)
}
