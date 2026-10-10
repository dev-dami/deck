//go:build windows

package conin

import (
	"os"
	"strings"
	"testing"
	"time"
	"unsafe"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/erikgeiser/coninput"
	"golang.org/x/sys/windows"
)

// These tests drive a real console: they write key records into its input
// buffer, as a terminal does, and read them back through a Reader. They need a
// console window, so they run only with DECK_CONSOLE=1.

var writeConsoleInput = windows.NewLazySystemDLL("kernel32.dll").NewProc("WriteConsoleInputW")

func console(t *testing.T) windows.Handle {
	t.Helper()
	if os.Getenv("DECK_CONSOLE") != "1" {
		t.Skip("needs a console window: DECK_CONSOLE=1")
	}
	name, _ := windows.UTF16PtrFromString("CONIN$")
	h, err := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		t.Skipf("no console: %v", err)
	}
	t.Cleanup(func() { windows.CloseHandle(h) })
	coninput.FlushConsoleInputBuffer(h)
	return h
}

// keyRecord is a KEY_EVENT input record for one UTF-16 unit.
func keyRecord(c uint16, down bool) coninput.InputRecord {
	var rec coninput.InputRecord
	rec.EventType = coninput.KeyEventType
	if down {
		*(*int32)(unsafe.Pointer(&rec.Event[0])) = 1
	}
	*(*uint16)(unsafe.Pointer(&rec.Event[4])) = 1 // repeat count
	*(*uint16)(unsafe.Pointer(&rec.Event[10])) = c
	return rec
}

// typeIn writes s into the console's input buffer at once, as a terminal
// delivers a paste it does not bracket.
func typeIn(t *testing.T, h windows.Handle, s string) {
	t.Helper()
	var recs []coninput.InputRecord
	for _, c := range windows.StringToUTF16(s) {
		if c == 0 {
			break
		}
		recs = append(recs, keyRecord(c, true), keyRecord(c, false))
	}
	var n uint32
	ok, _, err := writeConsoleInput.Call(uintptr(h), uintptr(unsafe.Pointer(&recs[0])), uintptr(len(recs)), uintptr(unsafe.Pointer(&n)))
	if ok == 0 {
		t.Fatalf("WriteConsoleInput: %v", err)
	}
}

func readSome(t *testing.T, r *Reader) string {
	t.Helper()
	got := make(chan string, 1)
	go func() {
		buf := make([]byte, 4096)
		n, _ := r.Input().Read(buf)
		got <- string(buf[:n])
	}()
	select {
	case s := <-got:
		return s
	case <-time.After(2 * time.Second):
		t.Fatal("nothing read")
		return ""
	}
}

func TestConsoleBlocksAndKeys(t *testing.T) {
	h := console(t)
	r, err := newReader(h)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for _, tc := range []struct{ in, want string }{
		{"one\r\ntwo 😀\r\nthree", pasteStart + "one\r\ntwo 😀\r\nthree" + pasteEnd},
		{"a", "a"},
		{"\r", "\r"},
		{"\x1b[A", "\x1b[A"},
		{pasteStart + "x\ry" + pasteEnd, pasteStart + "x\ry" + pasteEnd},
	} {
		typeIn(t, h, tc.in)
		if got := readSome(t, r); got != tc.want {
			t.Errorf("%q came out as %q, want %q", tc.in, got, tc.want)
		}
	}
}

// A long paste is one message to the program, however many reads it takes.
func TestConsolePasteIsOneMessage(t *testing.T) {
	h := console(t)
	r, err := newReader(h)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	text := strings.Repeat("func main() { println(\"hello\") }\r\n", 60)
	got := make(chan tea.KeyMsg, 16)
	p := tea.NewProgram(probe{got}, tea.WithInput(r.Input()), tea.WithOutput(os.Stderr), tea.WithoutRenderer())
	go p.Run()
	defer p.Kill()
	time.Sleep(100 * time.Millisecond)
	typeIn(t, h, text)
	select {
	case k := <-got:
		if !k.Paste || string(k.Runes) != text {
			t.Fatalf("want one paste of %d runes, got paste=%v with %d runes", len(text), k.Paste, len(k.Runes))
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no key message")
	}
	select {
	case k := <-got:
		t.Fatalf("one message expected, also got %q", k.String())
	case <-time.After(200 * time.Millisecond):
	}
}

type probe struct{ got chan tea.KeyMsg }

func (p probe) Init() tea.Cmd { return nil }
func (p probe) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		p.got <- k
	}
	return p, nil
}
func (p probe) View() string { return "" }
