package sub

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/klauspost/compress/zstd"
)

// Moving a capsule to another store (another machine) is three steps: the
// receiver asks for the capsule's Closure, keeps the IDs it is Missing, and
// the sender Packs just those into one zstd stream the receiver Unpacks.
// Work on the same files again and again sends almost nothing, since the
// receiver already has every chunk but the few that changed.

// Closure is every object a capsule (with its chain) needs to be verified,
// merged and applied elsewhere: the capsules, and each file's manifest and
// chunks before and after. The base snapshot is not included; applying never
// needs it.
func (s *Store) Closure(id ID) ([]ID, error) {
	chain, err := s.Chain(id)
	if err != nil {
		return nil, err
	}
	seen := map[ID]bool{}
	var out []ID
	add := func(i ID) {
		if i != "" && !seen[i] {
			seen[i] = true
			out = append(out, i)
		}
	}
	for c := id; c != ""; {
		add(c)
		cp, err := s.Capsule(c)
		if err != nil {
			return nil, err
		}
		c = cp.Parent
	}
	for _, c := range chain {
		for _, o := range c.Ops {
			for _, f := range []ID{o.Before, o.After} {
				if f == "" || seen[f] {
					continue
				}
				add(f)
				chunks, err := s.Chunks(f)
				if err != nil {
					return nil, err
				}
				for _, ch := range chunks {
					add(ch)
				}
			}
		}
	}
	return out, nil
}

// Missing is the ids the store does not have.
func (s *Store) Missing(ids []ID) []ID {
	var out []ID
	for _, id := range ids {
		if !s.Has(id) {
			out = append(out, id)
		}
	}
	return out
}

// MaxObject bounds one object in a pack: chunks are at most maxChunk, and
// manifests, snapshots and capsules are far below this.
const MaxObject = 64 << 20

// Pack writes ids to w as one zstd stream of length-prefixed objects.
func (s *Store) Pack(w io.Writer, ids []ID) error {
	zw, err := zstd.NewWriter(w, zstd.WithEncoderLevel(zstd.SpeedDefault))
	if err != nil {
		return err
	}
	var n [binary.MaxVarintLen64]byte
	for _, id := range ids {
		b, err := s.Get(id)
		if err != nil {
			zw.Close()
			return err
		}
		if _, err := zw.Write(n[:binary.PutUvarint(n[:], uint64(len(b)))]); err != nil {
			zw.Close()
			return err
		}
		if _, err := zw.Write(b); err != nil {
			zw.Close()
			return err
		}
	}
	return zw.Close()
}

// Unpack stores every object in a Pack stream, at most limit bytes of
// objects in all, and returns the IDs it stored. Each object is stored under
// its own hash, so a sender can never place bytes under an ID they do not
// hash to.
func (s *Store) Unpack(r io.Reader, limit int64) ([]ID, error) {
	zr, err := zstd.NewReader(r, zstd.WithDecoderMaxMemory(uint64(MaxObject)*2))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	br := bufio.NewReader(zr)
	var ids []ID
	var total int64
	for {
		size, err := binary.ReadUvarint(br)
		if errors.Is(err, io.EOF) {
			return ids, nil
		}
		if err != nil {
			return ids, fmt.Errorf("sub: bad pack: %w", err)
		}
		if size > MaxObject {
			return ids, fmt.Errorf("sub: pack object of %d bytes is too large", size)
		}
		if total += int64(size); total > limit {
			return ids, fmt.Errorf("sub: pack is over %d bytes", limit)
		}
		b := make([]byte, size)
		if _, err := io.ReadFull(br, b); err != nil {
			return ids, fmt.Errorf("sub: bad pack: %w", err)
		}
		id, err := s.Put(b)
		if err != nil {
			return ids, err
		}
		ids = append(ids, id)
	}
}

// Publishing a capsule is what lets another Deck fetch it: a peer can only
// read objects in the closure of a published capsule, never a checkpoint or
// anything else in the store, whatever IDs it asks for.

var pubMu sync.Mutex

func (s *Store) pubDir() string { return filepath.Join(s.dir, "published") }

// Publish makes the capsule fetchable by peers and returns its closure.
func (s *Store) Publish(id ID) ([]ID, error) {
	ids, err := s.Closure(id)
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	for _, i := range ids {
		b.WriteString(string(i))
		b.WriteByte('\n')
	}
	pubMu.Lock()
	defer pubMu.Unlock()
	if err := os.MkdirAll(s.pubDir(), 0o755); err != nil {
		return nil, err
	}
	if err := writeAtomic(filepath.Join(s.pubDir(), strings.TrimPrefix(string(id), idPrefix)), []byte(b.String()), 0o644); err != nil {
		return nil, err
	}
	s.pubSet = nil // rebuilt on next use
	return ids, nil
}

// Unpublish makes the capsule private again: peers can no longer fetch it
// (nor what only it shared). Copies they fetched before stay theirs.
func (s *Store) Unpublish(id ID) error {
	pubMu.Lock()
	defer pubMu.Unlock()
	err := os.Remove(filepath.Join(s.pubDir(), strings.TrimPrefix(string(id), idPrefix)))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	s.pubSet = nil // rebuilt on next use
	return nil
}

// IsPublished reports whether the capsule itself is published.
func (s *Store) IsPublished(id ID) bool {
	_, err := os.Stat(filepath.Join(s.pubDir(), strings.TrimPrefix(string(id), idPrefix)))
	return err == nil
}

// Published lists the published capsules.
func (s *Store) Published() []ID {
	ents, _ := os.ReadDir(s.pubDir())
	var out []ID
	for _, e := range ents {
		id := ID(idPrefix + e.Name())
		if id.Valid() {
			out = append(out, id)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Shareable reports whether id is in the closure of a published capsule.
func (s *Store) Shareable(id ID) bool {
	pubMu.Lock()
	defer pubMu.Unlock()
	if s.pubSet == nil {
		s.pubSet = map[ID]bool{}
		ents, _ := os.ReadDir(s.pubDir())
		for _, e := range ents {
			b, err := os.ReadFile(filepath.Join(s.pubDir(), e.Name()))
			if err != nil {
				continue
			}
			for _, l := range strings.Split(string(b), "\n") {
				if l != "" {
					s.pubSet[ID(l)] = true
				}
			}
		}
	}
	return s.pubSet[id]
}
