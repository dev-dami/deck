package projects

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAddGetAndPersist(t *testing.T) {
	dir, proj := t.TempDir(), t.TempDir()
	r, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	p, err := r.Add(proj, "")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != filepath.Base(proj) || !p.Enabled || !strings.HasPrefix(p.ID, "prj_") {
		t.Fatalf("project %+v", p)
	}
	again, err := r.Add(proj, "renamed")
	if err != nil || again.ID != p.ID || len(r.List()) != 1 {
		t.Fatalf("adding the same folder twice: %+v %v (%d projects)", again, err, len(r.List()))
	}
	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := reopened.Get(p.ID); err != nil || got.Name != "renamed" {
		t.Fatalf("after reopen: %+v %v", got, err)
	}
}

func TestAddRejectsNonDirectories(t *testing.T) {
	r, _ := Open(t.TempDir())
	file := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{file, filepath.Join(t.TempDir(), "missing")} {
		if _, err := r.Add(path, ""); err == nil {
			t.Errorf("%s accepted", path)
		}
	}
}

func TestDisabledProjectIsInvisibleToThePhone(t *testing.T) {
	r, _ := Open(t.TempDir())
	p, _ := r.Add(t.TempDir(), "a")
	if err := r.SetEnabled(p.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Get(p.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("disabled project served: %v", err)
	}
	if len(r.Refs(func(string) bool { return true })) != 0 {
		t.Fatal("disabled project listed to the phone")
	}
	if len(r.List()) != 1 {
		t.Fatal("the CLI should still see it")
	}
}

func TestRefsNeverCarryThePath(t *testing.T) {
	r, _ := Open(t.TempDir())
	proj := t.TempDir()
	r.Add(proj, "deck")
	refs := r.Refs(func(string) bool { return true })
	raw, _ := json.Marshal(refs)
	if strings.Contains(string(raw), filepath.Base(proj)) && filepath.Base(proj) != "deck" {
		t.Fatalf("the path leaked: %s", raw)
	}
	if len(refs) != 1 || refs[0].Name != "deck" || !refs[0].Online {
		t.Fatalf("refs %+v", refs)
	}
}

func TestRemoveUnknown(t *testing.T) {
	r, _ := Open(t.TempDir())
	if err := r.Remove("prj_nope"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if err := r.SetEnabled("prj_nope", true); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}
