package identify

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
)

// ErrNotXbox360 is returned when default.xex is missing, doesn't start
// with the XEX2 magic, or has no Execution ID header.
var ErrNotXbox360 = errors.New("identify: not an Xbox 360 disc")

// xexExecutionIDHeaderKey is the XEX optional-header directory key for
// the Execution ID block. Computed as (0x0400 << 8) | (sizeof(execID
// struct)>>2) = (0x0400<<8) | (0x18>>2) = 0x040006, per the XEX2 header
// convention (Free60 wiki, cross-checked against idaxex's header-ID
// macro and its XexExecutionId struct, which reports a 0x18-byte
// size). Retail Xbox 360 discs use the XEX2 header revision — earlier
// XEX1/XEX-/XEX? variants are devkit-only and not handled here.
const xexExecutionIDHeaderKey = 0x00040006

// xexExecutionIDSize is the byte size of the Execution ID struct this
// package reads: MediaID(4) + Version(4) + BaseVersion(4) + TitleID(4)
// + Platform(1) + ExecutableType(1) + DiscNumber(1) + DiscCount(1) +
// SaveGameID(4) = 0x18. Only TitleID (the first 4 bytes after the
// first three fields, i.e. offset +0xC) is actually consumed.
const xexExecutionIDSize = 0x18

// Xbox360Info is the subset of XEX Execution ID data the daemon consults.
type Xbox360Info struct {
	TitleID uint32 // big-endian uint32 from the Execution ID header
}

// ProbeXEX parses a .xex binary blob (as extracted from the disc's
// default.xex, analogous to ProbeXBE for original Xbox's default.xbe)
// and returns its Execution ID title ID.
//
// XEX2 layout consulted:
//   - magic "XEX2" at offset 0
//   - optional-header count (uint32 BE) at 0x14
//   - optional-header directory starting at 0x18, 8 bytes per entry:
//     4-byte header key (BE) + 4-byte value. For a struct-typed header
//     like Execution ID, the value is a byte offset (from the start of
//     the file) to seek to, not inline data -- confirmed against
//     idaxex's opt_header_ptr, which indexes its header buffer
//     directly by the directory entry's value.
//   - at that offset: the Execution ID struct, TitleID at +0xC.
func ProbeXEX(data []byte) (*Xbox360Info, error) {
	const fixedHeaderSize = 0x18
	if len(data) < fixedHeaderSize || !bytes.HasPrefix(data, []byte("XEX2")) {
		return nil, ErrNotXbox360
	}
	count := binary.BigEndian.Uint32(data[0x14:])
	const entrySize = 8
	dirStart := fixedHeaderSize
	dirEnd := dirStart + int(count)*entrySize
	if count > 0 && (dirEnd < dirStart || dirEnd > len(data)) {
		return nil, fmt.Errorf("xex: header directory out of range (count=%d, file=%d bytes)", count, len(data))
	}
	var execOff uint32
	found := false
	for off := dirStart; off < dirEnd; off += entrySize {
		key := binary.BigEndian.Uint32(data[off:])
		if key == xexExecutionIDHeaderKey {
			execOff = binary.BigEndian.Uint32(data[off+4:])
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("xex: no Execution ID header (0x%06x) in %d directory entries", xexExecutionIDHeaderKey, count)
	}
	if uint64(execOff)+xexExecutionIDSize > uint64(len(data)) {
		return nil, fmt.Errorf("xex: Execution ID block truncated (offset=%#x, file=%d bytes)", execOff, len(data))
	}
	titleID := binary.BigEndian.Uint32(data[execOff+0xC:])
	return &Xbox360Info{TitleID: titleID}, nil
}
