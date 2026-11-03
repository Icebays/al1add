package portforward

import (
	"io"
	"net"
)

// copyData copies from src to dst and half-closes the TCP write side when done.
func copyData(dst, src net.Conn) (int64, error) {
	n, err := io.Copy(dst, src)
	if tc, ok := dst.(*net.TCPConn); ok {
		_ = tc.CloseWrite()
	}
	return n, err
}
