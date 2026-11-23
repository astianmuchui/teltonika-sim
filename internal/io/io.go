package avlio

import (
	"fmt"
	"math"
	"math/rand/v2"
	"slices"
	"strings"

	"github.com/alim-zanibekov/teltonika/ioelements"

	"github.com/astianmuchui/teltonika-sim/internal/codec"
	"github.com/astianmuchui/teltonika-sim/internal/route"
)

const (
	IgnitionEventID uint16 = 239
	BeaconID        uint16 = 385
	DefaultModel           = "FMB920"
	DefaultOperator uint32 = 63902
)

type Config struct {
	Model       string
	CodecID     byte
	WithNX      bool
	Operator    uint32
	ProfilePath string
}

func DefaultConfig() Config {
	return Config{Model: DefaultModel, CodecID: codec.Codec8E, Operator: DefaultOperator}
}

type valueFunc func(s *Sampler, fix route.Fix) uint64

type element struct {
	id    uint16
	size  int
	name  string
	value valueFunc
}

type Generator struct {
	cfg       Config
	elements  []element
	beacon    bool
	beaconID  uint16
	beaconMin int
	beaconMax int
}

type defaults struct {
	id    uint16
	value valueFunc
}

func builtins() []defaults {
	return []defaults{
		{239, func(s *Sampler, f route.Fix) uint64 { return boolValue(f.Ignition) }},
		{240, func(s *Sampler, f route.Fix) uint64 { return boolValue(f.Moving) }},
		{21, func(s *Sampler, f route.Fix) uint64 { return s.signal }},
		{200, func(s *Sampler, f route.Fix) uint64 { return boolValue(!f.Ignition) }},
		{69, func(s *Sampler, f route.Fix) uint64 {
			switch {
			case !f.Ignition:
				return 3
			case f.Valid:
				return 1
			default:
				return 2
			}
		}},
		{181, func(s *Sampler, f route.Fix) uint64 { return s.dop(181, f.PDOP) }},
		{182, func(s *Sampler, f route.Fix) uint64 { return s.dop(182, f.HDOP) }},
		{66, func(s *Sampler, f route.Fix) uint64 { return uint64(math.Round(s.externalMV)) }},
		{67, func(s *Sampler, f route.Fix) uint64 { return uint64(math.Round(s.batteryMV)) }},
		{68, func(s *Sampler, f route.Fix) uint64 { return uint64(math.Round(s.batteryMA)) }},
		{113, func(s *Sampler, f route.Fix) uint64 { return uint64(math.Round(s.batteryLevel)) }},
		{16, func(s *Sampler, f route.Fix) uint64 { return uint64(s.odometerM) }},
		{241, func(s *Sampler, f route.Fix) uint64 { return uint64(s.g.cfg.Operator) }},
		{205, func(s *Sampler, f route.Fix) uint64 { return s.cellID }},
		{206, func(s *Sampler, f route.Fix) uint64 { return s.areaCode }},
	}
}

func boolValue(b bool) uint64 {
	if b {
		return 1
	}
	return 0
}

