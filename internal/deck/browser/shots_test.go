package browser

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSaveShot(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "shots")
	path, err := SaveShot(dir, "claude2/../evil", []byte("\xff\xd8img"), "image/jpeg")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(path) != dir || !strings.HasSuffix(path, ".jpg") || strings.Contains(filepath.Base(path), "..") || strings.Contains(filepath.Base(path), "/") {
		t.Fatalf("path = %s", path)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "\xff\xd8img" {
		t.Fatalf("file = %q, %v", data, err)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", info.Mode().Perm())
	}
	if info, _ := os.Stat(dir); info.Mode().Perm() != 0o700 {
		t.Errorf("folder mode = %v, want 0700", info.Mode().Perm())
	}
	png, err := SaveShot(dir, "codex1", []byte("\x89PNG"), "image/png")
	if err != nil || !strings.HasSuffix(png, ".png") {
		t.Fatalf("png = %s, %v", png, err)
	}
}

func TestShotsArePruned(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "old.jpg")
	if err := os.WriteFile(old, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-shotAge - time.Minute)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}
	for i := range shotKeep + 5 {
		p := filepath.Join(dir, "shot"+string(rune('a'+i%26))+string(rune('a'+i/26))+".jpg")
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		at := time.Now().Add(-time.Duration(shotKeep+5-i) * time.Second)
		_ = os.Chtimes(p, at, at)
	}
	prune(dir, time.Now())
	es, _ := os.ReadDir(dir)
	if len(es) >= shotKeep {
		t.Errorf("%d shots kept, want fewer than %d", len(es), shotKeep)
	}
	if _, err := os.Stat(old); err == nil {
		t.Error("an old shot was kept")
	}
}
