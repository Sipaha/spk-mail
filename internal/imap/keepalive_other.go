//go:build !linux

package imap

import "syscall"

// tuneTCPKeepAlive is a Linux-only optimisation: the raw setsockopt
// calls for TCP_KEEPIDLE / TCP_KEEPINTVL / TCP_KEEPCNT use Linux-
// specific syscall numbers. On other platforms we leave the socket
// on system defaults (which still get SO_KEEPALIVE via the
// per-Dialer KeepAlive duration set elsewhere if applicable). Dead
// IDLE sockets are then detected only by the per-IDLE 5-min bounce.
func tuneTCPKeepAlive(network, address string, c syscall.RawConn) error { return nil }
