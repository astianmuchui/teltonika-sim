package codec

import (
	"bytes"
	"encoding/hex"
	"math"
	"math/rand/v2"
	"testing"
	"time"

	lib "github.com/alim-zanibekov/teltonika"
)

const coordEpsilon = 1e-7

func compareRecords(t *testing.T, want, got []Record) {
	t.Helper()
	if len(want) != len(got) {
		t.Fatalf("decoded %d records, want %d", len(got), len(want))
	}
	for i := range want {
		w, g := want[i], got[i]
		if w.Timestamp.UnixMilli() != g.Timestamp.UnixMilli() {
			t.Errorf("record %d timestamp %d ms, want %d ms", i, g.Timestamp.UnixMilli(), w.Timestamp.UnixMilli())
		}
		if w.Priority != g.Priority {
			t.Errorf("record %d priority %d, want %d", i, g.Priority, w.Priority)
		}
		if math.Abs(w.GPS.Latitude-g.GPS.Latitude) > coordEpsilon {
			t.Errorf("record %d latitude %.9f, want %.9f", i, g.GPS.Latitude, w.GPS.Latitude)
		}
		if math.Abs(w.GPS.Longitude-g.GPS.Longitude) > coordEpsilon {
			t.Errorf("record %d longitude %.9f, want %.9f", i, g.GPS.Longitude, w.GPS.Longitude)
		}
		if w.GPS.Altitude != g.GPS.Altitude || w.GPS.Angle != g.GPS.Angle ||
			w.GPS.Satellites != g.GPS.Satellites || w.GPS.Speed != g.GPS.Speed {
			t.Errorf("record %d gps %+v, want %+v", i, g.GPS, w.GPS)
		}
		if w.IO.EventID != g.IO.EventID {
			t.Errorf("record %d event io id %d, want %d", i, g.IO.EventID, w.IO.EventID)
		}
		if len(w.IO.Elements) != len(g.IO.Elements) {
			t.Fatalf("record %d carries %d io elements, want %d", i, len(g.IO.Elements), len(w.IO.Elements))
		}
		for j := range w.IO.Elements {
			we, ge := w.IO.Elements[j], g.IO.Elements[j]
			if we.ID != ge.ID || we.Variable != ge.Variable || !bytes.Equal(we.Value, ge.Value) {
				t.Errorf("record %d io element %d = id %d variable %v value %s, want id %d variable %v value %s",
					i, j, ge.ID, ge.Variable, hex.EncodeToString(ge.Value),
					we.ID, we.Variable, hex.EncodeToString(we.Value))
			}
		}
	}
}

func compareWithLibrary(t *testing.T, want []Record, got []lib.Data) {
	t.Helper()
	if len(want) != len(got) {
		t.Fatalf("library decoded %d records, want %d", len(got), len(want))
	}
	for i := range want {
		w, g := want[i], got[i]
		if uint64(w.Timestamp.UnixMilli()) != g.TimestampMs {
			t.Errorf("record %d timestamp %d ms, library read %d ms", i, w.Timestamp.UnixMilli(), g.TimestampMs)
		}
		if w.Priority != g.Priority {
			t.Errorf("record %d priority %d, library read %d", i, w.Priority, g.Priority)
		}
		if math.Abs(w.GPS.Latitude-g.Lat) > coordEpsilon {
			t.Errorf("record %d latitude %.9f, library read %.9f", i, w.GPS.Latitude, g.Lat)
		}
		if math.Abs(w.GPS.Longitude-g.Lng) > coordEpsilon {
			t.Errorf("record %d longitude %.9f, library read %.9f", i, w.GPS.Longitude, g.Lng)
		}
		if w.GPS.Altitude != g.Altitude || w.GPS.Angle != g.Angle ||
			w.GPS.Satellites != g.Satellites || w.GPS.Speed != g.Speed {
			t.Errorf("record %d gps %+v, library read altitude %d angle %d satellites %d speed %d",
				i, w.GPS, g.Altitude, g.Angle, g.Satellites, g.Speed)
		}
		if w.IO.EventID != g.EventID {
			t.Errorf("record %d event io id %d, library read %d", i, w.IO.EventID, g.EventID)
		}
		if len(w.IO.Elements) != len(g.Elements) {
			t.Fatalf("record %d carries %d io elements, library read %d", i, len(w.IO.Elements), len(g.Elements))
		}
		for j := range w.IO.Elements {
			we, ge := w.IO.Elements[j], g.Elements[j]
			if we.ID != ge.Id || !bytes.Equal(we.Value, ge.Value) {
				t.Errorf("record %d io element %d = id %d value %s, library read id %d value %s",
					i, j, we.ID, hex.EncodeToString(we.Value), ge.Id, hex.EncodeToString(ge.Value))
			}
		}
	}
}