func NewGenerator(cfg Config) (*Generator, error) {
	if cfg.Model == "" {
		cfg.Model = DefaultModel
	}
	if cfg.Operator == 0 {
		cfg.Operator = DefaultOperator
	}
	if cfg.CodecID != codec.Codec8 && cfg.CodecID != codec.Codec8E {
		return nil, fmt.Errorf("codec 0x%02X cannot carry io elements", cfg.CodecID)
	}
	if cfg.WithNX && cfg.CodecID != codec.Codec8E {
		return nil, fmt.Errorf("variable length io elements need codec 8e, not 0x%02X", cfg.CodecID)
	}

	profile, err := loadProfile(cfg.ProfilePath)
	if err != nil {
		return nil, err
	}

	decoder := ioelements.DefaultDecoder()
	g := &Generator{cfg: cfg, beacon: cfg.WithNX, beaconID: BeaconID, beaconMin: 12, beaconMax: 32}

	order := []uint16{}
	chosen := map[uint16]valueFunc{}
	if profile == nil || !profile.Replace {
		for _, d := range builtins() {
			order = append(order, d.id)
			chosen[d.id] = d.value
		}
	}
	if profile != nil {
		for _, pe := range profile.Elements {
			lo, hi := pe.Min, pe.Max
			if hi < lo {
				return nil, fmt.Errorf("io profile element %d has min %d above max %d", pe.ID, lo, hi)
			}
			if _, ok := chosen[pe.ID]; !ok {
				order = append(order, pe.ID)
			}
			chosen[pe.ID] = rangeValue(lo, hi)
		}
		for _, id := range profile.Drop {
			delete(chosen, id)
		}
		if profile.Beacon != nil {
			if !cfg.WithNX {
				return nil, fmt.Errorf("io profile configures a beacon but --with-nx is off")
			}
			if profile.Beacon.ID != 0 {
				g.beaconID = profile.Beacon.ID
			}
			if profile.Beacon.MinBytes > 0 {
				g.beaconMin = profile.Beacon.MinBytes
			}
			if profile.Beacon.MaxBytes > 0 {
				g.beaconMax = profile.Beacon.MaxBytes
			}
			if g.beaconMax < g.beaconMin {
				return nil, fmt.Errorf("io profile beacon has min_bytes %d above max_bytes %d", g.beaconMin, g.beaconMax)
			}
			if g.beaconMax > 0xFFFF {
				return nil, fmt.Errorf("io profile beacon max_bytes %d exceeds 65535", g.beaconMax)
			}
		}
	}

	for _, id := range order {
		value, ok := chosen[id]
		if !ok {
			continue
		}
		def, err := decoder.GetElementInfo(cfg.Model, id)
		if err != nil {
			return nil, fmt.Errorf("io element %d is not defined for %s: %w", id, cfg.Model, err)
		}
		if def.NumBytes != 1 && def.NumBytes != 2 && def.NumBytes != 4 && def.NumBytes != 8 {
			return nil, fmt.Errorf("io element %d (%s) is %d bytes wide on %s, which cannot sit in a fixed size group",
				id, def.Name, def.NumBytes, cfg.Model)
		}
		if cfg.CodecID == codec.Codec8 && id > 0xFF {
			return nil, fmt.Errorf("io element %d does not fit in the one byte ids of codec 8", id)
		}
		g.elements = append(g.elements, element{id: id, size: def.NumBytes, name: def.Name, value: value})
	}
	if len(g.elements) == 0 && !g.beacon {
		return nil, fmt.Errorf("the io profile left no elements to send")
	}

	if g.beacon {
		def, err := decoder.GetElementInfo(cfg.Model, g.beaconID)
		if err != nil {
			return nil, fmt.Errorf("io element %d is not defined for %s: %w", g.beaconID, cfg.Model, err)
		}
		if def.NumBytes >= 0 {
			return nil, fmt.Errorf("io element %d (%s) is a fixed %d byte element on %s, it cannot be sent as a variable length element",
				g.beaconID, def.Name, def.NumBytes, cfg.Model)
		}
	}
	return g, nil
}

func rangeValue(lo, hi uint64) valueFunc {
	return func(s *Sampler, f route.Fix) uint64 {
		if hi <= lo {
			return lo
		}
		return lo + s.rng.Uint64N(hi-lo+1)
	}
}

func (g *Generator) Describe() string {
	parts := make([]string, 0, len(g.elements)+1)
	for _, e := range g.elements {
		parts = append(parts, fmt.Sprintf("%d %s/%dB", e.id, e.name, e.size))
	}
	if g.beacon {
		parts = append(parts, fmt.Sprintf("%d beacon/%d-%dB", g.beaconID, g.beaconMin, g.beaconMax))
	}
	return strings.Join(parts, ", ")
}

func (g *Generator) IDs() []uint16 {
	ids := make([]uint16, 0, len(g.elements))
	for _, e := range g.elements {
		ids = append(ids, e.id)
	}
	slices.Sort(ids)
	return ids
}

