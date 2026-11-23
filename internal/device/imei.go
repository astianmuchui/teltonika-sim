package device

import (
	"fmt"
	"strings"
)

const IMEILength = 15

func LuhnCheckDigit(body string) (byte, error) {
	if body == "" {
		return 0, fmt.Errorf("cannot compute a check digit for an empty string")
	}
	sum := 0
	double := true
	for i := len(body) - 1; i >= 0; i-- {
		c := body[i]
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("%q is not a digit string: byte %d is %q", body, i, c)
		}
		d := int(c - '0')
		if double {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		sum += d
		double = !double
	}
	return byte('0' + (10-sum%10)%10), nil
}

func ValidIMEI(imei string) bool {
	if len(imei) != IMEILength {
		return false
	}
	check, err := LuhnCheckDigit(imei[:IMEILength-1])
	if err != nil {
		return false
	}
	return check == imei[IMEILength-1]
}

func GenerateIMEI(prefix string, index int) (string, error) {
	if index < 0 {
		return "", fmt.Errorf("device index %d is negative", index)
	}
	if len(prefix) > IMEILength-1 {
		return "", fmt.Errorf("imei prefix %q is %d digits, at most %d fit", prefix, len(prefix), IMEILength-1)
	}
	for i := 0; i < len(prefix); i++ {
		if prefix[i] < '0' || prefix[i] > '9' {
			return "", fmt.Errorf("imei prefix %q is not all digits", prefix)
		}
	}
	width := IMEILength - 1 - len(prefix)
	body := prefix + fmt.Sprintf("%0*d", width, index)
	if len(body) != IMEILength-1 {
		return "", fmt.Errorf("device index %d needs more than the %d digits left by prefix %q", index, width, prefix)
	}
	check, err := LuhnCheckDigit(body)
	if err != nil {
		return "", err
	}
	return body + string(check), nil
}

func GenerateIMEIs(prefix string, count int) ([]string, error) {
	if count < 1 {
		return nil, fmt.Errorf("device count %d is not positive", count)
	}
	out := make([]string, 0, count)
	seen := make(map[string]bool, count)
	for i := range count {
		imei, err := GenerateIMEI(prefix, i)
		if err != nil {
			return nil, err
		}
		if seen[imei] {
			return nil, fmt.Errorf("prefix %q produced the duplicate imei %s", prefix, imei)
		}
		seen[imei] = true
		out = append(out, imei)
	}
	return out, nil
}

func NormalizeIMEI(imei string) (string, error) {
	imei = strings.TrimSpace(imei)
	if len(imei) != IMEILength {
		return "", fmt.Errorf("imei %q is %d digits, want %d", imei, len(imei), IMEILength)
	}
	if _, err := LuhnCheckDigit(imei[:IMEILength-1]); err != nil {
		return "", err
	}
	return imei, nil
}
