package codec

import (
	"bytes"
	"encoding/hex"
	"errors"
	"io"
	"testing"
)

func TestFrameRoundTrip(t *testing.T) {
	payload := []byte{Codec8E, 0x01, 0x02, 0x03}
	frame := Frame(payload)
	if len(frame) != HeaderSize+len(payload)+CRCSize {
		t.Fatalf("frame is %d bytes, want %d", len(frame), HeaderSize+len(payload)+CRCSize)
	}
	got, size, err := Unframe(frame)
	if err != nil {
		t.Fatalf("unframe: %v", err)
	}
	if size != len(frame) {
		t.Fatalf("unframe reports %d bytes, want %d", size, len(frame))
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload %s, want %s", hex.EncodeToString(got), hex.EncodeToString(payload))
	}
}

func TestUnframeReportsBadCRC(t *testing.T) {
	frame := Frame([]byte{Codec8E, 0x01})
	frame[len(frame)-1] ^= 0x01
	if _, _, err := Unframe(frame); !errors.Is(err, ErrBadCRC) {
		t.Fatalf("error %v, want ErrBadCRC", err)
	}
}

func TestReadFrameFromStream(t *testing.T) {
	first := Frame([]byte{Codec8E, 0x01, 0xAA})
	second := Frame([]byte{Codec8, 0x02, 0xBB})
	stream := bytes.NewReader(append(bytes.Clone(first), second...))

	got, err := ReadFrame(stream)
	if err != nil {
		t.Fatalf("read first frame: %v", err)
	}
	if !bytes.Equal(got, first) {
		t.Fatalf("first frame %s, want %s", hex.EncodeToString(got), hex.EncodeToString(first))
	}
	if got, err = ReadFrame(stream); err != nil {
		t.Fatalf("read second frame: %v", err)
	}
	if !bytes.Equal(got, second) {
		t.Fatalf("second frame %s, want %s", hex.EncodeToString(got), hex.EncodeToString(second))
	}
	if _, err = ReadFrame(stream); !errors.Is(err, io.EOF) {
		t.Fatalf("error at end of stream %v, want io.EOF", err)
	}
}

func TestHandshakeRoundTrip(t *testing.T) {
	const imei = "356307042441013"
	encoded, err := EncodeIMEI(imei)
	if err != nil {
		t.Fatalf("encode imei: %v", err)
	}
	if got, want := hex.EncodeToString(encoded), "000f333536333037303432343431303133"; got != want {
		t.Fatalf("handshake %s, want %s", got, want)
	}
	got, err := ReadIMEI(bytes.NewReader(encoded))
	if err != nil {
		t.Fatalf("read imei: %v", err)
	}
	if got != imei {
		t.Fatalf("imei %q, want %q", got, imei)
	}
}

func TestReadIMEIRejectsGarbage(t *testing.T) {
	if _, err := ReadIMEI(bytes.NewReader([]byte("GET / HTTP/1.1\r\n\r\n"))); err == nil {
		t.Error("an http request was accepted as a handshake")
	}
	if _, err := ReadIMEI(bytes.NewReader([]byte{0x00, 0x0F, 'a', 'b'})); err == nil {
		t.Error("a truncated handshake was accepted")
	}
}

func TestACKRoundTrip(t *testing.T) {
	for _, n := range []uint32{0, 1, 2, 10, 255, 1 << 20} {
		got, err := DecodeACK(EncodeACK(n))
		if err != nil {
			t.Fatalf("decode ack for %d: %v", n, err)
		}
		if got != n {
			t.Fatalf("ack %d, want %d", got, n)
		}
	}
	if _, err := DecodeACK([]byte{0x00, 0x01}); err == nil {
		t.Error("a two byte ack was accepted")
	}
}
