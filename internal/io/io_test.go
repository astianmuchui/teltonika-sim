package avlio

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alim-zanibekov/teltonika/ioelements"

	"github.com/astianmuchui/teltonika-sim/internal/codec"
	"github.com/astianmuchui/teltonika-sim/internal/route"
)

func movingFix() route.Fix {
	return route.Fix{
		Latitude: -1.2864, Longitude: 36.8172, Altitude: 1670, Bearing: 90,
		SpeedKPH: 42, Satellites: 11, HDOP: 1.2, PDOP: 1.6,
		Valid: true, Moving: true, Ignition: true, StepMeters: 117,
	}
}

func TestElementSizesComeFromTheDefinitions(t *testing.T) {
	g, err := NewGenerator(DefaultConfig())
	if err != nil {
		t.Fatalf("new generator: %v", err)
	}
	decoder := ioelements.DefaultDecoder()
	sampler := g.NewSampler(1)
	elements, err := sampler.Elements(movingFix())
	if err != nil {
		t.Fatalf("sample: %v", err)
	}
	if len(elements) != 15 {
		t.Fatalf("generated %d elements, want the 15 fmb920 defaults", len(elements))
	}
	for _, e := range elements {
		def, err := decoder.GetElementInfo(DefaultModel, e.ID)
		if err != nil {
			t.Fatalf("io element %d is not defined for %s: %v", e.ID, DefaultModel, err)
		}
		if len(e.Value) != def.NumBytes {
			t.Errorf("io element %d (%s) encoded %d bytes, the definitions say %d",
				e.ID, def.Name, len(e.Value), def.NumBytes)
		}
	}
}

func TestGeneratedElementsSurviveBothCodecs(t *testing.T) {
	for _, tc := range []struct {
		name    string
		codecID byte
		withNX  bool
		want    int
	}{
		{"codec8", codec.Codec8, false, 15},
		{"codec8e", codec.Codec8E, false, 15},
		{"codec8e-with-nx", codec.Codec8E, true, 16},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.CodecID = tc.codecID
			cfg.WithNX = tc.withNX
			g, err := NewGenerator(cfg)
			if err != nil {
				t.Fatalf("new generator: %v", err)
			}
			elements, err := g.NewSampler(7).Elements(movingFix())
			if err != nil {
				t.Fatalf("sample: %v", err)
			}
			if len(elements) != tc.want {
				t.Fatalf("generated %d elements, want %d", len(elements), tc.want)
			}
			record := codec.Record{
				Timestamp: time.Now().UTC(),
				Priority:  codec.PriorityLow,
				GPS:       codec.GPS{Latitude: -1.2864, Longitude: 36.8172, Satellites: 11},
				IO:        codec.IO{Elements: elements},
			}
			raw, err := codec.EncodeAVL(tc.codecID, []codec.Record{record})
			if err != nil {
				t.Fatalf("encode: %v", err)
			}
			p, err := codec.Decode(raw)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if got := len(p.Records[0].IO.Elements); got != tc.want {
				t.Fatalf("decoded %d elements, want %d", got, tc.want)
			}
			if tc.withNX {
				last := p.Records[0].IO.Elements[tc.want-1]
				if last.ID != BeaconID || !last.Variable || len(last.Value) < 12 {
					t.Errorf("beacon element = id %d variable %v %d bytes, want id %d variable and at least 12 bytes",
						last.ID, last.Variable, len(last.Value), BeaconID)
				}
			}
		})
	}
}

func TestSamplerFollowsTheFix(t *testing.T) {
	g, err := NewGenerator(DefaultConfig())
	if err != nil {
		t.Fatalf("new generator: %v", err)
	}
	sampler := g.NewSampler(3)

	driving, err := sampler.Elements(movingFix())
	if err != nil {
		t.Fatalf("sample: %v", err)
	}
	set := codec.IO{Elements: driving}
	for _, want := range []struct {
		id    uint16
		value uint64
	}{{239, 1}, {240, 1}, {200, 0}, {69, 1}, {182, 12}, {181, 16}} {
		e, ok := set.Find(want.id)
		if !ok {
			t.Fatalf("io element %d missing", want.id)
		}
		if e.Uint() != want.value {
			t.Errorf("io element %d = %d, want %d", want.id, e.Uint(), want.value)
		}
	}
	if e, _ := set.Find(16); e.Uint() != 117 {
		t.Errorf("odometer %d m, want 117 m after a 117 m step", e.Uint())
	}
	if e, _ := set.Find(241); e.Uint() != uint64(DefaultOperator) {
		t.Errorf("gsm operator %d, want %d", e.Uint(), DefaultOperator)
	}
	if e, _ := set.Find(66); e.Uint() < 12000 || e.Uint() > 14400 {
		t.Errorf("external voltage %d mV outside 12000 to 14400 with the ignition on", e.Uint())
	}
	if e, _ := set.Find(67); e.Uint() < 3900 || e.Uint() > 4100 {
		t.Errorf("battery voltage %d mV outside 3900 to 4100", e.Uint())
	}
	if e, _ := set.Find(21); e.Uint() > 5 {
		t.Errorf("gsm signal %d, want 0 to 5", e.Uint())
	}
	if e, _ := set.Find(113); e.Uint() > 100 {
		t.Errorf("battery level %d%%", e.Uint())
	}

	parked := movingFix()
	parked.Ignition = false
	parked.Moving = false
	parked.SpeedKPH = 0
	parked.StepMeters = 0
	stopped, err := sampler.Elements(parked)
	if err != nil {
		t.Fatalf("sample: %v", err)
	}
	set = codec.IO{Elements: stopped}
	for _, want := range []struct {
		id    uint16
		value uint64
	}{{239, 0}, {240, 0}, {200, 1}, {69, 3}, {68, 0}} {
		e, ok := set.Find(want.id)
		if !ok {
			t.Fatalf("io element %d missing", want.id)
		}
		if e.Uint() != want.value {
			t.Errorf("parked io element %d = %d, want %d", want.id, e.Uint(), want.value)
		}
	}
	if e, _ := set.Find(16); e.Uint() != 117 {
		t.Errorf("odometer %d m, want it to hold at 117 m while parked", e.Uint())
	}

	for range 10 {
		if _, err := sampler.Elements(movingFix()); err != nil {
			t.Fatalf("sample: %v", err)
		}
	}
	if got := sampler.OdometerM(); got != 117*11 {
		t.Errorf("odometer %.0f m after eleven 117 m steps, want %d m", got, 117*11)
	}
}

