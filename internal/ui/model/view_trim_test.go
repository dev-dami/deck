package model

import (
	"math/rand"
	"strings"
	"testing"
)

// trimFrameLinesSplit is the split/trim/join View used before
// trimFrameLines; the tests hold the one-pass version to its output.
func trimFrameLinesSplit(s string) string {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " ")
	}
	return strings.Join(lines, "\n")
}

func TestTrimFrameLinesMatchesSplit(t *testing.T) {
	cases := []string{
		"",
		" ",
		"\n",
		"\r\n",
		"\r",
		"\r\r\n",
		"a \r\n",
		"a\r \n",
		"a  \nb\t \n  c  ",
		"x \x1b[0m  \r\n\r\n  ",
		"trailing newline  \n",
		"no newline at all   ",
	}
	r := rand.New(rand.NewSource(1))
	alphabet := []byte{' ', ' ', ' ', '\r', '\n', 'a', '\t', 0x1b, '['}
	for range 2000 {
		b := make([]byte, r.Intn(40))
		for i := range b {
			b[i] = alphabet[r.Intn(len(alphabet))]
		}
		cases = append(cases, string(b))
	}
	for _, c := range cases {
		if got, want := string(trimFrameLines(nil, c)), trimFrameLinesSplit(c); got != want {
			t.Fatalf("trimFrameLines(%q) = %q, want %q", c, got, want)
		}
	}
}

func FuzzTrimFrameLines(f *testing.F) {
	f.Add("a \r\nb  \n c\r")
	f.Fuzz(func(t *testing.T, s string) {
		if got, want := string(trimFrameLines(nil, s)), trimFrameLinesSplit(s); got != want {
			t.Fatalf("trimFrameLines(%q) = %q, want %q", s, got, want)
		}
	})
}

// benchFrame is a rendered screen as View sees it before trimming.
func benchFrame(b *testing.B) string {
	u := newFrameTestUI(b)
	u.frames = nil
	u.View()
	return u.canvas.Render()
}

func BenchmarkTrimFrameLines(b *testing.B) {
	frame := benchFrame(b)
	b.Run("split", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = trimFrameLinesSplit(frame)
		}
	})
	b.Run("onepass", func(b *testing.B) {
		b.ReportAllocs()
		var buf []byte
		for b.Loop() {
			buf = trimFrameLines(buf[:0], frame)
			_ = string(buf)
		}
	})
}
