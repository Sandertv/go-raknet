//go:build windows

package raknet

import (
	"errors"
	"syscall"
)

// Winsock error codes for transient UDP errors.
const (
	wsaeConnRefused = syscall.Errno(10061) // ECONNREFUSED
	wsaeHostUnreach = syscall.Errno(10065) // EHOSTUNREACH
	wsaeNetUnreach  = syscall.Errno(10051) // ENETUNREACH
	wsaeConnReset   = syscall.Errno(10054) // ECONNRESET
	wsaeConnAborted = syscall.Errno(10053) // ECONNABORTED
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
		case wsaeConnRefused, wsaeHostUnreach, wsaeNetUnreach, wsaeConnReset, wsaeConnAborted:
			return true
		}
	}
	return false
}
