package device

import "testing"

func TestLuhnIMEIGenerator(t *testing.T) {
	const prefix = "35630704"
	imeis, err := GenerateIMEIs(prefix, 500)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(imeis) != 500 {
		t.Fatalf("generated %d imeis, want 500", len(imeis))
	}
	seen := make(map[string]bool, len(imeis))
	for i, imei := range imeis {
		if len(imei) != IMEILength {
			t.Fatalf("imei %d is %q, %d digits, want %d", i, imei, len(imei), IMEILength)
		}
		for j := 0; j < len(imei); j++ {
			if imei[j] < '0' || imei[j] > '9' {
				t.Fatalf("imei %q has a non digit at %d", imei, j)
			}
		}
		if imei[:len(prefix)] != prefix {
			t.Errorf("imei %q does not start with the prefix %q", imei, prefix)
		}
		if !ValidIMEI(imei) {
			t.Errorf("imei %q fails its own luhn check", imei)
		}
		if seen[imei] {
			t.Errorf("imei %q was generated twice", imei)
		}
		seen[imei] = true
	}
}

func TestLuhnAgainstKnownIMEIs(t *testing.T) {
	valid := []string{
		"356307042441013",
		"490154203237518",
		"352099001761481",
	}
	for _, imei := range valid {
		if !ValidIMEI(imei) {
			t.Errorf("%s is a valid imei but was rejected", imei)
		}
	}
	for _, imei := range []string{
		"356307042441014",
		"356307042441012",
		"49015420323751",
		"4901542032375188",
		"49015420323751x",
		"",
	} {
		if ValidIMEI(imei) {
			t.Errorf("%q is not a valid imei but was accepted", imei)
		}
	}
}

func TestLuhnCheckDigitMatchesTheKnownIMEI(t *testing.T) {
	got, err := LuhnCheckDigit("35630704244101")
	if err != nil {
		t.Fatalf("check digit: %v", err)
	}
	if got != '3' {
		t.Fatalf("check digit %q, want 3", got)
	}
	if _, err := LuhnCheckDigit("3563070424410x"); err == nil {
		t.Error("a body with a letter in it produced a check digit")
	}
	if _, err := LuhnCheckDigit(""); err == nil {
		t.Error("an empty body produced a check digit")
	}
}

func TestGenerateIMEIRejectsUnusableInput(t *testing.T) {
	if _, err := GenerateIMEI("3563070444444444", 0); err == nil {
		t.Error("a prefix longer than 14 digits was accepted")
	}
	if _, err := GenerateIMEI("3563abcd", 0); err == nil {
		t.Error("a prefix with letters was accepted")
	}
	if _, err := GenerateIMEI("35630704", 1000000); err == nil {
		t.Error("an index too large for the remaining digits was accepted")
	}
	if _, err := GenerateIMEI("35630704", -1); err == nil {
		t.Error("a negative index was accepted")
	}
	if _, err := GenerateIMEIs("35630704", 0); err == nil {
		t.Error("a zero device count was accepted")
	}
	imei, err := GenerateIMEI("", 7)
	if err != nil {
		t.Fatalf("empty prefix: %v", err)
	}
	if !ValidIMEI(imei) {
		t.Errorf("imei %q from an empty prefix fails its luhn check", imei)
	}
}

func TestNormalizeIMEI(t *testing.T) {
	got, err := NormalizeIMEI("  356307042441013  ")
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if got != "356307042441013" {
		t.Fatalf("normalized to %q", got)
	}
	if _, err := NormalizeIMEI("12345"); err == nil {
		t.Error("a short imei was accepted")
	}
}
