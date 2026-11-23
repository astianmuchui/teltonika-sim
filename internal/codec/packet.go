package codec

import (
	"errors"
	"fmt"
)

type Packet struct {
	CodecID byte
	Records []Record
	Command *Command
	CRC     uint16
}

func (p *Packet) ACK() []byte { return EncodeACK(uint32(len(p.Records))) }

func EncodeAVL(codecID byte, records []Record) ([]byte, error) {
	payload, err := avlPayload(codecID, records)
	if err != nil {
		return nil, err
	}
	return Frame(payload), nil
}

func avlPayload(codecID byte, records []Record) ([]byte, error) {
	if codecID != Codec8 && codecID != Codec8E {
		return nil, fmt.Errorf("codec 0x%02X does not carry avl records", codecID)
	}
	if len(records) == 0 {
		return nil, errors.New("an avl packet needs at least one record")
	}
	if len(records) > 0xFF {
		return nil, fmt.Errorf("an avl packet carries at most 255 records, got %d", len(records))
	}
	payload := make([]byte, 0, 2+len(records)*96)
	payload = append(payload, codecID, byte(len(records)))
	var err error
	for i := range records {
		if payload, err = appendRecord(payload, codecID, records[i]); err != nil {
			return nil, fmt.Errorf("record %d: %w", i, err)
		}
	}
	return append(payload, byte(len(records))), nil
}

func Decode(frame []byte) (*Packet, error) {
	p, n, err := DecodeNext(frame)
	if err != nil {
		return nil, err
	}
	if n != len(frame) {
		return nil, fmt.Errorf("%w: %d bytes", ErrTrailingData, len(frame)-n)
	}
	return p, nil
}

func DecodeNext(b []byte) (*Packet, int, error) {
	payload, size, err := Unframe(b)
	if err != nil {
		return nil, size, err
	}
	p, err := DecodePayload(payload)
	if err != nil {
		return nil, size, err
	}
	p.CRC = CRC16(payload)
	return p, size, nil
}

func DecodePayload(payload []byte) (*Packet, error) {
	c := &cursor{b: payload}
	p := &Packet{CodecID: c.u8()}
	switch p.CodecID {
	case Codec8, Codec8E:
		n := int(c.u8())
		if c.err == nil && n == 0 {
			return nil, errors.New("avl packet declares zero records")
		}
		for range n {
			if c.err != nil {
				break
			}
			p.Records = append(p.Records, decodeRecord(c, p.CodecID))
		}
		if m := int(c.u8()); c.err == nil && m != n {
			return nil, fmt.Errorf("record count mismatch: header %d, trailer %d", n, m)
		}
	case Codec12:
		cmd := decodeCommand(c)
		p.Command = &cmd
	default:
		return nil, fmt.Errorf("unsupported codec id 0x%02X", p.CodecID)
	}
	if c.err != nil {
		return nil, c.err
	}
	if c.left() != 0 {
		return nil, fmt.Errorf("%d unread bytes at the end of the payload", c.left())
	}
	return p, nil
}
