package web

import (
	"io/fs"
	"strings"
	"testing"
)

// The built client is committed; an empty dist means a release would ship a
// server with no page. Rebuild with `bun run build` in web/remote.
func TestEmbeddedClientHasTheAppShell(t *testing.T) {
	index, err := fs.ReadFile(FS(), "index.html")
	if err != nil {
		t.Fatalf("index.html is not embedded: %v", err)
	}
	if !strings.Contains(string(index), "<script") {
		t.Fatal("index.html loads no script")
	}
	for _, f := range []string{"sw.js", "manifest.webmanifest"} {
		if _, err := fs.Stat(FS(), f); err != nil {
			t.Errorf("%s is not embedded: %v", f, err)
		}
	}
}
