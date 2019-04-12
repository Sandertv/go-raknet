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
	data := make([]byte, 3)
	if _, err := b.Read(data); err != nil {
		return 0, fmt.Errorf("error reading uint24: %v", err)
	}
	return uint24(data[0]) | (uint24(data[1]) << 8) | (uint24(data[2]) << 16), nil
}

// writeUint24 writes a uint24 to the buffer passed as 3 bytes. If not successful, an error is returned.
func writeUint24(b *bytes.Buffer, value uint24) error {
	data := []byte{
		byte(value),
		byte(value >> 8),
		byte(value >> 16),
	}
	if _, err := b.Write(data); err != nil {
		return fmt.Errorf("error writing uint24: %v", err)
	}
	return nil
}
