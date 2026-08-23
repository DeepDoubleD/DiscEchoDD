//go:build linux

package drive

import (
	"encoding/binary"
	"strings"
	"testing"
)

func buildUdevFrame(t *testing.T, payload string) []byte {
	t.Helper()
	header := make([]byte, udevHeaderLen)
	copy(header, "libudev\x00")
	binary.BigEndian.PutUint32(header[8:12], udevMonitorMagic)
	binary.NativeEndian.PutUint32(header[16:20], udevHeaderLen)
	return append(header, []byte(payload)...)
}

func TestExtractUdevPayload_StripsHeader(t *testing.T) {
	want := "ACTION=change\x00SUBSYSTEM=block\x00"
	got, err := extractUdevPayload(buildUdevFrame(t, want))
	if err != nil {
		t.Fatalf("extractUdevPayload: %v", err)
	}
	if got != want {
		t.Errorf("payload = %q, want %q", got, want)
	}
}

func TestExtractUdevPayload_RejectsMissingPrefix(t *testing.T) {
	if _, err := extractUdevPayload([]byte("not a udev frame at all, but long enough..")); err != errNotUdevFramed {
		t.Errorf("want errNotUdevFramed, got %v", err)
	}
}

func TestExtractUdevPayload_RejectsShortMessage(t *testing.T) {
	if _, err := extractUdevPayload([]byte("libudev\x00short")); err != errNotUdevFramed {
		t.Errorf("want errNotUdevFramed, got %v", err)
	}
}

func TestExtractUdevPayload_RejectsBadMagic(t *testing.T) {
	frame := buildUdevFrame(t, "ACTION=add\x00")
	binary.BigEndian.PutUint32(frame[8:12], 0)
	if _, err := extractUdevPayload(frame); err != errNotUdevFramed {
		t.Errorf("want errNotUdevFramed, got %v", err)
	}
}

func TestExtractUdevPayload_RejectsOffsetOutOfRange(t *testing.T) {
	frame := buildUdevFrame(t, "ACTION=add\x00")
	binary.NativeEndian.PutUint32(frame[16:20], uint32(len(frame)+100))
	if _, err := extractUdevPayload(frame); err == nil || strings.Contains(err.Error(), "not udevd-framed") {
		t.Errorf("want out-of-range error, got %v", err)
	}
}
