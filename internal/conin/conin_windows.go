//go:build windows

package conin

import (
	"io"
	"os"
	"sync"
	"unicode/utf16"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/term"
	"github.com/erikgeiser/coninput"
	"golang.org/x/sys/windows"
)

// settle is how long, in milliseconds, to wait for the rest of a block that is
// still arriving. A single key never waits.
const settle = 4

// Reader is the console, read as VT bytes.
type Reader struct {
	h    windows.Handle
	mode uint32
	stop windows.Handle
	pr   *io.PipeReader
	pw   *io.PipeWriter

	mu   sync.Mutex
	prog *tea.Program
	once sync.Once
	done chan struct{}
}

// New puts the console in VT input mode and starts reading it.
func New() (*Reader, error) { return newReader(windows.Handle(os.Stdin.Fd())) }

func newReader(h windows.Handle) (*Reader, error) {
	var mode uint32
	if err := windows.GetConsoleMode(h, &mode); err != nil {
		return nil, ErrUnsupported
	}
	stop, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return nil, err
	}
	// No line editing, echo or ctrl+c handling, no quick edit; keys, mouse
	// and focus as VT sequences, and resizes as records.
	raw := uint32(windows.ENABLE_VIRTUAL_TERMINAL_INPUT | windows.ENABLE_WINDOW_INPUT |
		windows.ENABLE_MOUSE_INPUT | windows.ENABLE_EXTENDED_FLAGS)
	if err := windows.SetConsoleMode(h, raw); err != nil {
		windows.CloseHandle(stop)
		return nil, ErrUnsupported
	}
	pr, pw := io.Pipe()
	r := &Reader{h: h, mode: mode, stop: stop, pr: pr, pw: pw, done: make(chan struct{})}
	go r.loop()
	return r, nil
}

// Input is what Bubble Tea reads (tea.WithInput).
func (r *Reader) Input() io.Reader { return r.pr }

// Attach is the program that resizes are sent to.
func (r *Reader) Attach(p *tea.Program) {
	r.mu.Lock()
	r.prog = p
	r.mu.Unlock()
}

// Close stops reading, ends the input and gives the console its mode back.
func (r *Reader) Close() {
	r.once.Do(func() {
		windows.SetEvent(r.stop)
		<-r.done
		r.pw.Close()
		windows.SetConsoleMode(r.h, r.mode)
		windows.CloseHandle(r.stop)
	})
}

func (r *Reader) loop() {
	defer close(r.done)
	recs := make([]coninput.InputRecord, 512)
	var hi rune // a high surrogate waiting for its pair
	for {
		if !r.wait(windows.INFINITE) {
			return
		}
		var buf []byte
		resized := false
		for {
			n, err := coninput.ReadConsoleInput(r.h, recs)
			if err != nil {
				return
			}
			buf, resized = decode(recs[:n], buf, &hi, resized)
			// A lone key goes straight on; a block (or an escape sequence
			// that may not be whole yet) waits briefly for the rest of itself.
			lone := utf8.RuneCount(buf) <= 1 && (len(buf) == 0 || buf[0] != 0x1b)
			if lone || !r.wait(settle) {
				break
			}
		}
		if resized {
			r.resize()
		}
		if len(buf) > 0 {
			if _, err := r.pw.Write(frame(buf)); err != nil {
				return
			}
		}
	}
}

// wait reports whether input is waiting, within ms; false when stopped or
// timed out.
func (r *Reader) wait(ms uint32) bool {
	ev, err := windows.WaitForMultipleObjects([]windows.Handle{r.h, r.stop}, false, ms)
	return err == nil && ev == windows.WAIT_OBJECT_0
}

func (r *Reader) resize() {
	w, h, err := term.GetSize(os.Stdout.Fd())
	r.mu.Lock()
	p := r.prog
	r.mu.Unlock()
	if err == nil && p != nil {
		go p.Send(tea.WindowSizeMsg{Width: w, Height: h})
	}
}

// decode appends the characters of the key-down records to buf (VT input
// mode delivers every key, mouse report and paste as characters) and notes a
// resize.
func decode(recs []coninput.InputRecord, buf []byte, hi *rune, resized bool) ([]byte, bool) {
	for _, rec := range recs {
		switch e := rec.Unwrap().(type) {
		case coninput.KeyEventRecord:
			if !e.KeyDown || e.Char == 0 {
				continue
			}
			for i := 0; i < max(1, int(e.RepeatCount)); i++ {
				buf = appendChar(buf, e.Char, hi)
			}
		case coninput.WindowBufferSizeEventRecord:
			resized = true
		}
	}
	return buf, resized
}

// appendChar appends one UTF-16 unit, pairing surrogates.
func appendChar(buf []byte, c rune, hi *rune) []byte {
	switch {
	case utf16.IsSurrogate(c) && c < 0xDC00:
		*hi = c
		return buf
	case utf16.IsSurrogate(c):
		if *hi != 0 {
			c = utf16.DecodeRune(*hi, c)
		}
	}
	*hi = 0
	return utf8.AppendRune(buf, c)
}
