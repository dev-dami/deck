package auth

import (
	"regexp"
	"strings"
	"testing"
)

func TestPairCodeFormat(t *testing.T) {
	re := regexp.MustCompile(`^[23456789ABCDEFGHJKMNPQRSTVWXYZ]{5}-[23456789ABCDEFGHJKMNPQRSTVWXYZ]{5}$`)
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		c, err := NewPairCode()
		if err != nil || !re.MatchString(c) {
			t.Fatalf("code %q: %v", c, err)
		}
		if seen[c] {
			t.Fatalf("repeated code %q", c)
		}
		seen[c] = true
		if !ValidCode(NormalizeCode(strings.ToLower(c))) || !ValidCode(NormalizeCode(strings.ReplaceAll(c, "-", " "))) {
			t.Fatalf("normalize failed for %q", c)
		}
	}
	for _, bad := range []string{"", "ABCDE", "ABCDE-2345O", "ABCDE-234561", "ABCDE-2345I"} {
		if ValidCode(NormalizeCode(bad)) {
			t.Fatalf("%q accepted", bad)
		}
	}
}
