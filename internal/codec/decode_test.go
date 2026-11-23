package codec

import (
	"bytes"
	"encoding/hex"
	"math"
	"testing"

	lib "github.com/alim-zanibekov/teltonika"
)

func TestDecodeTestVector(t *testing.T) {
	p, err := Decode(testVectorFrame(t))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if p.CodecID != Codec8E {
		t.Fatalf("codec id 0x%02X, want 0x8E", p.CodecID)
	}
	if len(p.Records) != 2 {
		t.Fatalf("decoded %d records, want 2", len(p.Records))
	}
	if p.CRC != 0xBF30 {
		t.Fatalf("packet crc 0x%04X, want 0xBF30", p.CRC)
	}

	first := p.Records[0]
	if got := first.Timestamp.UnixMilli(); got != 1594898986000 {
		t.Errorf("record 0 timestamp %d ms, want 1594898986000", got)
	}
	if math.Abs(first.GPS.Latitude-54.667425) > 1e-7 {
		t.Errorf("record 0 latitude %.7f, want 54.667425", first.GPS.Latitude)
	}
	if math.Abs(first.GPS.Longitude-25.2560283) > 1e-7 {
		t.Errorf("record 0 longitude %.7f, want 25.2560283", first.GPS.Longitude)
	}
	if first.GPS.Altitude != 172 || first.GPS.Angle != 248 || first.GPS.Satellites != 11 || first.GPS.Speed != 0 {
		t.Errorf("record 0 gps = %+v, want altitude 172, angle 248, satellites 11, speed 0", first.GPS)
	}
	if first.Priority != PriorityLow {
		t.Errorf("record 0 priority %d, want 0", first.Priority)
	}
	if first.IO.EventID != 0 {
		t.Errorf("record 0 event io id %d, want 0", first.IO.EventID)
	}
	if len(first.IO.Elements) != 11 {
		t.Fatalf("record 0 carries %d io elements, want 11", len(first.IO.Elements))
	}
	for _, want := range []struct {
		id    uint16
		size  int
		value uint64
	}{
		{240, 1, 1}, {21, 1, 4}, {200, 1, 0}, {69, 1, 1}, {113, 1, 86},
		{181, 2, 5}, {182, 2, 4}, {24, 2, 0}, {67, 2, 4064}, {68, 2, 283},
		{241, 4, 24603},
	} {
		e, ok := first.IO.Find(want.id)
		if !ok {
			t.Errorf("record 0 is missing io element %d", want.id)
			continue
		}
		if len(e.Value) != want.size || e.Uint() != want.value || e.Variable {
			t.Errorf("record 0 io %d = %d bytes value %d variable %v, want %d bytes value %d fixed",
				want.id, len(e.Value), e.Uint(), e.Variable, want.size, want.value)
		}
	}

	second := p.Records[1]
	if got := second.Timestamp.UnixMilli(); got != 1594898988001 {
		t.Errorf("record 1 timestamp %d ms, want 1594898988001", got)
	}
	if second.IO.EventID != 385 {
		t.Errorf("record 1 event io id %d, want 385", second.IO.EventID)
	}
	if len(second.IO.Elements) != 1 {
		t.Fatalf("record 1 carries %d io elements, want 1", len(second.IO.Elements))
	}
	nx := second.IO.Elements[0]
	if nx.ID != 385 || !nx.Variable || len(nx.Value) != 45 {
		t.Errorf("record 1 io element = id %d variable %v %d bytes, want id 385 variable 45 bytes",
			nx.ID, nx.Variable, len(nx.Value))
	}

	if got, want := hex.EncodeToString(p.ACK()), "00000002"; got != want {
		t.Errorf("ack %s, want %s", got, want)
	}
}

func TestDecodeTestVectorMatchesLibrary(t *testing.T) {
	frame := testVectorFrame(t)
	ours, err := Decode(frame)
	if err != nil {
		t.Fatalf("our decoder: %v", err)
	}
	read, theirs, err := lib.DecodeTCPFromSlice(frame)
	if err != nil {
		t.Fatalf("library decoder: %v", err)
	}
	if read != len(frame) {
		t.Fatalf("library consumed %d of %d bytes", read, len(frame))
	}
	compareWithLibrary(t, ours.Records, theirs.Packet.Data)
	if !bytes.Equal(ours.ACK(), theirs.Response) {
		t.Errorf("ack %X, library expects %X", ours.ACK(), theirs.Response)
	}
}

func TestReencodeTestVector(t *testing.T) {
	frame := testVectorFrame(t)
	p, err := Decode(frame)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	out, err := EncodeAVL(p.CodecID, p.Records)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if !bytes.Equal(out, frame) {
		t.Fatalf("re-encoded packet differs\n got %s\nwant %s", hex.EncodeToString(out), hex.EncodeToString(frame))
	}
}

func TestDecodeRejectsCorruptFrames(t *testing.T) {
	good := testVectorFrame(t)

	badCRC := bytes.Clone(good)
	badCRC[len(badCRC)-1] ^= 0xFF
	if _, err := Decode(badCRC); err == nil {
		t.Error("a frame with a corrupt crc decoded without error")
	}

	badPreamble := bytes.Clone(good)
	badPreamble[0] = 0x01
	if _, err := Decode(badPreamble); err == nil {
		t.Error("a frame with a non-zero preamble decoded without error")
	}

	if _, err := Decode(good[:len(good)-5]); err == nil {
		t.Error("a truncated frame decoded without error")
	}

	trailing := append(bytes.Clone(good), 0x00)
	if _, err := Decode(trailing); err == nil {
		t.Error("a frame with trailing bytes decoded without error")
	}

	badCount := bytes.Clone(good)
	badCount[len(badCount)-5] = 0x07
	badCount = Frame(badCount[HeaderSize : len(badCount)-CRCSize])
	if _, err := Decode(badCount); err == nil {
		t.Error("a frame whose two record counts disagree decoded without error")
	}
}
