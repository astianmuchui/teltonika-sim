package route

import (
	"fmt"
	"math"
	"math/rand/v2"
	"time"
)

type Fix struct {
	Latitude   float64
	Longitude  float64
	Altitude   float64
	Bearing    float64
	SpeedKPH   float64
	Satellites uint8
	HDOP       float64
	PDOP       float64
	Valid      bool
	Moving     bool
	Ignition   bool
	StepMeters float64
}

type Source interface {
	Advance(d time.Duration) Fix
	Current() Fix
	Describe() string
}

type Config struct {
	GPXPath      string
	Loop         bool
	CenterLat    float64
	CenterLon    float64
	RadiusKM     float64
	MaxSpeedKPH  float64
	BaseAltitude float64
	Seed         uint64
	Devices      int
}

func DefaultConfig() Config {
	return Config{
		CenterLat:    -1.2864,
		CenterLon:    36.8172,
		RadiusKM:     5,
		MaxSpeedKPH:  60,
		BaseAltitude: 1670,
		Devices:      1,
	}
}

type Factory struct {
	cfg   Config
	track *Track
}

func NewFactory(cfg Config) (*Factory, error) {
	if cfg.Devices < 1 {
		cfg.Devices = 1
	}
	if cfg.MaxSpeedKPH <= 0 {
		cfg.MaxSpeedKPH = 60
	}
	f := &Factory{cfg: cfg}
	if cfg.GPXPath != "" {
		track, err := LoadGPX(cfg.GPXPath)
		if err != nil {
			return nil, err
		}
		f.track = track
		return f, nil
	}
	if cfg.CenterLat < -90 || cfg.CenterLat > 90 || cfg.CenterLon < -180 || cfg.CenterLon > 180 {
		return nil, fmt.Errorf("centre %v,%v is not a position on earth", cfg.CenterLat, cfg.CenterLon)
	}
	if cfg.RadiusKM <= 0 || math.IsNaN(cfg.RadiusKM) {
		return nil, fmt.Errorf("radius %v km leaves nowhere to drive", cfg.RadiusKM)
	}
	return f, nil
}

func (f *Factory) Track() *Track { return f.track }

func (f *Factory) New(index int) Source {
	seed := f.cfg.Seed ^ (uint64(index+1) * 0x9E3779B97F4A7C15)
	if f.track != nil {
		var offset time.Duration
		if f.cfg.Devices > 1 {
			offset = time.Duration(float64(f.track.Duration()) * float64(index%f.cfg.Devices) / float64(f.cfg.Devices))
		}
		return NewPlayer(f.track, f.cfg.Loop, offset, seed)
	}
	return NewWalk(WalkConfig{
		CenterLat:    f.cfg.CenterLat,
		CenterLon:    f.cfg.CenterLon,
		RadiusKM:     f.cfg.RadiusKM,
		MaxSpeedKPH:  f.cfg.MaxSpeedKPH,
		BaseAltitude: f.cfg.BaseAltitude,
	}, seed)
}

func (f *Factory) Describe() string {
	if f.track != nil {
		return f.track.Describe(f.cfg.Loop)
	}
	return fmt.Sprintf("random walk around %.4f,%.4f within %.1f km up to %.0f km/h",
		f.cfg.CenterLat, f.cfg.CenterLon, f.cfg.RadiusKM, f.cfg.MaxSpeedKPH)
}

type quality struct {
	rng  *rand.Rand
	sats uint8
	hdop float64
	pdop float64
	left time.Duration
}

func newQuality(rng *rand.Rand) *quality {
	q := &quality{rng: rng}
	q.resample()
	return q
}

func (q *quality) resample() {
	q.sats = uint8(8 + q.rng.IntN(7))
	q.hdop = 0.6 + q.rng.Float64()*0.9
	q.pdop = clamp(q.hdop+0.2+q.rng.Float64()*0.6, 0.6, 3)
	q.left = time.Duration(20+q.rng.IntN(40)) * time.Second
}

func (q *quality) advance(d time.Duration) {
	q.left -= d
	if q.left <= 0 {
		q.resample()
	}
}
