//go:build !windows

package raknet

import (
	"errors"
	"syscall"
)

// isTransientUDPReadError returns true for read errors on connected UDP sockets
// commonly caused by ICMP errors. These may be due to lossy client networks or
// normal transient conditions, and are safe to retry during the handshake.
func isTransientUDPReadError(err error) bool {
	if err == nil {
		return false
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		switch errno {
		case syscall.ECONNREFUSED, syscall.EHOSTUNREACH, syscall.ENETUNREACH, syscall.ECONNRESET:
			return true
		}
	}
	return false
}
