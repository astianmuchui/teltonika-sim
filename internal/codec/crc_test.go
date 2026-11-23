package codec

import (
	"encoding/binary"
	"testing"
)

func TestCRC16TestVector(t *testing.T) {
	frame := testVectorFrame(t)
	payload := frame[HeaderSize : len(frame)-CRCSize]
	got := CRC16(payload)
	if got != 0xBF30 {
		t.Fatalf("CRC16 over the test vector = 0x%04X, want 0xBF30", got)
	}
	want := binary.BigEndian.Uint32(frame[len(frame)-CRCSize:])
	if uint32(got) != want {
		t.Fatalf("CRC16 = 0x%08X, the frame carries 0x%08X", uint32(got), want)
	}
}

func TestCRC16CheckValue(t *testing.T) {
	if got := CRC16([]byte("123456789")); got != 0xBB3D {
		t.Fatalf("CRC16(123456789) = 0x%04X, want 0xBB3D for CRC-16/ARC", got)
	}
	if got := CRC16(nil); got != 0 {
		t.Fatalf("CRC16 of no bytes = 0x%04X, want 0x0000", got)
	}
}

func TestCRC16CoversCodecIDThroughNumberOfData2(t *testing.T) {
	frame := testVectorFrame(t)
	payload := frame[HeaderSize : len(frame)-CRCSize]
	if payload[0] != Codec8E {
		t.Fatalf("payload starts with 0x%02X, want the codec id 0x8E", payload[0])
	}
	if payload[1] != payload[len(payload)-1] {
		t.Fatalf("payload bounds are %d and %d, want both to be the record count", payload[1], payload[len(payload)-1])
	}
	if n := int(binary.BigEndian.Uint32(frame[4:8])); n != len(payload) {
		t.Fatalf("declared data length %d, payload between codec id and number of data 2 is %d", n, len(payload))
	}
}
