package hub

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type fakeBrowser struct {
	ops  []string
	args []map[string]any
	err  error
}

func (f *fakeBrowser) BrowserOp(_ context.Context, op string, a map[string]any) (string, error) {
	f.ops, f.args = append(f.ops, op), append(f.args, a)
	return "ok " + op, f.err
}

func browserEnv(t *testing.T) (Env, *fakeBrowser) {
	env := testEnv(t)
	fb := &fakeBrowser{}
	env.Browser = fb
	return env, fb
}

func TestBrowserOpsAreCataloged(t *testing.T) {
	want := []string{
		"browser.open", "browser.tabs", "browser.snapshot", "browser.click", "browser.type", "browser.press", "browser.scroll",
		"browser.select", "browser.wait", "browser.back", "browser.forward", "browser.reload", "browser.eval", "browser.screenshot", "browser.close",
	}
	have := map[string]Op{}
	for _, o := range Ops() {
		have[o.Name] = o
	}
	for _, name := range want {
		if o, ok := have[name]; !ok || o.run == nil || o.Desc == "" {
			t.Errorf("op %s missing or has no runner", name)
		}
	}
	// A search for the browser finds the ops.
	out := Search(testEnv(t), []byte(`{"query":"browser click page"}`))
	if !strings.Contains(out, "browser.click {ref*") {
		t.Errorf("search does not show browser.click:\n%s", out)
	}
	// An ops contract's agent has none of them.
	env := testEnv(t)
	env.Ops = &fakeOpsRunner{}
	if got, isErr := run(t, env, "browser.tabs", ""); !isErr || !strings.Contains(got, "Unknown op") {
		t.Errorf("an ops agent ran browser.tabs: %q %v", got, isErr)
	}
}

type fakeOpsRunner struct{}

func (*fakeOpsRunner) ShRun(context.Context, string) (string, error) { return "", nil }
func (*fakeOpsRunner) Deliver(json.RawMessage) (string, error)       { return "", nil }

func TestBrowserOpRoutes(t *testing.T) {
	env, fb := browserEnv(t)
	cases := []struct{ op, args string }{
		{"browser.open", `{"url":"https://example.com"}`},
		{"browser.tabs", ``},
		{"browser.snapshot", `{"interactive":true,"max":"100"}`},
		{"browser.click", `{"ref":"e12","tab":"t2"}`},
		{"browser.type", `{"ref":"e3","text":"hello","submit":"true"}`},
		{"browser.press", `{"key":"Control+A"}`},
		{"browser.scroll", `{"dy":-400}`},
		{"browser.select", `{"ref":"e5","value":"UK"}`},
		{"browser.wait", `{"text":"Welcome"}`},
		{"browser.eval", `{"js":"document.title"}`},
		{"browser.close", `{}`},
	}
	for _, c := range cases {
		out, isErr := run(t, env, c.op, c.args)
		if isErr || out != "ok "+c.op {
			t.Errorf("%s %s = %q, %v", c.op, c.args, out, isErr)
		}
	}
	if len(fb.ops) != len(cases) {
		t.Fatalf("browser got %v", fb.ops)
	}
	if a := fb.args[2]; a["interactive"] != true || a["max"] != 100 {
		t.Errorf("snapshot args = %v", a)
	}
	if a := fb.args[3]; a["ref"] != "e12" || a["tab"] != "t2" {
		t.Errorf("click args = %v", a)
	}
}

func TestBrowserOpValidation(t *testing.T) {
	env, fb := browserEnv(t)
	bad := []struct{ op, args, want string }{
		{"browser.open", ``, `missing argument "url"`},
		{"browser.click", `{"ref":"12"}`, "not an element ref"},
		{"browser.click", `{"ref":"e1; drop"}`, "not an element ref"},
		{"browser.click", `{"ref":"e1","tab":"main"}`, "not a tab id"},
		{"browser.click", `{}`, `missing argument "ref"`},
		{"browser.type", `{"ref":"e1"}`, `missing argument "text"`},
		{"browser.type", `{"ref":"e1","text":"` + strings.Repeat("x", maxBrowserText+1) + `"}`, "at most"},
		{"browser.press", `{"key":"Enter; rm -rf"}`, "not a key"},
		{"browser.press", `{"key":""}`, "is empty"},
		{"browser.scroll", `{}`, "scroll needs"},
		{"browser.scroll", `{"to":"middle"}`, `"top" or "bottom"`},
		{"browser.scroll", `{"dy":999999}`, "within"},
		{"browser.wait", `{}`, "wait needs"},
		{"browser.wait", `{"ms":60000}`, "between 0 and"},
		{"browser.wait", `{"text":"x","timeout":900}`, "between 1 and"},
		{"browser.snapshot", `{"max":5}`, "between 10 and"},
		{"browser.eval", `{"js":"` + strings.Repeat("a", maxBrowserText+1) + `"}`, "at most"},
		{"browser.eval", `{"javascript":"1"}`, "unknown argument"},
	}
	for _, c := range bad {
		out, isErr := run(t, env, c.op, c.args)
		if !isErr || !strings.Contains(out, c.want) {
			t.Errorf("%s %.60s = %q, %v; want an error with %q", c.op, c.args, out, isErr, c.want)
		}
	}
	if len(fb.ops) != 0 {
		t.Errorf("bad calls reached the browser: %v", fb.ops)
	}
	for _, k := range []string{"Enter", "Tab", "Escape", "ArrowDown", "Control+A", "Shift+Tab", "a", "F5", "Meta+Shift+R", "/"} {
		if !keyRe.MatchString(k) {
			t.Errorf("key %q refused", k)
		}
	}
}

func TestBrowserOpErrors(t *testing.T) {
	env := testEnv(t)
	if out, isErr := run(t, env, "browser.tabs", ""); !isErr || !strings.Contains(out, "not available") {
		t.Errorf("without a browser: %q %v", out, isErr)
	}
	env, fb := browserEnv(t)
	fb.err = errors.New("no browser attached: open Deck's desktop app")
	if out, isErr := run(t, env, "browser.tabs", ""); !isErr || !strings.Contains(out, "no browser attached") {
		t.Errorf("browser error: %q %v", out, isErr)
	}
}
