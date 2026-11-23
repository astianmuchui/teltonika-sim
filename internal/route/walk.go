package route

import (
	"fmt"
	"math/rand/v2"
	"time"
)

const (
	walkSubstep      = time.Second
	walkMaxAccelKPHS = 6
	walkMaxTurnDegS  = 25
	walkAltitudeSwag = 20
	walkPullZone     = 0.8
)

type walkMode int

const (
	modeDriving walkMode = iota
	modeStopped
	modeParked
)

type WalkConfig struct {
	CenterLat    float64
	CenterLon    float64
	RadiusKM     float64
	MaxSpeedKPH  float64
	BaseAltitude float64
}

type Walk struct {
	cfg      WalkConfig
	rng      *rand.Rand
	q        *quality
	lat      float64
	lon      float64
	bearing  float64
	speed    float64
	target   float64
	altitude float64
	mode     walkMode
	modeLeft time.Duration
	nextTgt  time.Duration
}

func NewWalk(cfg WalkConfig, seed uint64) *Walk {
	if cfg.MaxSpeedKPH <= 0 {
		cfg.MaxSpeedKPH = 60
	}
	rng := rand.New(rand.NewPCG(seed, 0x2545F4914F6CDD1D))
	w := &Walk{
		cfg:      cfg,
		rng:      rng,
		q:        newQuality(rng),
		bearing:  rng.Float64() * 360,
		altitude: cfg.BaseAltitude,
		mode:     modeDriving,
	}
	w.lat, w.lon = Destination(cfg.CenterLat, cfg.CenterLon, rng.Float64()*360, rng.Float64()*cfg.RadiusKM*700)
	w.modeLeft = w.randDuration(2*time.Minute, 15*time.Minute)
	w.retarget()
	return w
}

func (w *Walk) randDuration(lo, hi time.Duration) time.Duration {
	return lo + time.Duration(w.rng.Int64N(int64(hi-lo)+1))
}

func (w *Walk) retarget() {
	w.target = 10 + w.rng.Float64()*(w.cfg.MaxSpeedKPH-10)
	w.nextTgt = w.randDuration(20*time.Second, 90*time.Second)
}

func (w *Walk) chooseMode() {
	switch w.mode {
	case modeDriving:
		if w.rng.Float64() < 0.2 {
			w.mode = modeParked
			w.modeLeft = w.randDuration(5*time.Minute, 20*time.Minute)
		} else {
			w.mode = modeStopped
			w.modeLeft = w.randDuration(30*time.Second, 180*time.Second)
		}
	default:
		w.mode = modeDriving
		w.modeLeft = w.randDuration(2*time.Minute, 15*time.Minute)
		w.retarget()
	}
}

func (w *Walk) Advance(d time.Duration) Fix {
	if d < 0 {
		d = 0
	}
	moved := 0.0
	for remaining := d; remaining > 0; {
		step := min(remaining, walkSubstep)
		moved += w.substep(step)
		remaining -= step
	}
	w.q.advance(d)
	fix := w.Current()
	fix.StepMeters = moved
	return fix
}

func (w *Walk) substep(d time.Duration) float64 {
	secs := d.Seconds()
	w.modeLeft -= d
	if w.modeLeft <= 0 {
		w.chooseMode()
	}
	w.altitude = clamp(w.altitude+(w.rng.Float64()*2-1)*0.3*secs,
		w.cfg.BaseAltitude-walkAltitudeSwag, w.cfg.BaseAltitude+walkAltitudeSwag)
	if w.mode != modeDriving {
		w.speed = 0
		return 0
	}
	w.nextTgt -= d
	if w.nextTgt <= 0 {
		w.retarget()
	}
	w.speed = clamp(w.speed+clamp(w.target-w.speed, -walkMaxAccelKPHS*secs, walkMaxAccelKPHS*secs), 0, w.cfg.MaxSpeedKPH)

	turn := (w.rng.Float64()*2 - 1) * walkMaxTurnDegS * secs
	radiusM := w.cfg.RadiusKM * 1000
	fromCenter := DistanceM(w.cfg.CenterLat, w.cfg.CenterLon, w.lat, w.lon)
	if fromCenter > radiusM*walkPullZone {
		toCenter := BearingDeg(w.lat, w.lon, w.cfg.CenterLat, w.cfg.CenterLon)
		pull := clamp((fromCenter/radiusM-walkPullZone)/(1-walkPullZone), 0, 1)
		turn += angleDelta(w.bearing, toCenter) * pull * 0.5
	}
	w.bearing = normalizeDeg(w.bearing + turn)

	dist := w.speed / 3.6 * secs
	lat, lon := Destination(w.lat, w.lon, w.bearing, dist)
	if DistanceM(w.cfg.CenterLat, w.cfg.CenterLon, lat, lon) > radiusM {
		w.bearing = normalizeDeg(BearingDeg(w.lat, w.lon, w.cfg.CenterLat, w.cfg.CenterLon) + w.rng.Float64()*60 - 30)
		w.speed *= 0.5
		return 0
	}
	w.lat, w.lon = lat, lon
	return dist
}

func (w *Walk) Current() Fix {
	return Fix{
		Latitude:   w.lat,
		Longitude:  w.lon,
		Altitude:   w.altitude,
		Bearing:    w.bearing,
		SpeedKPH:   w.speed,
		Satellites: w.q.sats,
		HDOP:       w.q.hdop,
		PDOP:       w.q.pdop,
		Valid:      true,
		Moving:     w.mode == modeDriving && w.speed > 0,
		Ignition:   w.mode != modeParked,
	}
}

func (w *Walk) Describe() string {
	return fmt.Sprintf("random walk around %.4f,%.4f within %.1f km up to %.0f km/h",
		w.cfg.CenterLat, w.cfg.CenterLon, w.cfg.RadiusKM, w.cfg.MaxSpeedKPH)
}
