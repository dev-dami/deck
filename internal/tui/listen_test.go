package tui

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Update must re-arm the session listener for every message the session
// sends; a type missing from that case stops the live app hearing anything
// after the first one arrives (the test rig feeds messages directly, so other
// tests cannot see it).
func TestUpdateKeepsListeningForEverySessionMessage(t *testing.T) {
	files, err := filepath.Glob("../session/*.go")
	if err != nil {
		t.Fatal(err)
	}
	decl := regexp.MustCompile(`(?m)^type ([A-Z][A-Za-z]*Msg) struct`)
	var types []string
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range decl.FindAllStringSubmatch(string(b), -1) {
			types = append(types, m[1])
		}
	}
	if len(types) < 10 {
		t.Fatalf("found only %v; did the session package move?", types)
	}
	app, err := os.ReadFile("app.go")
	if err != nil {
		t.Fatal(err)
	}
	// the case that dispatches and calls m.listen()
	re := regexp.MustCompile(`(?s)case (session\.[A-Za-z]+Msg,\s*)*session\.[A-Za-z]+Msg:\s*cmds = append\(cmds, m\.dispatch\(msg, false\)\.\.\.\)\s*cmds = append\(cmds, m\.listen\(\)\)`)
	block := re.Find(app)
	if block == nil {
		t.Fatal("cannot find the dispatch-and-listen case in app.go")
	}
	for _, ty := range types {
		if !strings.Contains(string(block), "session."+ty) {
			t.Errorf("session.%s is not in Update's dispatch-and-listen case", ty)
		}
	}
}
