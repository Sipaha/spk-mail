//go:build linux

package imap

import (
	"syscall"

	"golang.org/x/sys/unix"
)

// tuneTCPKeepAlive enables SO_KEEPALIVE on the raw socket and tunes
// TCP_KEEPIDLE / TCP_KEEPINTVL / TCP_KEEPCNT to detect dead IMAP IDLE
// connections within ~60 seconds:
//
//   TCP_KEEPIDLE  = 30s   — start probing after 30s of silence
//   TCP_KEEPINTVL = 10s   — 10s between probes
//   TCP_KEEPCNT   = 3     — declare dead after 3 failed probes
//
// Total: 30s + 3×10s = 60s before kernel marks the socket failed and
// our blocked read wakes with ECONNRESET. The runIDLESession outer
// loop then dials fresh and re-IDLEs.
//
// Kernel defaults (tcp_keepalive_intvl=75s × tcp_keepalive_probes=9)
// would mean ~11 minutes — useless for the IDLE silent-death case.
//
// Failures here are best-effort: a kernel that rejects setsockopt
// just leaves the connection on system defaults; we still benefit
// from the per-IDLE 5-min bounce safety net at the application layer.
func tuneTCPKeepAlive(network, address string, c syscall.RawConn) error {
	return c.Control(func(fd uintptr) {
		_ = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_KEEPALIVE, 1)
		_ = unix.SetsockoptInt(int(fd), unix.IPPROTO_TCP, unix.TCP_KEEPIDLE, 30)
		_ = unix.SetsockoptInt(int(fd), unix.IPPROTO_TCP, unix.TCP_KEEPINTVL, 10)
		_ = unix.SetsockoptInt(int(fd), unix.IPPROTO_TCP, unix.TCP_KEEPCNT, 3)
	})
}
