package route

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRandomWalkStaysWithinRadius(t *testing.T) {
	cfg := WalkConfig{CenterLat: -1.2864, CenterLon: 36.8172, RadiusKM: 5, MaxSpeedKPH: 60, BaseAltitude: 1670}
	const step = 10 * time.Second
	const steps = 8640

	for _, seed := range []uint64{1, 2, 99, 123456} {
		w := NewWalk(cfg, seed)
		radiusM := cfg.RadiusKM * 1000
		maxStepM := cfg.MaxSpeedKPH/3.6*step.Seconds()*1.05 + 1
		travelled := 0.0
		sawStop := false
		sawParked := false
		sawMotion := false
		prev := w.Current()

		for i := range steps {
			fix := w.Advance(step)
			from := DistanceM(cfg.CenterLat, cfg.CenterLon, fix.Latitude, fix.Longitude)
			if from > radiusM {
				t.Fatalf("seed %d step %d: %.1f m from the centre, radius is %.0f m", seed, i, from, radiusM)
			}
			if fix.SpeedKPH < 0 || fix.SpeedKPH > cfg.MaxSpeedKPH {
				t.Fatalf("seed %d step %d: speed %.1f km/h outside [0, %.0f]", seed, i, fix.SpeedKPH, cfg.MaxSpeedKPH)
			}
			if jump := DistanceM(prev.Latitude, prev.Longitude, fix.Latitude, fix.Longitude); jump > maxStepM {
				t.Fatalf("seed %d step %d: teleported %.1f m in %s, at most %.1f m is possible", seed, i, jump, step, maxStepM)
			}
			if fix.StepMeters > maxStepM {
				t.Fatalf("seed %d step %d: reported %.1f m of travel in %s", seed, i, fix.StepMeters, step)
			}
			if math.Abs(fix.Altitude-cfg.BaseAltitude) > walkAltitudeSwag+0.001 {
				t.Fatalf("seed %d step %d: altitude %.1f m drifted past %.0f m from the %.0f m base",
					seed, i, fix.Altitude, float64(walkAltitudeSwag), cfg.BaseAltitude)
			}
			if fix.Satellites < 8 || fix.Satellites > 14 {
				t.Fatalf("seed %d step %d: %d satellites, want 8 to 14", seed, i, fix.Satellites)
			}
			if fix.HDOP < 0.6 || fix.HDOP > 1.5 {
				t.Fatalf("seed %d step %d: hdop %.2f, want 0.6 to 1.5", seed, i, fix.HDOP)
			}
			if fix.Bearing < 0 || fix.Bearing >= 360 {
				t.Fatalf("seed %d step %d: bearing %.1f outside [0, 360)", seed, i, fix.Bearing)
			}
			if !fix.Valid {
				t.Fatalf("seed %d step %d: the walk reported no fix", seed, i)
			}
			switch {
			case fix.Moving:
				sawMotion = true
			case fix.Ignition:
				sawStop = true
			default:
				sawParked = true
			}
			travelled += fix.StepMeters
			prev = fix
		}

		if !sawMotion || !sawStop || !sawParked {
			t.Errorf("seed %d: over 24 h saw motion %v, stops %v, ignition-off parking %v, want all three",
				seed, sawMotion, sawStop, sawParked)
		}
		if travelled < 1000 {
			t.Errorf("seed %d: only travelled %.0f m in 24 h", seed, travelled)
		}
	}
}

func TestRandomWalkIsDeterministicPerSeed(t *testing.T) {
	cfg := WalkConfig{CenterLat: -1.2864, CenterLon: 36.8172, RadiusKM: 5, MaxSpeedKPH: 60, BaseAltitude: 1670}
	a, b := NewWalk(cfg, 42), NewWalk(cfg, 42)
	for i := range 500 {
		fa, fb := a.Advance(time.Second), b.Advance(time.Second)
		if fa != fb {
			t.Fatalf("step %d: same seed produced %+v then %+v", i, fa, fb)
		}
	}
	other := NewWalk(cfg, 43)
	for range 500 {
		other.Advance(time.Second)
	}
	if other.Current() == a.Current() {
		t.Error("two different seeds produced the same track")
	}
}

