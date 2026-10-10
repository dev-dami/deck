//go:build !windows

package conin

import (
	"io"

	tea "github.com/charmbracelet/bubbletea"
)

// Reader is unused outside Windows.
type Reader struct{}

// New reports ErrUnsupported: Bubble Tea reads the terminal itself.
func New() (*Reader, error) { return nil, ErrUnsupported }

func (r *Reader) Input() io.Reader    { return nil }
func (r *Reader) Attach(*tea.Program) {}
func (r *Reader) Close()              {}
