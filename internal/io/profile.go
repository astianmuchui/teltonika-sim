package avlio

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
)

type ProfileElement struct {
	ID  uint16 `json:"id"`
	Min uint64 `json:"min"`
	Max uint64 `json:"max"`
}

type ProfileBeacon struct {
	ID       uint16 `json:"id"`
	MinBytes int    `json:"min_bytes"`
	MaxBytes int    `json:"max_bytes"`
}

type Profile struct {
	Replace  bool             `json:"replace"`
	Drop     []uint16         `json:"drop"`
	Elements []ProfileElement `json:"elements"`
	Beacon   *ProfileBeacon   `json:"beacon"`
}

func loadProfile(path string) (*Profile, error) {
	if path == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var p Profile
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if p.Replace && len(p.Elements) == 0 {
		return nil, fmt.Errorf("%s: replace is set but no elements are listed", path)
	}
	return &p, nil
}
