package raknet

import (
	"bytes"
	"testing"
)

func Test_uint24(t *testing.T) {
	b := bytes.NewBuffer(nil)
	if err := writeUint24(b, 123456); err != nil {
		t.Fatalf("error writing uint24: %v", err)
	}
	val, err := readUint24(b)
	if err != nil {
		t.Fatalf("error reading uint24: %v", err)
	}
	if val != 123456 {
		t.Fatal("read uint24 was not equal to 123456")
	}
}
