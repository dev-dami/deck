package sub

import (
	"encoding/binary"
	"fmt"
	"strings"

	"github.com/zeebo/blake3"
)

// Content-defined chunking (FastCDC, normalized): cut points depend on the
// bytes around them, not on offsets, so inserting a line moves only the one
// or two chunks around it and every other chunk keeps its ID.
const (
	minChunk = 4 << 10
	avgChunk = 16 << 10
	maxChunk = 64 << 10
	// The masks have more bits set before the average size (fewer cuts)
	// and fewer after it, which keeps chunk sizes close to avgChunk.
	maskS = 0x0003590703530000 // 15 bits
	maskL = 0x0000d90003530000 // 11 bits
)

// gear is the rolling hash's table, fixed for all time: changing it would
// change every chunk boundary and so every file ID.
var gear = func() (g [256]uint64) {
	for i := range g {
		h := blake3.Sum256([]byte{'s', 'u', 'b', byte(i)})
		g[i] = binary.LittleEndian.Uint64(h[:8])
	}
	return g
}()

// cut is the length of the first chunk of b.
func cut(b []byte) int {
	n := len(b)
	if n <= minChunk {
		return n
	}
	n = min(n, maxChunk)
	mid := min(avgChunk, n)
	var h uint64
	i := minChunk
	for ; i < mid; i++ {
		h = h<<1 + gear[b[i]]
		if h&maskS == 0 {
			return i + 1
		}
	}
	for ; i < n; i++ {
		h = h<<1 + gear[b[i]]
		if h&maskL == 0 {
			return i + 1
		}
	}
	return n
}

// PutFile chunks b into the store and returns the file's ID: the ID of its
// manifest, the chunk IDs one per line. Equal contents give equal IDs.
func (s *Store) PutFile(b []byte) (ID, error) {
	var m strings.Builder
	for len(b) > 0 {
		n := cut(b)
		id, err := s.Put(b[:n])
		if err != nil {
			return "", err
		}
		m.WriteString(string(id))
		m.WriteByte('\n')
		b = b[n:]
	}
	return s.Put([]byte(m.String()))
}

// FileID is the ID PutFile would give b, without storing anything.
func FileID(b []byte) ID {
	var m strings.Builder
	for len(b) > 0 {
		n := cut(b)
		m.WriteString(string(Sum(b[:n])))
		m.WriteByte('\n')
		b = b[n:]
	}
	return Sum([]byte(m.String()))
}

// Chunks lists a file's chunk IDs.
func (s *Store) Chunks(file ID) ([]ID, error) {
	m, err := s.Get(file)
	if err != nil {
		return nil, err
	}
	var ids []ID
	for _, l := range strings.Split(string(m), "\n") {
		if l == "" {
			continue
		}
		id := ID(l)
		if !id.Valid() {
			return nil, fmt.Errorf("sub: %s is not a file", file.Short())
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// ReadFile puts a file back together from its chunks.
func (s *Store) ReadFile(file ID) ([]byte, error) {
	ids, err := s.Chunks(file)
	if err != nil {
		return nil, err
	}
	var out []byte
	for _, id := range ids {
		c, err := s.Get(id)
		if err != nil {
			return nil, err
		}
		out = append(out, c...)
	}
	return out, nil
}
