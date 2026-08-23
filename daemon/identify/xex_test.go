package identify

import (
	"encoding/binary"
	"errors"
	"testing"
)

// writeXEX2 builds a minimal synthetic XEX2 buffer: the 0x18-byte fixed
// header (magic + entry count), a directory of dirEntries (each
// key/value pair), and an Execution ID block placed immediately after
// the directory. Returns the full buffer plus the file offset the
// Execution ID block was written at.
func writeXEX2(t *testing.T, dirEntries [][2]uint32, titleID uint32) []byte {
	t.Helper()
	const fixedHeaderSize = 0x18
	const entrySize = 8
	dirLen := len(dirEntries) * entrySize
	execOff := fixedHeaderSize + dirLen
	buf := make([]byte, execOff+xexExecutionIDSize)

	copy(buf, []byte("XEX2"))
	binary.BigEndian.PutUint32(buf[0x14:], uint32(len(dirEntries)))
	for i, e := range dirEntries {
		off := fixedHeaderSize + i*entrySize
		binary.BigEndian.PutUint32(buf[off:], e[0])
		binary.BigEndian.PutUint32(buf[off+4:], e[1])
	}
	// Execution ID struct: MediaID(4) Version(4) BaseVersion(4) TitleID(4) ...
	binary.BigEndian.PutUint32(buf[execOff+0xC:], titleID)
	return buf
}

func TestProbeXEX_OK(t *testing.T) {
	// Directory has a decoy entry before the real Execution ID entry to
	// prove the loop doesn't just grab the first one.
	const execOffPlaceholder = 0x28 // fixedHeaderSize(0x18) + 2 entries * 8
	buf := writeXEX2(t, [][2]uint32{
		{0x00030002, 0xDEADBEEF}, // decoy header, unrelated key
		{xexExecutionIDHeaderKey, execOffPlaceholder},
	}, 0x4D5307D5)
	info, err := ProbeXEX(buf)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if info.TitleID != 0x4D5307D5 {
		t.Fatalf("title id: got %#x, want %#x", info.TitleID, 0x4D5307D5)
	}
}

func TestProbeXEX_NoExecutionIDHeader(t *testing.T) {
	buf := writeXEX2(t, [][2]uint32{
		{0x00030002, 0xDEADBEEF},
	}, 0x4D5307D5)
	_, err := ProbeXEX(buf)
	if err == nil {
		t.Fatal("want error when Execution ID header is absent")
	}
}

func TestProbeXEX_BadMagic(t *testing.T) {
	_, err := ProbeXEX([]byte("XEX1garbage"))
	if !errors.Is(err, ErrNotXbox360) {
		t.Fatalf("expected ErrNotXbox360, got %v", err)
	}
}

func TestProbeXEX_TooShort(t *testing.T) {
	_, err := ProbeXEX([]byte("XEX2"))
	if !errors.Is(err, ErrNotXbox360) {
		t.Fatalf("expected ErrNotXbox360, got %v", err)
	}
}

func TestProbeXEX_TruncatedExecutionIDBlock(t *testing.T) {
	// Directory entry points past the end of the buffer.
	buf := writeXEX2(t, [][2]uint32{
		{xexExecutionIDHeaderKey, 0xFFFFFF},
	}, 0x4D5307D5)
	_, err := ProbeXEX(buf)
	if err == nil {
		t.Fatal("want error when Execution ID offset is out of range")
	}
}

func TestProbeXEX_DirectoryCountOutOfRange(t *testing.T) {
	buf := make([]byte, 0x18)
	copy(buf, []byte("XEX2"))
	binary.BigEndian.PutUint32(buf[0x14:], 0xFFFFFFFF) // absurd entry count
	_, err := ProbeXEX(buf)
	if err == nil {
		t.Fatal("want error when directory count would overflow the buffer")
	}
}
