package route

import (
	"encoding/xml"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"sort"
	"time"
)

const (
	gpxDefaultSpeedKPH = 50
	gpxMinSegment      = time.Second
)

type Point struct {
	Latitude   float64
	Longitude  float64
	Altitude   float64
	Offset     time.Duration
	DistanceM  float64
	Bearing    float64
	SpeedKPH   float64
	SegmentDur time.Duration
}

type Track struct {
	Name      string
	Points    []Point
	Synthetic bool
	total     time.Duration
	length    float64
}

type gpxPoint struct {
	Lat  float64 `xml:"lat,attr"`
	Lon  float64 `xml:"lon,attr"`
	Ele  float64 `xml:"ele"`
	Time string  `xml:"time"`
}

type gpxSegment struct {
	Points []gpxPoint `xml:"trkpt"`
}

type gpxTrack struct {
	Name     string       `xml:"name"`
	Segments []gpxSegment `xml:"trkseg"`
}

type gpxRoute struct {
	Name   string     `xml:"name"`
	Points []gpxPoint `xml:"rtept"`
}

type gpxFile struct {
	MetaName string     `xml:"metadata>name"`
	Tracks   []gpxTrack `xml:"trk"`
	Routes   []gpxRoute `xml:"rte"`
}

func LoadGPX(path string) (*Track, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	track, err := ParseGPX(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if track.Name == "" {
		track.Name = path
	}
	return track, nil
}

func ParseGPX(r io.Reader) (*Track, error) {
	var doc gpxFile
	if err := xml.NewDecoder(r).Decode(&doc); err != nil {
		return nil, fmt.Errorf("not readable as gpx: %w", err)
	}
	track := &Track{Name: doc.MetaName}
	var raw []gpxPoint
	for _, t := range doc.Tracks {
		if track.Name == "" {
			track.Name = t.Name
		}
		for _, seg := range t.Segments {
			raw = append(raw, seg.Points...)
		}
	}
	if len(raw) == 0 {
		for _, rt := range doc.Routes {
			if track.Name == "" {
				track.Name = rt.Name
			}
			raw = append(raw, rt.Points...)
		}
	}
	if len(raw) < 2 {
		return nil, fmt.Errorf("a track needs at least 2 points, found %d", len(raw))
	}

	times := make([]time.Time, len(raw))
	timed := true
	for i, p := range raw {
		if p.Lat < -90 || p.Lat > 90 || p.Lon < -180 || p.Lon > 180 {
			return nil, fmt.Errorf("point %d at %v,%v is not a position on earth", i, p.Lat, p.Lon)
		}
		if p.Time == "" {
			timed = false
			continue
		}
		t, err := time.Parse(time.RFC3339, p.Time)
		if err != nil {
			timed = false
			continue
		}
		times[i] = t
	}
	if timed {
		for i := 1; i < len(times); i++ {
			if !times[i].After(times[i-1]) {
				timed = false
				break
			}
		}
	}
	track.Synthetic = !timed

	track.Points = make([]Point, len(raw))
	for i, p := range raw {
		track.Points[i] = Point{Latitude: p.Lat, Longitude: p.Lon, Altitude: p.Ele}
	}
	for i := range track.Points {
		if i+1 == len(track.Points) {
			break
		}
		a, b := &track.Points[i], &track.Points[i+1]
		a.DistanceM = DistanceM(a.Latitude, a.Longitude, b.Latitude, b.Longitude)
		a.Bearing = BearingDeg(a.Latitude, a.Longitude, b.Latitude, b.Longitude)
		if timed {
			a.SegmentDur = times[i+1].Sub(times[i])
		} else {
			a.SegmentDur = time.Duration(a.DistanceM / (gpxDefaultSpeedKPH / 3.6) * float64(time.Second))
		}
		if a.SegmentDur < gpxMinSegment {
			a.SegmentDur = gpxMinSegment
		}
		a.SpeedKPH = a.DistanceM / a.SegmentDur.Seconds() * 3.6
		b.Offset = a.Offset + a.SegmentDur
		track.length += a.DistanceM
	}
	last := &track.Points[len(track.Points)-1]
	last.Bearing = track.Points[len(track.Points)-2].Bearing
	track.total = last.Offset
	return track, nil
}

func (t *Track) Duration() time.Duration { return t.total }

func (t *Track) LengthM() float64 { return t.length }

func (t *Track) Describe(loop bool) string {
	tail := ""
	if loop {
		tail = " looping"
	}
	if t.Synthetic {
		tail += fmt.Sprintf(" (no usable timestamps, played at %d km/h)", gpxDefaultSpeedKPH)
	}
	return fmt.Sprintf("gpx %q: %d points, %.2f km, %s%s", t.Name, len(t.Points), t.length/1000, t.total, tail)
}

func (t *Track) At(offset time.Duration) Fix {
	if offset <= 0 {
		p := t.Points[0]
		return Fix{Latitude: p.Latitude, Longitude: p.Longitude, Altitude: p.Altitude,
			Bearing: p.Bearing, SpeedKPH: p.SpeedKPH}
	}
	if offset >= t.total {
		p := t.Points[len(t.Points)-1]
		return Fix{Latitude: p.Latitude, Longitude: p.Longitude, Altitude: p.Altitude, Bearing: p.Bearing}
	}
	i := sort.Search(len(t.Points), func(j int) bool { return t.Points[j].Offset > offset }) - 1
	if i < 0 {
		i = 0
	}
	a := t.Points[i]
	b := t.Points[i+1]
	frac := 0.0
	if a.SegmentDur > 0 {
		frac = (offset - a.Offset).Seconds() / a.SegmentDur.Seconds()
	}
	frac = clamp(frac, 0, 1)
	lat, lon := Destination(a.Latitude, a.Longitude, a.Bearing, a.DistanceM*frac)
	return Fix{
		Latitude:  lat,
		Longitude: lon,
		Altitude:  a.Altitude + (b.Altitude-a.Altitude)*frac,
		Bearing:   a.Bearing,
		SpeedKPH:  a.SpeedKPH,
	}
}

type Player struct {
	track   *Track
	loop    bool
	q       *quality
	elapsed time.Duration
	last    Fix
}

func NewPlayer(track *Track, loop bool, offset time.Duration, seed uint64) *Player {
	p := &Player{
		track:   track,
		loop:    loop,
		q:       newQuality(rand.New(rand.NewPCG(seed, 0x2545F4914F6CDD1D))),
		elapsed: offset,
	}
	p.last = p.sample()
	return p
}

func (p *Player) sample() Fix {
	fix := p.track.At(p.elapsed)
	fix.Satellites = p.q.sats
	fix.HDOP = p.q.hdop
	fix.PDOP = p.q.pdop
	fix.Valid = true
	fix.Moving = fix.SpeedKPH > 0
	fix.Ignition = p.loop || p.elapsed < p.track.total
	return fix
}

func (p *Player) Advance(d time.Duration) Fix {
	if d < 0 {
		d = 0
	}
	moved := 0.0
	for remaining := d; remaining > 0; {
		step := min(remaining, walkSubstep)
		remaining -= step
		before := p.last
		p.elapsed += step
		wrapped := false
		if p.loop && p.track.total > 0 {
			for p.elapsed >= p.track.total {
				p.elapsed -= p.track.total
				wrapped = true
			}
		}
		p.last = p.sample()
		if !wrapped {
			moved += DistanceM(before.Latitude, before.Longitude, p.last.Latitude, p.last.Longitude)
		}
	}
	p.q.advance(d)
	fix := p.sample()
	fix.StepMeters = moved
	p.last = fix
	return fix
}

func (p *Player) Current() Fix { return p.last }

func (p *Player) Elapsed() time.Duration { return p.elapsed }

func (p *Player) Describe() string { return p.track.Describe(p.loop) }
