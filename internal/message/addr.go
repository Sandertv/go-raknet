package message

import (
	"encoding/binary"
	"net/netip"
)

type systemAddresses [20]netip.AddrPort

// sizeOf returns the size in bytes of the system addresses.
func (addresses systemAddresses) sizeOf() int {
	size := 0
	for _, addr := range addresses {
		size += sizeofAddr(addr)
	}
	return size
}

// sizeOfAddr returns the size in bytes of an address.
func sizeofAddr(addr netip.AddrPort) int {
	if addr.Addr().Is6() {
		return sizeofAddr6
	}
	return sizeofAddr4
}

const (
	sizeofAddr4 = 1 + 4 + 2
	sizeofAddr6 = 1 + 2 + 2 + 4 + 16 + 4
)

func putAddr(b []byte, addrPort netip.AddrPort) int {
	addr, port := addrPort.Addr(), addrPort.Port()
	if !addr.Is4() && !addr.Is6() {
		// Special case for zero addresses.
		b[0], b[1], b[2], b[3], b[4] = 4, 255, 255, 255, 255
		return sizeofAddr4
	} else if addr.Is4() {
		ip4 := addr.As4()
		b[0], b[1], b[2], b[3], b[4] = 4, ^ip4[0], ^ip4[1], ^ip4[2], ^ip4[3]
		binary.BigEndian.PutUint16(b[5:], port)
		return sizeofAddr4
	} else {
		ip16 := addr.As16()
		b[0] = 6
		binary.LittleEndian.PutUint16(b[1:], uint16(23)) // syscall.AF_INET6 on Windows.
		binary.BigEndian.PutUint16(b[3:], port)
		// 4 bytes.
		copy(b[9:], ip16[:])
		// 4 bytes.
		return sizeofAddr6
	}
}

func addr(b []byte) (netip.AddrPort, int) {
	if b[0] == 4 || b[0] == 0 {
		ip := netip.AddrFrom4([4]byte{(-b[1] - 1) & 0xff, (-b[2] - 1) & 0xff, (-b[3] - 1) & 0xff, (-b[4] - 1) & 0xff})
		port := binary.BigEndian.Uint16(b[5:])
		return netip.AddrPortFrom(ip, port), sizeofAddr4
	} else {
		port := binary.BigEndian.Uint16(b[3:])
		ip := netip.AddrFrom16([16]byte(b[9:]))
		return netip.AddrPortFrom(ip, port), sizeofAddr6
	}
}

func addrSize(b []byte) int {
	if len(b) == 0 || b[0] == 4 || b[0] == 0 {
		return sizeofAddr4
	}
	return sizeofAddr6
}
