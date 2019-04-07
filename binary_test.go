package raknet

import (
	"bytes"
	"testing"
)

func Test_LEUint24(t *testing.T) {
	b := bytes.NewBuffer(nil)
	if err := writeUint24(b, 123456); err != nil {
		t.Error(err)
	}
	val, err := readUint24(b)
	if err != nil {
		t.Error(err)
	}
	if val != 123456 {
		t.Error("read uint24 was not equal to 123456")
	}
}
