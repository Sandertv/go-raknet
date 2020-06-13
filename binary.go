package raknet

import (
	"bytes"
	"fmt"
)

// uint24 represents an integer existing out of 3 bytes. It is actually a uint32, but is an alias for the
// sake of clarity.
type uint24 uint32

// readUint24 reads 3 bytes from the buffer passed and combines it into a uint24. If there were no 3 bytes to
// read, an error is returned.
func readUint24(b *bytes.Buffer) (uint24, error) {
	ba, _ := b.ReadByte()
	bb, _ := b.ReadByte()
	bc, err := b.ReadByte()
	if err != nil {
		return 0, fmt.Errorf("error reading uint24: %v", err)
	}
	return uint24(ba) | (uint24(bb) << 8) | (uint24(bc) << 16), nil
}

// writeUint24 writes a uint24 to the buffer passed as 3 bytes. If not successful, an error is returned.
func writeUint24(b *bytes.Buffer, value uint24) {
	b.WriteByte(byte(value))
	b.WriteByte(byte(value >> 8))
	b.WriteByte(byte(value >> 16))
}
