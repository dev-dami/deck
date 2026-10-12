package peer

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

// DefaultMaxMessage caps one JSON-RPC message on an mcp channel, in bytes.
const DefaultMaxMessage = 1 << 20

// ErrMessageTooLarge is returned for a frame longer than the cap; the
// channel is closed after it.
var ErrMessageTooLarge = errors.New("peer: message too large")

// lineStream is the framing on an mcp channel: newline-delimited JSON-RPC
// 2.0, one message per line, like MCP's stdio transport. It implements
// jsonrpc2.ObjectStream. A line longer than max, or one that is not JSON, gets
// a JSON-RPC error reply and then closes the stream: a peer that breaks the
// framing is not worth resynchronizing with.
type lineStream struct {
	r   *bufio.Reader
	w   io.Writer
	c   io.Closer
	max int
	wmu sync.Mutex
}

func newLineStream(rwc io.ReadWriteCloser, max int) *lineStream {
	if max <= 0 {
		max = DefaultMaxMessage
	}
	return &lineStream{r: bufio.NewReaderSize(rwc, 32<<10), w: rwc, c: rwc, max: max}
}

// readFrame returns the next non-blank line without its line ending. It never
// buffers more than max+2 bytes, whatever the peer sends.
func readFrame(r *bufio.Reader, max int) ([]byte, error) {
	var buf []byte
	for {
		chunk, err := r.ReadSlice('\n')
		if len(buf)+len(chunk) > max+2 { // room for "\r\n"
			return nil, ErrMessageTooLarge
		}
		buf = append(buf, chunk...)
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if err != nil {
			if err == io.EOF && len(bytes.TrimSpace(buf)) > 0 {
				return nil, io.ErrUnexpectedEOF // a partial message is dropped
			}
			return nil, err
		}
		line := bytes.TrimSpace(buf)
		if len(line) == 0 {
			buf = buf[:0]
			continue
		}
		return line, nil
	}
}

// ReadObject implements jsonrpc2.ObjectStream.
func (s *lineStream) ReadObject(v any) error {
	line, err := readFrame(s.r, s.max)
	if errors.Is(err, ErrMessageTooLarge) {
		s.writeError(-32600, "message too large")
		return err
	}
	if err != nil {
		return err
	}
	if err := json.Unmarshal(line, v); err != nil {
		s.writeError(-32700, "parse error")
		return fmt.Errorf("peer: bad frame: %w", err)
	}
	return nil
}

// WriteObject implements jsonrpc2.ObjectStream.
func (s *lineStream) WriteObject(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if len(data) > s.max {
		return ErrMessageTooLarge
	}
	return s.writeLine(data)
}

func (s *lineStream) writeLine(data []byte) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	_, err := s.w.Write(append(data, '\n'))
	return err
}

// writeError sends a JSON-RPC error with a null id, best effort: it waits at
// most errorWriteWait, since a peer that stops reading must not keep the
// stream from closing. A write still blocked ends when the stream closes.
func (s *lineStream) writeError(code int, msg string) {
	data, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": nil,
		"error": map[string]any{"code": code, "message": msg},
	})
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = s.writeLine(data)
	}()
	t := time.NewTimer(errorWriteWait)
	defer t.Stop()
	select {
	case <-done:
	case <-t.C:
	}
}

// errorWriteWait bounds writeError.
const errorWriteWait = time.Second

// Close implements jsonrpc2.ObjectStream.
func (s *lineStream) Close() error { return s.c.Close() }
