package claude

import (
	"bufio"
	"io"
	"strings"
	"testing"
)

// TestReadLineLongerThanBuffer checks that readLine returns lines longer
// than the reader's buffer whole, and the short ones around them intact.
func TestReadLineLongerThanBuffer(t *testing.T) {
	long := strings.Repeat("x", 100)
	in := "a\n" + long + "\n" + "b\n" + long + "tail"
	br := bufio.NewReaderSize(strings.NewReader(in), 16)

	var got []string
	var scratch []byte
	for {
		line, buf, err := readLine(br, scratch)
		scratch = buf
		if len(line) > 0 {
			got = append(got, string(line))
		}
		if err != nil {
			if err != io.EOF {
				t.Fatal(err)
			}
			break
		}
	}
	want := []string{"a\n", long + "\n", "b\n", long + "tail"}
	if len(got) != len(want) {
		t.Fatalf("got %d lines, want %d: %q", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("line %d = %q, want %q", i, got[i], want[i])
		}
	}
}
