package raknet

import (
	"bytes"
	"fmt"
)

// readUint24 reads 3 bytes from the buffer passed and combines it into a uint24. If there were no 3 bytes to
// read, an error is returned.
func readUint24(b *bytes.Buffer) (uint32, error) {
	data := make([]byte, 3)
	if _, err := b.Read(data); err != nil {
		return 0, fmt.Errorf("error reading uint24: %v", err)
	}
	return uint32(data[0]) | (uint32(data[1]) << 8) | (uint32(data[2]) << 16), nil
}

// writeUint24 writes a uint24 to the buffer passed as 3 bytes. If not successful, an error is returned.
func writeUint24(b *bytes.Buffer, value uint32) error {
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