func writeTestGPX(t *testing.T, points int, metresApart float64, interval time.Duration, timed bool) string {
	t.Helper()
	const baseLat = -1.2864
	const baseLon = 36.8172
	const degPerMetre = 1.0 / 111320.0

	var b strings.Builder
	b.WriteString("<gpx version=\"1.1\" creator=\"teltonika-sim test\">\n")
	b.WriteString("<metadata><name>straight line north</name></metadata>\n<trk><trkseg>\n")
	start := time.Date(2026, 11, 24, 8, 0, 0, 0, time.UTC)
	for i := range points {
		lat := baseLat + float64(i)*metresApart*degPerMetre
		fmt.Fprintf(&b, "<trkpt lat=\"%.9f\" lon=\"%.9f\"><ele>1670</ele>", lat, baseLon)
		if timed {
			fmt.Fprintf(&b, "<time>%s</time>", start.Add(time.Duration(i)*interval).Format(time.RFC3339))
		}
		b.WriteString("</trkpt>\n")
	}
	b.WriteString("</trkseg></trk>\n</gpx>\n")

	path := filepath.Join(t.TempDir(), "track.gpx")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatalf("write gpx: %v", err)
	}
	return path
}

func TestGPXSpeedMatchesDistanceOverTime(t *testing.T) {
	const points = 61
	const metres = 100
	const interval = 10 * time.Second
	wantKPH := metres / interval.Seconds() * 3.6

	track, err := LoadGPX(writeTestGPX(t, points, metres, interval, true))
	if err != nil {
		t.Fatalf("load gpx: %v", err)
	}
	if track.Synthetic {
		t.Fatal("the track carries timestamps but was read as untimed")
	}
	if len(track.Points) != points {
		t.Fatalf("%d points, want %d", len(track.Points), points)
	}
	if got, want := track.Duration(), time.Duration(points-1)*interval; got != want {
		t.Fatalf("track duration %s, want %s", got, want)
	}

	for i, p := range track.Points[:points-1] {
		if math.Abs(p.DistanceM-metres) > 0.5 {
			t.Fatalf("segment %d spans %.2f m, want %.0f m", i, p.DistanceM, float64(metres))
		}
		derived := p.DistanceM / p.SegmentDur.Seconds() * 3.6
		if math.Abs(p.SpeedKPH-derived) > 1e-9 {
			t.Errorf("segment %d speed %.6f km/h, distance over time gives %.6f km/h", i, p.SpeedKPH, derived)
		}
		if math.Abs(p.SpeedKPH-wantKPH) > 0.5 {
			t.Errorf("segment %d speed %.3f km/h, want %.3f km/h for %.0f m in %s", i, p.SpeedKPH, wantKPH, float64(metres), interval)
		}
		if math.Abs(angleDelta(p.Bearing, 0)) > 1 {
			t.Errorf("segment %d bearing %.3f, want 0 for a line heading north", i, p.Bearing)
		}
	}

	player := NewPlayer(track, false, 0, 7)
	travelled := 0.0
	for i := range points - 2 {
		fix := player.Advance(interval)
		travelled += fix.StepMeters
		if math.Abs(fix.SpeedKPH-wantKPH) > 0.5 {
			t.Fatalf("playback step %d reported %.3f km/h, want %.3f km/h", i, fix.SpeedKPH, wantKPH)
		}
		if !fix.Moving {
			t.Fatalf("playback step %d reported no movement at %.1f km/h", i, fix.SpeedKPH)
		}
	}
	travelled += player.Advance(interval).StepMeters
	if rel := math.Abs(travelled-track.LengthM()) / track.LengthM(); rel > 0.01 {
		t.Errorf("playback travelled %.1f m over a %.1f m track, %.2f%% off", travelled, track.LengthM(), rel*100)
	}

	end := player.Advance(interval)
	if end.SpeedKPH != 0 || end.Moving || end.Ignition {
		t.Errorf("past the end of the track: speed %.1f moving %v ignition %v, want a parked device",
			end.SpeedKPH, end.Moving, end.Ignition)
	}
}

