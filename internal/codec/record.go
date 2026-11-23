package codec

import (
	"encoding/binary"
	"fmt"
	"math"
	"time"
)

type GPS struct {
	Longitude  float64
	Latitude   float64
	Altitude   int16
	Angle      uint16
	Satellites uint8
	Speed      uint16
}

func (g GPS) HasFix() bool { return g.Satellites > 0 }

type Record struct {
	Timestamp time.Time
	Priority  uint8
	GPS       GPS
	IO        IO
}

func scaled(deg float64, limit float64, name string) (int32, error) {
	if math.IsNaN(deg) || math.IsInf(deg, 0) || deg < -limit || deg > limit {
		return 0, fmt.Errorf("%s %v is outside [-%v, %v]", name, deg, limit, limit)
	}
	return int32(math.Round(deg * 1e7)), nil
}

func degrees(raw int32) float64 { return float64(raw) / 1e7 }

func appendRecord(dst []byte, codecID byte, r Record) ([]byte, error) {
	lon, err := scaled(r.GPS.Longitude, 180, "longitude")
	if err != nil {
		return nil, err
	}
	lat, err := scaled(r.GPS.Latitude, 90, "latitude")
	if err != nil {
		return nil, err
	}
	if r.Priority > PriorityPanic {
		return nil, fmt.Errorf("priority %d is outside [0, 2]", r.Priority)
	}
	dst = binary.BigEndian.AppendUint64(dst, uint64(r.Timestamp.UnixMilli()))
	dst = append(dst, r.Priority)
	dst = binary.BigEndian.AppendUint32(dst, uint32(lon))
	dst = binary.BigEndian.AppendUint32(dst, uint32(lat))
	dst = binary.BigEndian.AppendUint16(dst, uint16(r.GPS.Altitude))
	dst = binary.BigEndian.AppendUint16(dst, r.GPS.Angle)
	dst = append(dst, r.GPS.Satellites)
	dst = binary.BigEndian.AppendUint16(dst, r.GPS.Speed)
	switch codecID {
	case Codec8:
		return appendIO8(dst, r.IO)
	case Codec8E:
		return appendIO8E(dst, r.IO)
	}
	return nil, fmt.Errorf("codec 0x%02X does not carry avl records", codecID)
}

func decodeRecord(c *cursor, codecID byte) Record {
	var r Record
	r.Timestamp = time.UnixMilli(int64(c.u64())).UTC()
	r.Priority = c.u8()
	r.GPS.Longitude = degrees(int32(c.u32()))
	r.GPS.Latitude = degrees(int32(c.u32()))
	r.GPS.Altitude = int16(c.u16())
	r.GPS.Angle = c.u16()
	r.GPS.Satellites = c.u8()
	r.GPS.Speed = c.u16()
	switch codecID {
	case Codec8:
		r.IO = decodeIO8(c)
	case Codec8E:
		r.IO = decodeIO8E(c)
	default:
		c.fail("codec 0x%02X does not carry avl records", codecID)
	}
	return r
}
