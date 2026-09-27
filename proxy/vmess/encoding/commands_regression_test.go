package encoding

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestUnsupportedCommands(t *testing.T) {
	for _, command := range []any{nil, struct{}{}, byte(1)} {
		var output bytes.Buffer
		if err := MarshalCommand(command, &output); err != ErrUnknownCommand || output.Len() != 0 {
			t.Fatalf("marshal = %v, %x", err, output.Bytes())
		}
	}
	for id := 0; id < 256; id++ {
		data := make([]byte, 7)
		copy(data[4:], []byte("cmd"))
		binary.BigEndian.PutUint32(data, Authenticate(data[4:]))
		if cmd, err := UnmarshalCommand(byte(id), data); cmd != nil || err != ErrUnknownCommand {
			t.Fatalf("id %d: %v %v", id, cmd, err)
		}
		data[0] ^= 1
		if _, err := UnmarshalCommand(byte(id), data); err != ErrInvalidAuth {
			t.Fatalf("bad auth: %v", err)
		}
	}
	if _, err := UnmarshalCommand(1, []byte{1}); err != ErrInsufficientLength {
		t.Fatalf("short: %v", err)
	}
}
