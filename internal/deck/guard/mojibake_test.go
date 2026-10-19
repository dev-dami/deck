package guard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// textExts are the extensions TestNoMojibake reads: sources, docs, golden
// files and the committed web client build.
var textExts = map[string]bool{
	".go": true, ".golden": true, ".md": true, ".txt": true,
	".json": true, ".jsonl": true, ".yml": true, ".yaml": true, ".sql": true,
	".ts": true, ".tsx": true, ".js": true, ".css": true, ".html": true, ".svg": true,
	".webmanifest": true, ".sh": true, ".ps1": true,
}

// cp1252 maps the Windows-1252 characters in 0x80-0x9F back to their byte.
var cp1252 = map[rune]byte{
	0x20AC: 0x80, 0x201A: 0x82, 0x0192: 0x83, 0x201E: 0x84, 0x2026: 0x85, 0x2020: 0x86,
	0x2021: 0x87, 0x02C6: 0x88, 0x2030: 0x89, 0x0160: 0x8A, 0x2039: 0x8B, 0x0152: 0x8C,
	0x017D: 0x8E, 0x2018: 0x91, 0x2019: 0x92, 0x201C: 0x93, 0x201D: 0x94, 0x2022: 0x95,
	0x2013: 0x96, 0x2014: 0x97, 0x02DC: 0x98, 0x2122: 0x99, 0x0161: 0x9A, 0x203A: 0x9B,
	0x0153: 0x9C, 0x017E: 0x9E, 0x0178: 0x9F,
}

// mojibakeByte returns the byte r came from if UTF-8 text was decoded as
// Windows-1252 or Latin-1 (the C1 controls) and re-encoded.
func mojibakeByte(r rune) (byte, bool) {
	if r >= 0x80 && r <= 0xFF {
		return byte(r), true
	}
	b, ok := cp1252[r]
	return b, ok
}

// findMojibake returns the first double-encoded UTF-8 sequence in s, such as
// "\u00c2\u00b7" for a middle dot, or "" if there is none. A two-byte
// sequence counts only with a C2 or C3 lead (U+0080-U+00FF, which covers the
// usual middle dot and accented letters), so an accented capital before a
// curly quote in prose is not flagged; longer sequences need two or three
// continuation runes, which ordinary text does not produce.
func findMojibake(s string) string {
	runes := []rune(s)
	for i, r := range runes {
		lead, ok := mojibakeByte(r)
		if !ok {
			continue
		}
		var n int
		switch {
		case lead == 0xC2 || lead == 0xC3:
			n = 2
		case lead >= 0xE0 && lead <= 0xEF:
			n = 3
		case lead >= 0xF0 && lead <= 0xF4:
			n = 4
		default:
			continue
		}
		if i+n > len(runes) {
			continue
		}
		buf := []byte{lead}
		for _, c := range runes[i+1 : i+n] {
			b, ok := mojibakeByte(c)
			if !ok || b < 0x80 || b > 0xBF {
				break
			}
			buf = append(buf, b)
		}
		if len(buf) == n && utf8.Valid(buf) {
			return string(runes[i : i+n])
		}
	}
	return ""
}

func TestNoMojibake(t *testing.T) {
	root := repoRoot(t)

	files := walkFiles(t, root, func(path string) bool { return textExts[filepath.Ext(path)] })
	if len(files) < 3 {
		t.Fatalf("found only %d text files under %s; the walker is not seeing the repo", len(files), root)
	}

	for _, path := range files {
		rel, _ := filepath.Rel(root, path)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("read %s: %v", path, err)
			continue
		}
		if !strings.ContainsFunc(string(data), func(r rune) bool { return r >= 0x80 }) {
			continue
		}
		for n, line := range strings.Split(string(data), "\n") {
			if bad := findMojibake(line); bad != "" {
				fixed := make([]byte, 0, 4)
				for _, r := range bad {
					b, _ := mojibakeByte(r)
					fixed = append(fixed, b)
				}
				t.Errorf("%s:%d: double-encoded UTF-8 %q (meant %q); write the character itself or a \\u escape",
					filepath.ToSlash(rel), n+1, bad, fixed)
			}
		}
	}
}

// TestFindMojibake checks the detector against real mojibake and against
// well-formed text it must leave alone, so the scan above cannot pass by
// accident or fail on ordinary prose.
func TestFindMojibake(t *testing.T) {
	for _, bad := range []string{
		"claude \u00c2\u00b7 sonnet",         // middle dot
		"v1 \u00e2\u2020\u2019 v2",           // right arrow
		"wait \u00e2\u20ac\u201d done",       // em dash
		"I\u00e2\u20ac\u2122ll",              // right single quote
		"caf\u00c3\u00a9",                    // e acute
		"\u00c3\u0089t\u00c3\u00a9",          // E acute via a Latin-1 C1 control
		"ok \u00f0\u0178\u0161\u20ac",        // four-byte emoji
		"box \u00e2\u201d\u20ac\u00e2\u201d", // box drawing
	} {
		if findMojibake(bad) == "" {
			t.Errorf("findMojibake(%q) found nothing", bad)
		}
	}
	for _, good := range []string{
		"plain ascii",
		"claude · sonnet → done — fine’s “quoted” …",
		"café, naïve, Ærøskøbing, Straße, Ünïcödé",
		"ÉTÉ” and Ö’s", // accented capital before a curly quote
		"日本語 ─│┌ ● ✓ 🚀",
		"\u00e2\u20ac", // truncated: not a full sequence
	} {
		if got := findMojibake(good); got != "" {
			t.Errorf("findMojibake(%q) = %q, want none", good, got)
		}
	}
}
