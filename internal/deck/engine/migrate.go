package engine

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/dev-dami/deck/internal/deck/config"
	"github.com/dev-dami/deck/internal/deck/peer"
	"github.com/dev-dami/deck/internal/deck/store"
)

// teamConnectMarker names the marker file, under ~/.deck/migrations, that
// says the notice of Deck v3's removal of team chat and contracts was given.
const teamConnectMarker = "v3-team-chat-and-contracts"

// leftPeerFiles are the files of the removed team chat, outbox, contracts
// and ops audit log. They stay in the peers directory, unread.
var leftPeerFiles = []string{"chat.jsonl", "outbox.json", "contracts.json", "audit.jsonl"}

// teamConnectNotice is the one-time notice of Deck v3's removal of team chat,
// the outbox and contracts, or "" when it was given already or nothing here
// was affected. The store migrates the data as it loads it
// (store.Thread.Normalize): this only counts what it did, and remembers with
// a marker file that the notice was given.
func teamConnectNotice(st *store.Store) string {
	dir, err := config.Dir()
	if err != nil {
		return ""
	}
	marker := filepath.Join(dir, "migrations", teamConnectMarker)
	if _, err := os.Stat(marker); err == nil || !errors.Is(err, fs.ErrNotExist) {
		return ""
	}
	threads, _ := st.List()
	frozen, failed := 0, 0
	for _, th := range threads {
		if th.Frozen == store.FrozenContract || th.Frozen == store.FrozenOps {
			frozen++
		}
		for _, tk := range th.Tasks {
			if tk.Status == store.TaskFailed && tk.Error == store.RemoteTaskRemoved {
				failed++
			}
		}
	}
	left := false
	if pd, err := peer.DefaultDir(); err == nil {
		for _, name := range leftPeerFiles {
			if _, err := os.Stat(filepath.Join(pd, name)); err == nil {
				left = true
			}
		}
	}
	if err := os.MkdirAll(filepath.Dir(marker), 0o700); err == nil {
		if err := os.WriteFile(marker, nil, 0o600); err != nil {
			slog.Warn("could not record the team connect notice", "error", err)
		}
	}
	if frozen == 0 && failed == 0 && !left {
		return ""
	}
	return fmt.Sprintf("Team chat and contracts were removed in Deck v3. Deck no longer sends or receives team chat, code contracts or ops reports. "+
		"%d thread(s) that ran a teammate's contract are archived and read-only. %d task(s) a teammate owned were marked failed. "+
		"Shared threads still work until the next update replaces them with direct access.", frozen, failed)
}
