//go:build linux

package drive

import (
	"encoding/binary"
	"errors"
	"syscall"
)

// This file is a small, from-scratch client for the Linux kernel's device
// event bus: an AF_NETLINK/NETLINK_KOBJECT_UEVENT socket, subscribed to the
// "udev" multicast group so events already carry udevd-enriched properties
// (ID_CDROM, DISK_MEDIA_CHANGE, etc.) rather than the bare kernel event.
// The wire format is systemd's public udev_monitor_netlink_header struct
// (see src/libudev/libudev-monitor.c in the systemd source) — a documented
// interoperability protocol, not vendored code.

// udevMonitorGroup selects the udevd-processed event stream. Group 1 would
// be raw, unprocessed kernel events; group 2 is udevd's own re-broadcast,
// which is what carries the ID_CDROM/DISK_MEDIA_CHANGE properties this
// daemon depends on for optical-media detection.
const udevMonitorGroup = 2

// udevMonitorMagic is the constant udevd stamps (network byte order) at
// byte offset 8 of every message it re-broadcasts, letting a listener
// distinguish a udevd-framed message from a raw kernel one.
const udevMonitorMagic = 0xfeedcafe

// udevHeaderLen is the fixed size of the binary header udevd prepends:
// an 8-byte "libudev\0" prefix followed by 8 uint32 fields (magic,
// header size, properties offset/len, and three filter hashes/bloom
// bits this daemon doesn't use).
const udevHeaderLen = 40

var errNotUdevFramed = errors.New("udev netlink: message is not udevd-framed")

// netlinkConn is a raw kernel-uevent socket.
type netlinkConn struct {
	fd int
}

// dialUdevMonitor opens and binds the socket. The caller owns the
// returned conn and must Close it.
func dialUdevMonitor() (*netlinkConn, error) {
	fd, err := syscall.Socket(syscall.AF_NETLINK, syscall.SOCK_RAW, syscall.NETLINK_KOBJECT_UEVENT)
	if err != nil {
		return nil, err
	}
	addr := &syscall.SockaddrNetlink{Family: syscall.AF_NETLINK, Groups: udevMonitorGroup}
	if err := syscall.Bind(fd, addr); err != nil {
		_ = syscall.Close(fd)
		return nil, err
	}
	return &netlinkConn{fd: fd}, nil
}

func (c *netlinkConn) Close() error {
	return syscall.Close(c.fd)
}

// readPayload blocks for the next datagram and returns its property
// list as a NUL-separated "KEY=VALUE" string, with udevd's binary
// header stripped. Pass the result to ParseUevent.
func (c *netlinkConn) readPayload() (string, error) {
	// A netlink datagram this daemon cares about is a short property
	// list; 64KiB is generous headroom over anything udevd actually
	// sends. Recvfrom truncates rather than blocking again if a
	// message is somehow larger, which just drops that one event —
	// the reconnect/retry loop in Watch already tolerates that.
	buf := make([]byte, 64*1024)
	n, _, err := syscall.Recvfrom(c.fd, buf, 0)
	if err != nil {
		return "", err
	}
	return extractUdevPayload(buf[:n])
}

// extractUdevPayload validates and strips the udev_monitor_netlink_header
// framing, returning the raw property-list bytes as a string. Split out
// from readPayload so the parsing logic is unit-testable without a real
// netlink socket.
func extractUdevPayload(raw []byte) (string, error) {
	if len(raw) < udevHeaderLen || string(raw[:8]) != "libudev\x00" {
		return "", errNotUdevFramed
	}
	if magic := binary.BigEndian.Uint32(raw[8:12]); magic != udevMonitorMagic {
		return "", errNotUdevFramed
	}
	off := binary.NativeEndian.Uint32(raw[16:20])
	if int(off) >= len(raw) {
		return "", errors.New("udev netlink: payload offset out of range")
	}
	return string(raw[off:]), nil
}