func randomBytes(rng *rand.Rand, n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(rng.UintN(256))
	}
	return b
}

func uniqueIDs(rng *rand.Rand, n, maxID int) []uint16 {
	seen := make(map[uint16]bool, n)
	out := make([]uint16, 0, n)
	for len(out) < n {
		id := uint16(1 + rng.IntN(maxID))
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

func randomRecord(t *testing.T, rng *rand.Rand, codecID byte, withNX bool) Record {
	t.Helper()
	r := Record{
		Timestamp: time.UnixMilli(1500000000000 + rng.Int64N(300000000000)).UTC(),
		Priority:  uint8(rng.IntN(3)),
		GPS: GPS{
			Longitude:  rng.Float64()*360 - 180,
			Latitude:   rng.Float64()*180 - 90,
			Altitude:   int16(rng.IntN(20000) - 10000),
			Angle:      uint16(rng.IntN(360)),
			Satellites: uint8(rng.IntN(16)),
			Speed:      uint16(rng.IntN(300)),
		},
	}
	maxID := 0xFFFF
	if codecID == Codec8 {
		maxID = 0xFF
		r.IO.EventID = uint16(rng.IntN(0x100))
	} else {
		r.IO.EventID = uint16(rng.IntN(0x10000))
	}
	ids := uniqueIDs(rng, 32, maxID)
	next := func() uint16 {
		id := ids[0]
		ids = ids[1:]
		return id
	}
	for _, size := range fixedSizes {
		for range rng.IntN(4) {
			e, err := ElemSized(next(), size, rng.Uint64())
			if err != nil {
				t.Fatalf("build io element: %v", err)
			}
			r.IO.Elements = append(r.IO.Elements, e)
		}
	}
	if withNX {
		for range 1 + rng.IntN(3) {
			r.IO.Elements = append(r.IO.Elements, ElemVar(next(), randomBytes(rng, rng.IntN(64))))
		}
	}
	return r
}

func TestRoundTripAgainstLibraryDecoder(t *testing.T) {
	cases := []struct {
		name    string
		codecID byte
		withNX  bool
	}{
		{"codec8", Codec8, false},
		{"codec8e", Codec8E, false},
		{"codec8e-with-nx", Codec8E, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rng := rand.New(rand.NewPCG(0x5EED, uint64(tc.codecID)))
			for iteration := range 300 {
				records := make([]Record, 1+rng.IntN(5))
				for i := range records {
					records[i] = randomRecord(t, rng, tc.codecID, tc.withNX)
				}
				raw, err := EncodeAVL(tc.codecID, records)
				if err != nil {
					t.Fatalf("iteration %d: encode: %v", iteration, err)
				}

				ours, err := Decode(raw)
				if err != nil {
					t.Fatalf("iteration %d: our decoder: %v\nframe %s", iteration, err, hex.EncodeToString(raw))
				}
				if ours.CodecID != tc.codecID {
					t.Fatalf("iteration %d: our decoder read codec 0x%02X, want 0x%02X", iteration, ours.CodecID, tc.codecID)
				}
				compareRecords(t, records, ours.Records)

				read, theirs, err := lib.DecodeTCPFromSlice(raw)
				if err != nil {
					t.Fatalf("iteration %d: library decoder: %v\nframe %s", iteration, err, hex.EncodeToString(raw))
				}
				if read != len(raw) {
					t.Fatalf("iteration %d: library consumed %d of %d bytes", iteration, read, len(raw))
				}
				if byte(theirs.Packet.CodecID) != tc.codecID {
					t.Fatalf("iteration %d: library read codec 0x%02X, want 0x%02X", iteration, byte(theirs.Packet.CodecID), tc.codecID)
				}
				compareWithLibrary(t, records, theirs.Packet.Data)
				if want := EncodeACK(uint32(len(records))); !bytes.Equal(theirs.Response, want) {
					t.Fatalf("iteration %d: library expects ack %X, want %X", iteration, theirs.Response, want)
				}

				again, err := EncodeAVL(tc.codecID, ours.Records)
				if err != nil {
					t.Fatalf("iteration %d: re-encode: %v", iteration, err)
				}
				if !bytes.Equal(again, raw) {
					t.Fatalf("iteration %d: re-encoding the decoded records changed the bytes\n got %s\nwant %s",
						iteration, hex.EncodeToString(again), hex.EncodeToString(raw))
				}
				if t.Failed() {
					t.Fatalf("iteration %d: see the failures above", iteration)
				}
			}
		})
	}
}
