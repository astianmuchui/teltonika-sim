package codec

import (
	"encoding/binary"
	"fmt"
)

const (
	CommandRequest  byte = 0x05
	CommandResponse byte = 0x06
	CommandRejected byte = 0x11
)

type Command struct {
	Type     byte
	Quantity byte
	Text     string
}

func (c Command) IsRequest() bool { return c.Type == CommandRequest }

func (c Command) TypeName() string {
	switch c.Type {
	case CommandRequest:
		return "command"
	case CommandResponse:
		return "response"
	case CommandRejected:
		return "not executed"
	}
	return fmt.Sprintf("unknown(0x%02X)", c.Type)
}

func EncodeCommand(text string) ([]byte, error) { return encodeCodec12(CommandRequest, text) }

func EncodeResponse(text string) ([]byte, error) { return encodeCodec12(CommandResponse, text) }

func encodeCodec12(kind byte, text string) ([]byte, error) {
	if text == "" {
		return nil, fmt.Errorf("codec 12 %s is empty", Command{Type: kind}.TypeName())
	}
	if len(text) > MaxPayload-16 {
		return nil, fmt.Errorf("codec 12 text is %d bytes, too long for one frame", len(text))
	}
	payload := make([]byte, 0, 11+len(text))
	payload = append(payload, Codec12, 0x01, kind)
	payload = binary.BigEndian.AppendUint32(payload, uint32(len(text)))
	payload = append(payload, text...)
	return Frame(append(payload, 0x01)), nil
}

func decodeCommand(c *cursor) Command {
	var cmd Command
	cmd.Quantity = c.u8()
	if c.err == nil && cmd.Quantity != 1 {
		c.fail("codec 12 frame declares %d messages, want 1", cmd.Quantity)
		return cmd
	}
	cmd.Type = c.u8()
	n := int(c.u32())
	if c.err != nil {
		return cmd
	}
	if n < 0 || n > c.left() {
		c.fail("codec 12 text declares %d bytes, %d left in the payload", n, c.left())
		return cmd
	}
	cmd.Text = string(c.take(n))
	if q := c.u8(); c.err == nil && q != cmd.Quantity {
		c.fail("codec 12 quantity mismatch: header %d, trailer %d", cmd.Quantity, q)
	}
	return cmd
}
