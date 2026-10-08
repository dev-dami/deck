package doctor

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type result struct {
	out  string
	code int
	err  error
}

// fakeRunner stands in for the real CLIs. Commands are keyed by "path arg1 arg2".
// Any command without a scripted result exits 127, like a missing command.
type fakeRunner struct {
	paths   map[string]string
	results map[string]result
	calls   []string
}

func (f *fakeRunner) LookPath(name string) (string, error) {
	if p, ok := f.paths[name]; ok {
		return p, nil
	}
	return "", errors.New("not found")
}

func (f *fakeRunner) Run(_ context.Context, path string, args ...string) (string, int, error) {
	key := strings.Join(append([]string{path}, args...), " ")
	f.calls = append(f.calls, key)
	if r, ok := f.results[key]; ok {
		return r.out, r.code, r.err
	}
	return "", 127, nil
}

func baseOptions(t *testing.T, r *fakeRunner) Options {
	t.Helper()
	return Options{
		Runner:       r,
		ClaudeBinary: "claude",
		CodexBinary:  "codex",
		ConfigPath:   filepath.Join(t.TempDir(), "absent", "config.json"),
		ThreadsDir:   t.TempDir(),
		Timeout:      0,
		Terminal:     func() (int, int, bool) { return 120, 40, true },
	}
}

func runReport(t *testing.T, opts Options) string {
	t.Helper()
	var buf bytes.Buffer
	if err := RunWith(context.Background(), &buf, opts); err != nil {
		t.Fatalf("RunWith: %v", err)
	}
	return buf.String()
}

func TestSignedInBothAndNoIdentifiersPrinted(t *testing.T) {
	r := &fakeRunner{
		paths: map[string]string{"claude": "/bin/claude", "codex": "/bin/codex"},
		results: map[string]result{
			"/bin/claude --version":   {out: "2.1.285 (Claude Code)\n"},
			"/bin/claude --help":      {out: "Commands:\n  auth  Manage authentication\n"},
			"/bin/claude auth status": {out: "Logged in as dami@example.com\n"},
			"/bin/codex --version":    {out: "codex-cli 0.9.1\n"},
			"/bin/codex --help":       {out: "Commands:\n  login  Manage login\n"},
			"/bin/codex login status": {out: "Logged in using ChatGPT account jane@example.org\n"},
		},
	}
	out := runReport(t, baseOptions(t, r))

	for _, want := range []string{
		"✓ claude  2.1.285  signed in",
		"✓ codex   0.9.1  signed in",
		"deck doctor",
		"120×40, interactive",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report missing %q:\n%s", want, out)
		}
	}
	for _, leak := range []string{"@", "dami", "jane", "example", "ChatGPT", "Logged in"} {
		if strings.Contains(out, leak) {
			t.Errorf("report leaks status output (%q):\n%s", leak, out)
		}
	}

	wantCalls := []string{
		"/bin/claude --version",
		"/bin/claude --help",
		"/bin/claude auth status",
		"/bin/codex --version",
		"/bin/codex --help",
		"/bin/codex login status",
	}
	if strings.Join(r.calls, "|") != strings.Join(wantCalls, "|") {
		t.Errorf("commands run:\n got %v\nwant %v", r.calls, wantCalls)
	}
}

func TestMissingCLIShowsInstallHint(t *testing.T) {
	r := &fakeRunner{
		paths: map[string]string{"claude": "/bin/claude"},
		results: map[string]result{
			"/bin/claude --version":   {out: "2.1.285\n"},
			"/bin/claude --help":      {out: "auth\n"},
			"/bin/claude auth status": {code: 0},
		},
	}
	out := runReport(t, baseOptions(t, r))
	if !strings.Contains(out, "✗ codex   not found — install: npm install -g @openai/codex") {
		t.Errorf("missing codex install hint:\n%s", out)
	}
	for _, c := range r.calls {
		if strings.HasPrefix(c, "codex") || strings.HasPrefix(c, "/bin/codex") {
			t.Errorf("ran a command for a CLI that is not installed: %s", c)
		}
	}
}

func TestNotSignedInPrintsCLIHint(t *testing.T) {
	r := &fakeRunner{
		paths: map[string]string{"claude": "/bin/claude", "codex": "/bin/codex"},
		results: map[string]result{
			"/bin/claude --version":   {out: "2.1.285\n"},
			"/bin/claude --help":      {out: "auth\n"},
			"/bin/claude auth status": {out: "Not logged in · Run /login to sign in\n", code: 1},
			"/bin/codex --version":    {out: "codex-cli 0.9.1\n"},
			"/bin/codex --help":       {out: "login\n"},
			"/bin/codex login status": {out: "dami@example.com has no session\nRun codex login to sign in\n", code: 1},
		},
	}
	out := runReport(t, baseOptions(t, r))
	if !strings.Contains(out, "✗ claude  2.1.285  not signed in — Not logged in · Run /login to sign in") {
		t.Errorf("claude hint missing:\n%s", out)
	}
	if !strings.Contains(out, "✗ codex   0.9.1  not signed in — Run codex login to sign in") {
		t.Errorf("codex should skip the line with an address and use the next hint:\n%s", out)
	}
	if strings.Contains(out, "dami") || strings.Contains(out, "example.com") {
		t.Errorf("account address leaked:\n%s", out)
	}
}

