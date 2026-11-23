package codec

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const (
	Codec8  byte = 0x08
	Codec8E byte = 0x8E
	Codec12 byte = 0x0C
)

const (
	PriorityLow   uint8 = 0
	PriorityHigh  uint8 = 1
	PriorityPanic uint8 = 2
)

const (
	HandshakeAccept byte = 0x01
	HandshakeReject byte = 0x00
)

const (
	MaxPayload    = 1 << 20
	MaxIMEILength = 64
	HeaderSize    = 8
	CRCSize       = 4
	ACKSize       = 4
)

var (
	ErrBadPreamble  = errors.New("bad preamble")
	ErrBadCRC       = errors.New("crc mismatch")
	ErrShortFrame   = errors.New("frame truncated")
	ErrPayloadSize  = errors.New("payload length out of range")
	ErrTrailingData = errors.New("trailing bytes after frame")
)

func Frame(payload []byte) []byte {
	out := make([]byte, 0, HeaderSize+len(payload)+CRCSize)
	out = append(out, 0x00, 0x00, 0x00, 0x00)
	out = binary.BigEndian.AppendUint32(out, uint32(len(payload)))
	out = append(out, payload...)
	return binary.BigEndian.AppendUint32(out, uint32(CRC16(payload)))
}

func Unframe(frame []byte) ([]byte, int, error) {
	if len(frame) < HeaderSize+CRCSize {
		return nil, 0, fmt.Errorf("%w: %d bytes is not a frame", ErrShortFrame, len(frame))
	}
	if binary.BigEndian.Uint32(frame[0:4]) != 0 {
		return nil, 0, fmt.Errorf("%w: %X", ErrBadPreamble, frame[0:4])
	}
	n := int(binary.BigEndian.Uint32(frame[4:8]))
	if n <= 0 || n > MaxPayload {
		return nil, 0, fmt.Errorf("%w: %d", ErrPayloadSize, n)
	}
	size := HeaderSize + n + CRCSize
	if len(frame) < size {
		return nil, 0, fmt.Errorf("%w: want %d bytes, have %d", ErrShortFrame, size, len(frame))
	}
	payload := frame[HeaderSize : HeaderSize+n]
	want := binary.BigEndian.Uint32(frame[HeaderSize+n : size])
	if got := uint32(CRC16(payload)); got != want {
		return payload, size, fmt.Errorf("%w: computed %08X, frame carries %08X", ErrBadCRC, got, want)
	}
	return payload, size, nil
}

func ReadFrame(r io.Reader) ([]byte, error) {
	var head [HeaderSize]byte
	if _, err := io.ReadFull(r, head[:]); err != nil {
		return nil, err
	}
	if binary.BigEndian.Uint32(head[0:4]) != 0 {
		return nil, fmt.Errorf("%w: %X", ErrBadPreamble, head[0:4])
	}
	n := int(binary.BigEndian.Uint32(head[4:8]))
	if n <= 0 || n > MaxPayload {
		return nil, fmt.Errorf("%w: %d", ErrPayloadSize, n)
	}
	frame := make([]byte, HeaderSize+n+CRCSize)
	copy(frame, head[:])
	if _, err := io.ReadFull(r, frame[HeaderSize:]); err != nil {
		return nil, err
	}
	return frame, nil
}

func EncodeIMEI(imei string) ([]byte, error) {
	if imei == "" || len(imei) > MaxIMEILength {
		return nil, fmt.Errorf("imei %q has an unusable length of %d", imei, len(imei))
	}
	out := make([]byte, 2+len(imei))
	binary.BigEndian.PutUint16(out, uint16(len(imei)))
	copy(out[2:], imei)
	return out, nil
}

func ReadIMEI(r io.Reader) (string, error) {
	var head [2]byte
	if _, err := io.ReadFull(r, head[:]); err != nil {
		return "", err
	}
	n := int(binary.BigEndian.Uint16(head[:]))
	if n == 0 || n > MaxIMEILength {
		return "", fmt.Errorf("handshake declares %d bytes of imei (0x%04X), refusing", n, n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", err
	}
	for i, ch := range buf {
		if ch < '0' || ch > '9' {
			return "", fmt.Errorf("imei byte %d is %q, want a digit", i, ch)
		}
	}
	return string(buf), nil
}

func EncodeACK(records uint32) []byte {
	out := make([]byte, ACKSize)
	binary.BigEndian.PutUint32(out, records)
	return out
}

func DecodeACK(b []byte) (uint32, error) {
	if len(b) != ACKSize {
		return 0, fmt.Errorf("ack is %d bytes, want %d", len(b), ACKSize)
	}
	return binary.BigEndian.Uint32(b), nil
}
