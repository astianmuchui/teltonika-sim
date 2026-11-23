package codec

import (
	"bytes"
	"encoding/hex"
	"strings"
	"testing"

	lib "github.com/alim-zanibekov/teltonika"
)

func TestCodec12RoundTrip(t *testing.T) {
	kinds := []struct {
		name   string
		encode func(string) ([]byte, error)
		kind   byte
		libKey lib.MessageType
	}{
		{"command", EncodeCommand, CommandRequest, lib.TypeCommand},
		{"response", EncodeResponse, CommandResponse, lib.TypeResponse},
	}
	texts := []string{
		"getinfo",
		"getver",
		"getgps",
		"getio",
		"setdigout 1",
		"DOUT1:1",
		"Unknown command",
		"INI:2020/7/16 11:29 RTC:2020/7/16 11:29 RST:1 ERR:0 SR:0 BR:0 CF:0 CU:0 UT:0 SMS:0",
		strings.Repeat("A", 1024),
	}
	for _, k := range kinds {
		for _, text := range texts {
			raw, err := k.encode(text)
			if err != nil {
				t.Fatalf("%s %q: encode: %v", k.name, text, err)
			}
			p, err := Decode(raw)
			if err != nil {
				t.Fatalf("%s %q: our decoder: %v", k.name, text, err)
			}
			if p.CodecID != Codec12 {
				t.Fatalf("%s %q: codec id 0x%02X, want 0x0C", k.name, text, p.CodecID)
			}
			if p.Command == nil {
				t.Fatalf("%s %q: decoded packet carries no command", k.name, text)
			}
			if p.Command.Type != k.kind {
				t.Errorf("%s %q: type 0x%02X, want 0x%02X", k.name, text, p.Command.Type, k.kind)
			}
			if p.Command.Text != text {
				t.Errorf("%s: text %q, want %q", k.name, p.Command.Text, text)
			}
			if p.Command.Quantity != 1 {
				t.Errorf("%s %q: quantity %d, want 1", k.name, text, p.Command.Quantity)
			}

			read, theirs, err := lib.DecodeTCPFromSlice(raw)
			if err != nil {
				t.Fatalf("%s %q: library decoder: %v\nframe %s", k.name, text, err, hex.EncodeToString(raw))
			}
			if read != len(raw) {
				t.Fatalf("%s %q: library consumed %d of %d bytes", k.name, text, read, len(raw))
			}
			if byte(theirs.Packet.CodecID) != Codec12 {
				t.Fatalf("%s %q: library read codec 0x%02X", k.name, text, byte(theirs.Packet.CodecID))
			}
			if len(theirs.Packet.Messages) != 1 {
				t.Fatalf("%s %q: library read %d messages, want 1", k.name, text, len(theirs.Packet.Messages))
			}
			msg := theirs.Packet.Messages[0]
			if msg.Type != k.libKey {
				t.Errorf("%s %q: library read type 0x%02X, want 0x%02X", k.name, text, msg.Type, k.libKey)
			}
			if msg.Text != text {
				t.Errorf("%s: library read text %q, want %q", k.name, msg.Text, text)
			}
		}
	}
}

func TestCodec12KnownVector(t *testing.T) {
	const want = "000000000000000f0c010500000007676574696e666f0100004312"
	raw, err := EncodeCommand("getinfo")
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if got := hex.EncodeToString(raw); got != want {
		t.Fatalf("getinfo frame\n got %s\nwant %s", got, want)
	}
}

func TestCodec12RejectsEmptyText(t *testing.T) {
	if _, err := EncodeCommand(""); err == nil {
		t.Error("encoded an empty command without error")
	}
	if _, err := EncodeResponse(""); err == nil {
		t.Error("encoded an empty response without error")
	}
}

func TestCodec12RejectsBadQuantity(t *testing.T) {
	raw, err := EncodeCommand("getinfo")
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	payload := bytes.Clone(raw[HeaderSize : len(raw)-CRCSize])
	payload[len(payload)-1] = 0x02
	if _, err := Decode(Frame(payload)); err == nil {
		t.Error("a codec 12 frame whose quantity bytes disagree decoded without error")
	}
}
