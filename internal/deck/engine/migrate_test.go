package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dev-dami/deck/internal/deck/store"
)

func TestTeamConnectNoticeOnce(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir := t.TempDir()
	metas := map[string]string{
		"k1": `{"id":"k1","contract":"vps","members":[{"id":"claude","kind":"claude"}],"created":"2026-01-01T00:00:00Z","updated":"2026-01-01T00:00:00Z"}`,
		"m1": `{"id":"m1","members":[{"id":"claude","kind":"claude"}],"tasks":[{"id":"t1","title":"asked vps","status":"running","owner":"vps","remote":"vps",` +
			`"created":"2026-01-01T00:00:00Z","updated":"2026-01-01T00:00:00Z"}],"created":"2026-01-01T00:00:00Z","updated":"2026-01-01T00:00:00Z"}`,
	}
	for id, meta := range metas {
		if err := os.WriteFile(filepath.Join(dir, id+".meta.json"), []byte(meta), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	st, err := store.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	text := teamConnectNotice(st)
	for _, want := range []string{"Team chat and contracts were removed in Deck v3", "1 thread(s)", "1 task(s)"} {
		if !strings.Contains(text, want) {
			t.Errorf("notice %q lacks %q", text, want)
		}
	}
	if again := teamConnectNotice(st); again != "" {
		t.Errorf("the notice came twice: %q", again)
	}
}

func TestTeamConnectNoticeNamesLeftFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	st, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if text := teamConnectNotice(st); text != "" {
		t.Fatalf("nothing was affected, yet the notice says %q", text)
	}

	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	peers := filepath.Join(home, ".deck", "peers")
	if err := os.MkdirAll(peers, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(peers, "chat.jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if text := teamConnectNotice(st); !strings.Contains(text, "0 thread(s)") {
		t.Fatalf("a left chat.jsonl gives no notice: %q", text)
	}
}
