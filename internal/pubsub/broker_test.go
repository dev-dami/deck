package pubsub

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

// TestPublishDropWarningIsRateLimited checks that a burst of dropped events
// is all counted but logged once, with the number dropped.
func TestPublishDropWarningIsRateLimited(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })

	b := NewBrokerWithOptions[int](0) // nobody reads: every Publish drops
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_ = b.Subscribe(ctx)

	for i := range 500 {
		b.Publish(UpdatedEvent, i)
	}
	if n := b.DropCount(); n != 500 {
		t.Fatalf("DropCount = %d, want 500", n)
	}
	if n := strings.Count(buf.String(), "Pubsub buffer full"); n != 1 {
		t.Fatalf("logged %d drop warnings, want 1:\n%s", n, buf.String())
	}
}