func TestGPXPlaybackLoops(t *testing.T) {
	track, err := LoadGPX(writeTestGPX(t, 11, 100, 10*time.Second, true))
	if err != nil {
		t.Fatalf("load gpx: %v", err)
	}
	player := NewPlayer(track, true, 0, 7)
	for range 40 {
		fix := player.Advance(10 * time.Second)
		if !fix.Ignition {
			t.Fatal("a looping track switched the ignition off")
		}
		if fix.StepMeters > 200 {
			t.Fatalf("looping produced a %.1f m jump", fix.StepMeters)
		}
	}
	if player.Elapsed() >= track.Duration() {
		t.Fatalf("elapsed %s did not wrap within the %s track", player.Elapsed(), track.Duration())
	}
}

func TestGPXWithoutTimestampsUsesCruisingSpeed(t *testing.T) {
	track, err := LoadGPX(writeTestGPX(t, 11, 100, 10*time.Second, false))
	if err != nil {
		t.Fatalf("load gpx: %v", err)
	}
	if !track.Synthetic {
		t.Fatal("a track without timestamps was reported as timed")
	}
	for i, p := range track.Points[:len(track.Points)-1] {
		if math.Abs(p.SpeedKPH-gpxDefaultSpeedKPH) > 0.5 {
			t.Errorf("segment %d speed %.2f km/h, want the %d km/h default", i, p.SpeedKPH, gpxDefaultSpeedKPH)
		}
	}
}

func TestParseGPXRejectsUnusableFiles(t *testing.T) {
	if _, err := ParseGPX(strings.NewReader("<gpx><trk><trkseg></trkseg></trk></gpx>")); err == nil {
		t.Error("an empty track was accepted")
	}
	if _, err := ParseGPX(strings.NewReader("not xml at all")); err == nil {
		t.Error("a file that is not xml was accepted")
	}
	if _, err := ParseGPX(strings.NewReader(
		"<gpx><trk><trkseg><trkpt lat=\"1\" lon=\"2\"/><trkpt lat=\"999\" lon=\"2\"/></trkseg></trk></gpx>")); err == nil {
		t.Error("a point off the planet was accepted")
	}
}

func TestFactoryStaggersDevicesAlongTrack(t *testing.T) {
	path := writeTestGPX(t, 61, 100, 10*time.Second, true)
	f, err := NewFactory(Config{GPXPath: path, Devices: 4})
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	seen := make(map[time.Duration]bool)
	for i := range 4 {
		p, ok := f.New(i).(*Player)
		if !ok {
			t.Fatalf("device %d did not get a gpx player", i)
		}
		if seen[p.Elapsed()] {
			t.Errorf("device %d starts at %s, where another device already starts", i, p.Elapsed())
		}
		seen[p.Elapsed()] = true
	}

	walkFactory, err := NewFactory(DefaultConfig())
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	if _, ok := walkFactory.New(0).(*Walk); !ok {
		t.Error("no gpx path should produce a random walk")
	}
	if _, err := NewFactory(Config{RadiusKM: 0}); err == nil {
		t.Error("a zero radius was accepted")
	}
}

func TestGeoHelpers(t *testing.T) {
	lat, lon := Destination(-1.2864, 36.8172, 90, 1000)
	if d := DistanceM(-1.2864, 36.8172, lat, lon); math.Abs(d-1000) > 0.5 {
		t.Errorf("1000 m east then measured back gives %.2f m", d)
	}
	if b := BearingDeg(-1.2864, 36.8172, lat, lon); math.Abs(angleDelta(b, 90)) > 0.1 {
		t.Errorf("bearing %.3f, want 90", b)
	}
	if d := DistanceM(0, 0, 0, 0); d != 0 {
		t.Errorf("distance to the same point is %v", d)
	}
	for _, c := range []struct{ from, to, want float64 }{
		{350, 10, 20}, {10, 350, -20}, {0, 180, 180}, {90, 270, 180},
	} {
		if got := angleDelta(c.from, c.to); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("angleDelta(%v, %v) = %v, want %v", c.from, c.to, got, c.want)
		}
	}
}