type Sampler struct {
	g            *Generator
	rng          *rand.Rand
	odometerM    float64
	signal       uint64
	cellID       uint64
	areaCode     uint64
	externalMV   float64
	batteryMV    float64
	batteryMA    float64
	batteryLevel float64
	scales       map[uint16]float64
}

func (g *Generator) NewSampler(seed uint64) *Sampler {
	rng := rand.New(rand.NewPCG(seed, 0x14057B7EF767814F))
	s := &Sampler{
		g:            g,
		rng:          rng,
		signal:       uint64(3 + rng.IntN(3)),
		cellID:       uint64(rng.IntN(0x10000)),
		areaCode:     uint64(rng.IntN(0x10000)),
		externalMV:   12400,
		batteryMV:    3900 + rng.Float64()*200,
		batteryLevel: 92 + rng.Float64()*8,
		scales:       map[uint16]float64{},
	}
	decoder := ioelements.DefaultDecoder()
	for _, e := range g.elements {
		scale := 1.0
		if def, err := decoder.GetElementInfo(g.cfg.Model, e.id); err == nil && def.Multiplier > 0 && def.Multiplier < 1 {
			scale = 1 / def.Multiplier
		}
		s.scales[e.id] = scale
	}
	return s
}

func (s *Sampler) OdometerM() float64 { return s.odometerM }

func (s *Sampler) dop(id uint16, value float64) uint64 {
	scale := s.scales[id]
	if scale <= 0 {
		scale = 1
	}
	return uint64(math.Round(value * scale))
}

func (s *Sampler) advance(fix route.Fix) {
	s.odometerM += fix.StepMeters

	if s.rng.Float64() < 0.08 {
		step := int64(1)
		if s.rng.Float64() < 0.5 {
			step = -1
		}
		next := int64(s.signal) + step
		if next >= 1 && next <= 5 {
			s.signal = uint64(next)
		}
	}
	if s.rng.Float64() < 0.02 {
		s.cellID = uint64(s.rng.IntN(0x10000))
	}
	if s.rng.Float64() < 0.005 {
		s.areaCode = uint64(s.rng.IntN(0x10000))
	}

	if fix.Ignition {
		target := 13200 + (s.rng.Float64()*2-1)*1200
		s.externalMV += (target - s.externalMV) * 0.3
		s.externalMV = clamp(s.externalMV, 12000, 14400)
		s.batteryMA = s.rng.Float64() * 120
		s.batteryLevel = clamp(s.batteryLevel+s.rng.Float64()*0.4, 0, 100)
	} else {
		s.externalMV += (12400 - s.externalMV) * 0.3
		s.externalMV = clamp(s.externalMV, 12000, 14400)
		s.batteryMA = 0
		s.batteryLevel = clamp(s.batteryLevel-s.rng.Float64()*0.05, 40, 100)
	}
	s.batteryMV = clamp(s.batteryMV+(s.rng.Float64()*2-1)*20, 3900, 4100)
}

func (s *Sampler) Elements(fix route.Fix) ([]codec.IOElement, error) {
	s.advance(fix)
	out := make([]codec.IOElement, 0, len(s.g.elements)+1)
	for _, e := range s.g.elements {
		el, err := codec.ElemSized(e.id, e.size, e.value(s, fix))
		if err != nil {
			return nil, err
		}
		out = append(out, el)
	}
	if s.g.beacon {
		out = append(out, codec.ElemVar(s.g.beaconID, s.beaconPayload()))
	}
	return out, nil
}

func (s *Sampler) beaconPayload() []byte {
	span := s.g.beaconMax - s.g.beaconMin
	size := s.g.beaconMin
	if span > 0 {
		size += s.rng.IntN(span + 1)
	}
	payload := make([]byte, size)
	for i := range payload {
		payload[i] = byte(s.rng.UintN(256))
	}
	return payload
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func Lookup(model string, id uint16) *ioelements.IOElementDefinition {
	decoder := ioelements.DefaultDecoder()
	if def, err := decoder.GetElementInfo(model, id); err == nil {
		return def
	}
	if def, err := decoder.GetElementInfo("*", id); err == nil {
		return def
	}
	return nil
}
