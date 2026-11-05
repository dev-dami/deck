package auth

import (
	"crypto/rand"
	"strings"
)

// codeAlphabet has no 0/O, 1/I/L or U, so a code read aloud survives.
const codeAlphabet = "23456789ABCDEFGHJKMNPQRSTVWXYZ"

// codeLen is the code's length without the dash: 30^10, about 49 bits.
const codeLen = 10

// NewPairCode returns a fresh random code, formatted XXXXX-XXXXX.
func NewPairCode() (string, error) {
	var b strings.Builder
	buf := make([]byte, 1)
	for b.Len() < codeLen {
		if _, err := rand.Read(buf); err != nil {
			return "", err
		}
		// Reject sampling keeps every symbol equally likely.
		if int(buf[0]) >= 256-256%len(codeAlphabet) {
			continue
		}
		b.WriteByte(codeAlphabet[int(buf[0])%len(codeAlphabet)])
	}
	s := b.String()
	return s[:5] + "-" + s[5:], nil
}

// NormalizeCode upper-cases a typed code and drops dashes and spaces.
func NormalizeCode(s string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(s) {
		if r == '-' || r == ' ' || r == '\t' {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// ValidCode reports whether a normalized code could have come from
// NewPairCode.
func ValidCode(s string) bool {
	if len(s) != codeLen {
		return false
	}
	for _, r := range s {
		if !strings.ContainsRune(codeAlphabet, r) {
			return false
		}
	}
	return true
}
