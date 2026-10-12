package peer

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

// pipeRWC feeds fixed input to a stream and discards what it writes.
type pipeRWC struct {
	r io.Reader
	w io.Writer
}

func (p pipeRWC) Read(b []byte) (int, error)  { return p.r.Read(b) }
func (p pipeRWC) Write(b []byte) (int, error) { return p.w.Write(b) }
func (pipeRWC) Close() error                  { return nil }

var fuzzSeeds = []string{
	`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}` + "\n" +
		`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}` + "\n" +
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"execute","arguments":{"op":"peer.status"}}}` + "\n",
	`{"jsonrpc":"2.0","id":1,"method":"initialize"}` + "\n" + `{"jsonrpc":"2.0","id":"x","method":"tools/call","params":{"name":"search","arguments":null}}` + "\n",
	`{"jsonrpc":"2.0","id":1,"method":"notifications/cancelled","params":{"requestId":1}}` + "\n",
	`[{"jsonrpc":"2.0","id":1,"method":"ping"}]` + "\n",
	`{"jsonrpc":"2.0","id":1,"result":{}}` + "\n",
	`{"jsonrpc":"2.0","id":1,"error":{"code":1,"message":"x"}}` + "\n",
	"\n\n\r\n",
	`{"jsonrpc":"2.0","id":18446744073709551615,"method":"ping"}` + "\n",
	`{"jsonrpc":"2.0","id":-1,"method":"ping"}` + "\n",
	`{"method":"tools/call","id":1,"params":{"name":"execute","arguments":{"op":"","args":"x"}}}` + "\n",
	"{",
	"\xff\xfe\x00",
}

// FuzzMCPFrames runs the whole MCP server over arbitrary input. It must
// never panic or hang, whatever the bytes.
func FuzzMCPFrames(f *testing.F) {
	for _, s := range fuzzSeeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		srv := newMCPServer(&StatusHandler{Name: "fuzz", Started: time.Now()}, Caller{Name: "f"}, "fuzz", "0",
			serverLimits{maxInFlight: 4, callTimeout: time.Second}, quietLog)
		done := make(chan struct{})
		go func() {
			defer close(done)
			serveMCP(context.Background(), pipeRWC{bytes.NewReader(data), io.Discard}, srv, 4096)
		}()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatalf("server hung on %q", data)
		}
	})
}

// FuzzReadFrame checks the framing alone: frames never exceed the cap,
// never contain a newline, and never come back empty.
func FuzzReadFrame(f *testing.F) {
	for _, s := range fuzzSeeds {
		f.Add([]byte(s), 64)
	}
	f.Add([]byte(strings.Repeat("a", 300)+"\n"), 100)
	f.Fuzz(func(t *testing.T, data []byte, max int) {
		if max <= 0 || max > 1<<16 {
			max = 64
		}
		r := bufio.NewReaderSize(bytes.NewReader(data), 16)
		for i := 0; i < len(data)+1; i++ {
			line, err := readFrame(r, max)
			if err != nil {
				if !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, ErrMessageTooLarge) {
					t.Fatalf("unexpected error %v", err)
				}
				return
			}
			if len(line) == 0 || len(line) > max+2 || bytes.ContainsRune(line, '\n') {
				t.Fatalf("bad frame %q (max %d)", line, max)
			}
		}
	})
}
