package peer

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/dev-dami/deck/internal/deck/hub"
)

// maxAuditLines caps the audit log; past it, the oldest fifth goes.
const maxAuditLines = 5000

// AuditEntry is one event of an ops contract, on either Deck.
type AuditEntry struct {
	At       time.Time `json:"at"`
	Peer     string    `json:"peer"`
	Contract string    `json:"contract"`
	// Event is what happened: "proposed", "accepted", "refused",
	// "sh.run", "result refused", "result sent", "result accepted", ...
	Event  string `json:"event"`
	Detail string `json:"detail,omitempty"`
}

// auditLog is audit.jsonl in the peers directory: one AuditEntry a line.
type auditLog struct {
	path string
	mu   sync.Mutex
	// lines counts the lines in the file; -1 until it is read.
	lines int
}

func newAuditLog(dir string) *auditLog {
	l := &auditLog{lines: -1}
	if dir != "" {
		l.path = filepath.Join(dir, "audit.jsonl")
	}
	return l
}

// Audit records an event of ops contract id with peer. The detail is
// redacted and capped.
func (s *Service) Audit(peer, id, event, detail string) {
	s.audit.add(AuditEntry{At: time.Now().UTC(), Peer: peer, Contract: id, Event: event, Detail: cleanLine(hub.Redact(detail), 500)})
}

func (l *auditLog) add(e AuditEntry) {
	if l.path == "" {
		return
	}
	line, err := json.Marshal(e)
	if err != nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.lines < 0 {
		raw, err := os.ReadFile(l.path)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			// A new file is made private before anything goes in it.
			if writePrivateFile(l.path, nil, false) != nil {
				return
			}
			l.lines = 0
		case err != nil:
			return
		default:
			l.lines = bytes.Count(raw, []byte("\n"))
		}
	}
	if l.lines >= maxAuditLines {
		keep, err := os.ReadFile(l.path)
		if err != nil {
			l.lines = -1
			return
		}
		for n := bytes.Count(keep, []byte("\n")); n > maxAuditLines*4/5; n-- {
			_, keep, _ = bytes.Cut(keep, []byte("\n"))
		}
		if writePrivateFile(l.path, append(keep, append(line, '\n')...), false) == nil {
			l.lines = bytes.Count(keep, []byte("\n")) + 1
		}
		return
	}
	f, err := os.OpenFile(l.path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		l.lines = -1 // gone: made again next time
		return
	}
	if _, err := f.Write(append(line, '\n')); err == nil {
		l.lines++
	}
	_ = f.Close()
}
