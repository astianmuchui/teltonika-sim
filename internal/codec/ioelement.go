package codec

import (
	"encoding/binary"
	"fmt"
)

type IOElement struct {
	ID       uint16
	Value    []byte
	Variable bool
}

type IO struct {
	EventID  uint16
	Elements []IOElement
}

var fixedSizes = [4]int{1, 2, 4, 8}

func Elem8(id uint16, v uint8) IOElement {
	return IOElement{ID: id, Value: []byte{v}}
}

func Elem16(id uint16, v uint16) IOElement {
	return IOElement{ID: id, Value: binary.BigEndian.AppendUint16(nil, v)}
}

func Elem32(id uint16, v uint32) IOElement {
	return IOElement{ID: id, Value: binary.BigEndian.AppendUint32(nil, v)}
}

func Elem64(id uint16, v uint64) IOElement {
	return IOElement{ID: id, Value: binary.BigEndian.AppendUint64(nil, v)}
}

func ElemVar(id uint16, v []byte) IOElement {
	return IOElement{ID: id, Value: v, Variable: true}
}

func ElemSized(id uint16, size int, v uint64) (IOElement, error) {
	switch size {
	case 1:
		return Elem8(id, uint8(v)), nil
	case 2:
		return Elem16(id, uint16(v)), nil
	case 4:
		return Elem32(id, uint32(v)), nil
	case 8:
		return Elem64(id, v), nil
	}
	return IOElement{}, fmt.Errorf("io element %d has an unusable size of %d bytes", id, size)
}

func (e IOElement) Uint() uint64 {
	var v uint64
	for _, b := range e.Value {
		v = v<<8 | uint64(b)
	}
	return v
}

func (e IOElement) Int() int64 {
	switch len(e.Value) {
	case 1:
		return int64(int8(e.Value[0]))
	case 2:
		return int64(int16(binary.BigEndian.Uint16(e.Value)))
	case 4:
		return int64(int32(binary.BigEndian.Uint32(e.Value)))
	case 8:
		return int64(binary.BigEndian.Uint64(e.Value))
	}
	return int64(e.Uint())
}

func (s IO) Find(id uint16) (IOElement, bool) {
	for _, e := range s.Elements {
		if e.ID == id {
			return e, true
		}
	}
	return IOElement{}, false
}

type ioGroups struct {
	fixed    [4][]IOElement
	variable []IOElement
}

func (g ioGroups) total() int {
	n := len(g.variable)
	for _, grp := range g.fixed {
		n += len(grp)
	}
	return n
}

func splitIO(s IO) (ioGroups, error) {
	var g ioGroups
	for _, e := range s.Elements {
		if e.Variable {
			if len(e.Value) > 0xFFFF {
				return g, fmt.Errorf("io element %d carries %d bytes, max 65535", e.ID, len(e.Value))
			}
			g.variable = append(g.variable, e)
			continue
		}
		slot := -1
		for i, size := range fixedSizes {
			if len(e.Value) == size {
				slot = i
			}
		}
		if slot < 0 {
			return g, fmt.Errorf("io element %d has a %d byte value, want 1, 2, 4 or 8", e.ID, len(e.Value))
		}
		g.fixed[slot] = append(g.fixed[slot], e)
	}
	return g, nil
}

func appendIO8(dst []byte, s IO) ([]byte, error) {
	g, err := splitIO(s)
	if err != nil {
		return nil, err
	}
	if len(g.variable) > 0 {
		return nil, fmt.Errorf("codec 8 cannot carry %d variable length io elements, use codec 8 extended", len(g.variable))
	}
	if s.EventID > 0xFF {
		return nil, fmt.Errorf("codec 8 event io id %d does not fit in one byte", s.EventID)
	}
	total := g.total()
	if total > 0xFF {
		return nil, fmt.Errorf("codec 8 record carries %d io elements, max 255", total)
	}
	dst = append(dst, byte(s.EventID), byte(total))
	for _, grp := range g.fixed {
		dst = append(dst, byte(len(grp)))
		for _, e := range grp {
			if e.ID > 0xFF {
				return nil, fmt.Errorf("codec 8 io id %d does not fit in one byte", e.ID)
			}
			dst = append(dst, byte(e.ID))
			dst = append(dst, e.Value...)
		}
	}
	return dst, nil
}

func appendIO8E(dst []byte, s IO) ([]byte, error) {
	g, err := splitIO(s)
	if err != nil {
		return nil, err
	}
	total := g.total()
	if total > 0xFFFF {
		return nil, fmt.Errorf("codec 8 extended record carries %d io elements, max 65535", total)
	}
	dst = binary.BigEndian.AppendUint16(dst, s.EventID)
	dst = binary.BigEndian.AppendUint16(dst, uint16(total))
	for _, grp := range g.fixed {
		dst = binary.BigEndian.AppendUint16(dst, uint16(len(grp)))
		for _, e := range grp {
			dst = binary.BigEndian.AppendUint16(dst, e.ID)
			dst = append(dst, e.Value...)
		}
	}
	dst = binary.BigEndian.AppendUint16(dst, uint16(len(g.variable)))
	for _, e := range g.variable {
		dst = binary.BigEndian.AppendUint16(dst, e.ID)
		dst = binary.BigEndian.AppendUint16(dst, uint16(len(e.Value)))
		dst = append(dst, e.Value...)
	}
	return dst, nil
}

func decodeIO8(c *cursor) IO {
	var s IO
	s.EventID = uint16(c.u8())
	total := int(c.u8())
	for _, size := range fixedSizes {
		n := int(c.u8())
		for range n {
			if c.err != nil {
				break
			}
			s.Elements = append(s.Elements, IOElement{ID: uint16(c.u8()), Value: c.clone(size)})
		}
	}
	if c.err == nil && len(s.Elements) != total {
		c.fail("io element count mismatch: header says %d, groups carry %d", total, len(s.Elements))
	}
	return s
}

func decodeIO8E(c *cursor) IO {
	var s IO
	s.EventID = c.u16()
	total := int(c.u16())
	for _, size := range fixedSizes {
		n := int(c.u16())
		for range n {
			if c.err != nil {
				break
			}
			s.Elements = append(s.Elements, IOElement{ID: c.u16(), Value: c.clone(size)})
		}
	}
	nx := int(c.u16())
	for range nx {
		if c.err != nil {
			break
		}
		id := c.u16()
		length := int(c.u16())
		if length > c.left() {
			c.fail("variable io element %d declares %d bytes, %d left in the payload", id, length, c.left())
			break
		}
		s.Elements = append(s.Elements, IOElement{ID: id, Value: c.clone(length), Variable: true})
	}
	if c.err == nil && len(s.Elements) != total {
		c.fail("io element count mismatch: header says %d, groups carry %d", total, len(s.Elements))
	}
	return s
}
