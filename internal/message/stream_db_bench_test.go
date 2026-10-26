package message

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/dev-dami/deck/internal/db"
	"github.com/dev-dami/deck/internal/session"
)

// BenchmarkTokenToDB is the per-token path of a streamed reply as the
// workspace drives it: append the delta, hand the message to Update, and
// let the debounce (or, for flush-each, every Update) write it to SQLite.
// One op is a whole reply; ns/token is the cost of one delta.
func BenchmarkTokenToDB(b *testing.B) {
	for _, tc := range []struct {
		name     string
		debounce time.Duration
	}{
		{"debounced", defaultUpdateDebounce},
		{"flush-each", 0},
	} {
		for _, tokens := range []int{500, 2000} {
			b.Run(fmt.Sprintf("%s/tokens=%d", tc.name, tokens), func(b *testing.B) {
				ctx := b.Context()
				conn, err := db.Connect(ctx, b.TempDir())
				if err != nil {
					b.Fatal(err)
				}
				defer conn.Close()
				q := db.New(conn)
				sess, err := session.NewService(q, conn).Create(ctx, "bench")
				if err != nil {
					b.Fatal(err)
				}
				svc := NewService(q, WithDebounce(tc.debounce))
				delta := strings.Repeat("y", 7) + " "
				b.ReportAllocs()
				b.ResetTimer()
				for b.Loop() {
					m, err := svc.Create(ctx, sess.ID, CreateMessageParams{Role: Assistant})
					if err != nil {
						b.Fatal(err)
					}
					var buf StreamBuffer
					for range tokens {
						m.AppendContentBuf(&buf, delta)
						if err := svc.Update(ctx, m); err != nil {
							b.Fatal(err)
						}
					}
					if err := svc.Flush(ctx, m.ID); err != nil {
						b.Fatal(err)
					}
				}
				b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*tokens), "ns/token")
			})
		}
	}
}