func TestGeneratorRejectsUnusableConfigurations(t *testing.T) {
	cfg := DefaultConfig()
	cfg.CodecID = codec.Codec8
	cfg.WithNX = true
	if _, err := NewGenerator(cfg); err == nil {
		t.Error("--with-nx was accepted on codec 8")
	}

	cfg = DefaultConfig()
	cfg.CodecID = 0x10
	if _, err := NewGenerator(cfg); err == nil {
		t.Error("codec 16 was accepted")
	}

	cfg = DefaultConfig()
	cfg.Model = "NOT-A-DEVICE"
	if _, err := NewGenerator(cfg); err == nil {
		t.Error("an unknown device model was accepted")
	}
}

func writeProfile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "profile.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write profile: %v", err)
	}
	return path
}

func TestProfileOverridesRangesAndMembership(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ProfilePath = writeProfile(t, `{"drop": [205, 206], "elements": [{"id": 66, "min": 11800, "max": 11800}, {"id": 1, "min": 1, "max": 1}]}`)
	g, err := NewGenerator(cfg)
	if err != nil {
		t.Fatalf("new generator: %v", err)
	}
	elements, err := g.NewSampler(5).Elements(movingFix())
	if err != nil {
		t.Fatalf("sample: %v", err)
	}
	set := codec.IO{Elements: elements}
	if _, ok := set.Find(205); ok {
		t.Error("io element 205 was dropped by the profile but still sent")
	}
	if _, ok := set.Find(206); ok {
		t.Error("io element 206 was dropped by the profile but still sent")
	}
	if e, ok := set.Find(66); !ok || e.Uint() != 11800 {
		t.Errorf("io element 66 = %d, want the profile value 11800", e.Uint())
	}
	if e, ok := set.Find(1); !ok || e.Uint() != 1 || len(e.Value) != 1 {
		t.Errorf("io element 1 = %d in %d bytes, want 1 in 1 byte", e.Uint(), len(e.Value))
	}
	if len(elements) != 14 {
		t.Errorf("profile produced %d elements, want 15 defaults minus 2 dropped plus 1 added", len(elements))
	}
}

func TestProfileReplaceSendsOnlyListedElements(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ProfilePath = writeProfile(t, `{"replace": true, "elements": [{"id": 239, "min": 1, "max": 1}, {"id": 16, "min": 5000, "max": 5000}]}`)
	g, err := NewGenerator(cfg)
	if err != nil {
		t.Fatalf("new generator: %v", err)
	}
	elements, err := g.NewSampler(5).Elements(movingFix())
	if err != nil {
		t.Fatalf("sample: %v", err)
	}
	if len(elements) != 2 {
		t.Fatalf("replace profile produced %d elements, want 2", len(elements))
	}
	set := codec.IO{Elements: elements}
	if e, _ := set.Find(16); e.Uint() != 5000 {
		t.Errorf("odometer %d, want the profile value 5000", e.Uint())
	}
}

func TestProfileRejectsBadInput(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ProfilePath = writeProfile(t, `{"elements": [{"id": 9999, "min": 0, "max": 1}]}`)
	if _, err := NewGenerator(cfg); err == nil {
		t.Error("a profile naming an io element that is not in the definitions was accepted")
	}

	cfg.ProfilePath = writeProfile(t, `{"elements": [{"id": 66, "min": 500, "max": 10}]}`)
	if _, err := NewGenerator(cfg); err == nil {
		t.Error("a profile with min above max was accepted")
	}

	cfg.ProfilePath = writeProfile(t, `{"elements": [{"id": 385, "min": 0, "max": 1}]}`)
	if _, err := NewGenerator(cfg); err == nil {
		t.Error("the variable length beacon element was accepted as a fixed size element")
	}

	cfg.ProfilePath = writeProfile(t, `{"nonsense": true}`)
	if _, err := NewGenerator(cfg); err == nil {
		t.Error("a profile with an unknown field was accepted")
	}

	cfg.ProfilePath = filepath.Join(t.TempDir(), "missing.json")
	if _, err := NewGenerator(cfg); err == nil {
		t.Error("a missing profile file was accepted")
	}
}
