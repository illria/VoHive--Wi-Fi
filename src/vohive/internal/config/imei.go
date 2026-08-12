package config

import "strings"

// NormalizeIMEI returns the 14-digit TAC+SNR identity key used for IMEI equality.
// It is comparison-only: callers should keep storing and displaying the original
// full IMEI value.
func NormalizeIMEI(raw string) string {
	var b strings.Builder
	for _, r := range raw {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	digits := b.String()
	if len(digits) < 14 {
		return ""
	}
	return digits[:14]
}

// IsValidIMEI reports whether raw is one complete 15-digit IMEI with a valid
// Luhn check digit. Reader-only PC/SC lines use this to remain drafts until the
// operator supplies a usable terminal identity.
func IsValidIMEI(raw string) bool {
	raw = strings.TrimSpace(raw)
	if len(raw) != 15 {
		return false
	}
	for _, r := range raw {
		if r < '0' || r > '9' {
			return false
		}
	}
	sum := 0
	double := true
	for i := 13; i >= 0; i-- {
		n := int(raw[i] - '0')
		if double {
			n *= 2
			if n > 9 {
				n -= 9
			}
		}
		sum += n
		double = !double
	}
	want := byte('0' + (10-sum%10)%10)
	return raw[14] == want
}

// IMEIMatches reports whether two IMEI values identify the same modem.
// Empty or invalid IMEI values never match, even against themselves.
func IMEIMatches(a, b string) bool {
	normalized := NormalizeIMEI(a)
	return normalized != "" && normalized == NormalizeIMEI(b)
}
