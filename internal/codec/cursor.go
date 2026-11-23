package codec

import (
	"encoding/binary"
	"fmt"
)

type cursor struct {
	b   []byte
	i   int
	err error
}

func (c *cursor) fail(format string, args ...any) {
	if c.err == nil {
		c.err = fmt.Errorf(format, args...)
	}
}

func (c *cursor) take(n int) []byte {
	if n < 0 {
		c.fail("negative read length %d at offset %d", n, c.i)
		return nil
	}
	if c.err != nil || c.i+n > len(c.b) {
		if c.err == nil {
			c.fail("truncated payload: want %d bytes at offset %d of %d", n, c.i, len(c.b))
		}
		return make([]byte, n)
	}
	v := c.b[c.i : c.i+n]
	c.i += n
	return v
}

func (c *cursor) clone(n int) []byte {
	v := c.take(n)
	out := make([]byte, len(v))
	copy(out, v)
	return out
}

func (c *cursor) u8() uint8   { return c.take(1)[0] }
func (c *cursor) u16() uint16 { return binary.BigEndian.Uint16(c.take(2)) }
func (c *cursor) u32() uint32 { return binary.BigEndian.Uint32(c.take(4)) }
func (c *cursor) u64() uint64 { return binary.BigEndian.Uint64(c.take(8)) }
func (c *cursor) left() int   { return len(c.b) - c.i }
