package privfile

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestWritePrivate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "d")
	path := filepath.Join(dir, "f")
	if err := Write(path, []byte("one")); err != nil {
		t.Fatal(err)
	}
	if err := Write(path, []byte("two")); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); string(data) != "two" {
		t.Fatalf("content %q, want two", data)
	}
	if err := WriteNew(path, []byte("three")); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("WriteNew over an existing file = %v, want fs.ErrExist", err)
	}
	if err := Check(path); err != nil {
		t.Fatalf("file not private: %v", err)
	}
	if err := Check(dir); err != nil {
		t.Fatalf("dir not private: %v", err)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("temp file left: %s", e.Name())
		}
	}
}

func TestCheckFlagsSharedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shared")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(path, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// On Windows a file in the temp dir inherits its ACL, which also grants
	// SYSTEM and Administrators.
	if err := Check(path); err == nil {
		t.Fatal("Check accepted a file others can read")
	}
	if err := Restrict(path, false); err != nil {
		t.Fatal(err)
	}
	if err := Check(path); err != nil {
		t.Fatalf("after Restrict: %v", err)
	}
}
