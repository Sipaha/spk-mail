package sync

import (
	"net"
	"strconv"
)

// splitHostPortAddr is a test-only helper: takes "host:port" (as produced by
// mockimap.Server.Addr) and returns the parts in the storage row's typed shape.
func splitHostPortAddr(addr string) (string, int) {
	host, port, _ := net.SplitHostPort(addr)
	p, _ := strconv.Atoi(port)
	return host, p
}
