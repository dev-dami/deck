package message

import (
	"strings"
	"testing"
)

func TestAppendContentBufMatchesAppendContent(t *testing.T) {
	var buf, rbuf StreamBuffer
	var a, b Message
	deltas := []string{"Hel", "lo", ", ", "world", "!"}
	for _, d := range deltas {
		a.AppendContent(d)
		b.AppendContentBuf(&buf, d)
		a.AppendReasoningContent(d)
		b.AppendReasoningContentBuf(&rbuf, d)
	}
	if a.Content().Text != b.Content().Text || b.Content().Text != "Hello, world!" {
		t.Fatalf("text = %q, want %q", b.Content().Text, a.Content().Text)
	}
	if a.ReasoningContent().Thinking != b.ReasoningContent().Thinking {
		t.Fatalf("reasoning = %q, want %q", b.ReasoningContent().Thinking, a.ReasoningContent().Thinking)
	}

	// Text changed under the buffer (a final text replaced the stream, or a
	// new message): the buffer starts over from it.
	b.Parts[0] = TextContent{Text: "fresh", Hidden: true}
	b.AppendContentBuf(&buf, "+")
	if c := b.Content(); c.Text != "fresh+" || !c.Hidden {
		t.Fatalf("after reseed = %+v", c)
	}
	// Earlier strings stay intact as the buffer grows.
	keep := b.Content().Text
	for range 100 {
		b.AppendContentBuf(&buf, "x")
	}
	if keep != "fresh+" || b.Content().Text != "fresh+"+strings.Repeat("x", 100) {
		t.Fatalf("keep = %q, text = %q", keep, b.Content().Text)
	}
}

func TestHasToolCalls(t *testing.T) {
	var m Message
	m.AppendContent("hi")
	if m.HasToolCalls() {
		t.Fatal("no tool call yet")
	}
	m.AddToolCall(ToolCall{ID: "1"})
	if !m.HasToolCalls() {
		t.Fatal("tool call not seen")
	}
}

func BenchmarkStreamDeltas(b *testing.B) {
	delta := strings.Repeat("y", 8)
	b.Run("AppendContent", func(b *testing.B) {
		for range b.N {
			var m Message
			for range 4000 {
				m.AppendContent(delta)
			}
		}
	})
	b.Run("AppendContentBuf", func(b *testing.B) {
		for range b.N {
			var m Message
			var buf StreamBuffer
			for range 4000 {
				m.AppendContentBuf(&buf, delta)
			}
		}
	})
}