func TestNonZeroExitWithoutHintIsNotSignedIn(t *testing.T) {
	r := &fakeRunner{
		paths: map[string]string{"claude": "/bin/claude", "codex": "/bin/codex"},
		results: map[string]result{
			"/bin/claude --version":   {out: "2.1.285\n"},
			"/bin/claude --help":      {out: "auth\n"},
			"/bin/claude auth status": {out: "error\n", code: 2},
			"/bin/codex --version":    {out: "codex-cli 0.9.1\n"},
			"/bin/codex --help":       {out: "login\n"},
			"/bin/codex login status": {code: 1},
		},
	}
	out := runReport(t, baseOptions(t, r))
	if !strings.Contains(out, "✗ claude  2.1.285  not signed in\n") {
		t.Errorf("expected bare not-signed-in line:\n%s", out)
	}
}

func TestStatusCommandAbsentIsNotRun(t *testing.T) {
	r := &fakeRunner{
		paths: map[string]string{"claude": "/bin/claude", "codex": "/bin/codex"},
		results: map[string]result{
			"/bin/claude --version": {out: "2.1.285\n"},
			"/bin/claude --help":    {out: "Commands:\n  doctor  Check the install\n"},
			"/bin/codex --version":  {out: "codex-cli 0.9.1\n"},
			"/bin/codex --help":     {out: "Commands:\n  exec  Run\n"},
		},
	}
	out := runReport(t, baseOptions(t, r))
	if !strings.Contains(out, "! claude  2.1.285  sign-in status not available in this version") {
		t.Errorf("claude without auth command:\n%s", out)
	}
	for _, c := range r.calls {
		if strings.HasSuffix(c, "auth status") || strings.HasSuffix(c, "login status") {
			t.Errorf("status command ran although --help did not list it: %s", c)
		}
	}
}

func TestVersionFailureStillReportsSignIn(t *testing.T) {
	r := &fakeRunner{
		paths: map[string]string{"claude": "/bin/claude"},
		results: map[string]result{
			"/bin/claude --version":   {err: errors.New("boom")},
			"/bin/claude --help":      {out: "auth\n"},
			"/bin/claude auth status": {code: 0},
		},
	}
	out := runReport(t, baseOptions(t, r))
	if !strings.Contains(out, "✓ claude  version unknown  signed in") {
		t.Errorf("version failure line:\n%s", out)
	}
}

func TestConfigAndThreadsLines(t *testing.T) {
	r := &fakeRunner{paths: map[string]string{}}
	opts := baseOptions(t, r)
	out := runReport(t, opts)

	if !strings.Contains(out, "(not created; defaults in use)") {
		t.Errorf("missing config should say defaults are in use:\n%s", out)
	}
	if !strings.Contains(out, opts.ThreadsDir) {
		t.Errorf("threads dir not shown:\n%s", out)
	}

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(cfgPath, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	opts.ConfigPath = cfgPath
	if out := runReport(t, opts); !strings.Contains(out, cfgPath) || strings.Contains(out, "not created") {
		t.Errorf("existing config line:\n%s", out)
	}
}

func TestInvalidConfigIsReported(t *testing.T) {
	opts := baseOptions(t, &fakeRunner{paths: map[string]string{}})
	opts.configErr = errors.New("config: motion must be on or off")
	out := runReport(t, opts)
	if !strings.Contains(out, "invalid: config: motion must be on or off") {
		t.Errorf("invalid config not reported:\n%s", out)
	}
}

func TestTerminalLines(t *testing.T) {
	opts := baseOptions(t, &fakeRunner{paths: map[string]string{}})

	opts.Terminal = func() (int, int, bool) { return 0, 0, false }
	if out := runReport(t, opts); !strings.Contains(out, "not a terminal (output is piped)") {
		t.Errorf("piped output line:\n%s", out)
	}

	opts.Terminal = func() (int, int, bool) { return 0, 0, true }
	if out := runReport(t, opts); !strings.Contains(out, "interactive, size unknown") {
		t.Errorf("unknown size line:\n%s", out)
	}
}

func TestLoginHintSkipsAddressesAndTruncates(t *testing.T) {
	if got := loginHint("me@host.test\nnothing useful"); got != "" {
		t.Errorf("hint = %q, want none", got)
	}
	long := "Please log in " + strings.Repeat("x", 300)
	got := loginHint(long)
	if n := len([]rune(got)); n != maxHintRunes {
		t.Errorf("hint has %d runes, want %d", n, maxHintRunes)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("truncated hint %q lacks ellipsis", got)
	}
}
