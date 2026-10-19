package vsh

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fakeBin is the test shim that TestMain builds from testdata/fakeshim.
var fakeBin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "vsh-fake")
	if err != nil {
		panic(err)
	}
	fakeBin = filepath.Join(dir, "fakeshim")
	if runtime.GOOS == "windows" {
		fakeBin += ".exe"
	}
	out, err := exec.Command("go", "build", "-o", fakeBin, "./testdata/fakeshim").CombinedOutput()
	if err != nil {
		panic(fmt.Sprintf("build fakeshim: %v\n%s", err, out))
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func fakeShim() Shim {
	return Shim{Name: "fake", Binary: fakeBin, Allow: []Pattern{
		{"args", "[--flag {format}]", "{target}"},
		{"args", "-n", "{n}"},
		{"args", "--since", "{time}"},
		{"sleep"}, {"spam"}, {"child"}, {"exit", "{n}"},
	}}
}

// sandbox returns a Sandbox on a new scratch dir inside a parent dir that
// also holds a secret file next to it.
func sandbox(t *testing.T) Sandbox {
	t.Helper()
	parent := t.TempDir()
	dir := filepath.Join(parent, "scratch")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parent, "secret"), []byte("SECRET\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parent, "secret.json"), []byte(`{"k":"SECRET"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return Sandbox{
		Dir: dir, QuotaBytes: 1 << 20, OutputCap: 64 << 10, Timeout: 10 * time.Second,
		Shims: []Shim{fakeShim()}, Targets: []string{"web", "db-1"},
	}
}

func run(t *testing.T, sb Sandbox, script string) Result {
	t.Helper()
	res, err := Run(context.Background(), sb, script)
	if err != nil {
		t.Fatalf("Run(%q): %v", script, err)
	}
	return res
}

func write(t *testing.T, path, data string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestDialectRefused checks that scripts outside the dialect are refused as
// a whole: exit 126, one "vsh:" line, and nothing ran (no ran file).
func TestDialectRefused(t *testing.T) {
	scripts := []string{
		// eval, source, exec, cd
		"eval echo hi", "source x", ". x", "exec cat", "cd /", "cd ..",
		// background, coproc, time
		"echo hi &", "cat & echo", "coproc cat", "time echo hi",
		// substitutions and expansions
		"echo $(id)", "echo `id`", `echo "$(id)"`, "echo \"`id`\"", "cat <(echo hi)", "echo >(cat)",
		"echo $((1+1))", "echo ${x:-$(id)}", "echo $HOME", "echo ${IFS}", "echo $?", "echo \"$PATH\"",
		"echo {1..100000000}", "echo a{b,c}", "echo @(a|b)",
		// variables and environment
		"IFS=x", "PATH=/tmp fake sleep", "x=1", "export PATH=/x", "declare -x A=1", "local a", "readonly a",
		"typeset a", "let x=1", "(( 1 ))", "a[0]=1",
		// runner bypass
		"env fake sleep", "/usr/bin/env fake sleep", "command fake sleep", "builtin echo hi", `\fake sleep`,
		"'fake' sleep", `"echo" hi`, "/bin/echo hi", "./fake sleep", "fak? sleep", "fake\\ sleep",
		"nohup fake sleep", "xargs echo", "exec fake sleep",
		// definitions
		"alias ls=cat", "f() { echo; }", "function f { echo; }", "unalias ls",
		// here-docs and odd redirections
		"cat <<EOF\nhi\nEOF", "cat <<-EOF\n\thi\nEOF", "cat <<< hi", "echo hi >&3", "echo hi 3>x",
		"exec 3>x", "cat <> x", "echo hi >| x", "cat <&0", "echo hi >&-", "echo hi {fd}>x",
		// loops and other compound commands
		"while true; do :; done", "until false; do :; done", "for i in 1; do :; done", "for ((;;)); do :; done",
		"select x in a; do :; done", "case x in x) echo;; esac", "[[ -f x ]]",
		// commands that are not allowed
		"sh -c id", "bash", "python3 -c 1", "curl http://x", "wget x", "rm -rf /", "ls", "find /", "sudo id",
		"mkdir x", "cp a b", "podman ps", "trap id EXIT", "read x", "set -x", "shopt -s extglob", "type ls",
		"wait", "kill 1", "ulimit -a", "umask 0", "pushd /", "exit 0", "mapfile x", "getopts a b", "shift",
		// injection after an allowed command
		"echo hi; eval id", "echo hi\nid", "echo hi;id", "echo hi|id", "echo hi&&id", "true || $(id)",
		"if true; then id; fi", "{ echo; id; }", "(id)", "! id", "echo hi | (cat | id)",
	}
	for _, script := range scripts {
		t.Run(script, func(t *testing.T) {
			sb := sandbox(t)
			res := run(t, sb, "echo ran > ran\n"+script)
			if res.Exit != exitRefused {
				t.Errorf("exit = %d, want 126; stdout %q stderr %q", res.Exit, res.Stdout, res.Stderr)
			}
			if !strings.HasPrefix(res.Stderr, "vsh: ") || strings.Count(res.Stderr, "\n") != 1 {
				t.Errorf("stderr = %q, want one vsh: line", res.Stderr)
			}
			if _, err := os.Stat(filepath.Join(sb.Dir, "ran")); err == nil {
				t.Error("part of a refused script ran")
			}
		})
	}
}

func TestAllowed(t *testing.T) {
	tests := []struct{ script, want string }{
		{"echo hello", "hello\n"},
		{"printf '%s-%s\\n' a b", "a-b\n"},
		{"echo $'\\x41'", "A\n"},
		{"echo 'a b' | tr a-z A-Z", "A B\n"},
		{"true && echo yes || echo no", "yes\n"},
		{"false && echo yes || echo no", "no\n"},
		{"! false && echo ok", "ok\n"},
		{"echo a; echo b", "a\nb\n"},
		{"if false; then echo a; elif true; then echo b; else echo c; fi", "b\n"},
		{"{ echo a; echo b; } | sort -r", "b\na\n"},
		{"(echo sub)", "sub\n"},
		{"echo a > f; echo b >> f; cat f; cat < f", "a\nb\na\nb\n"},
		{"echo x > /dev/null; cat /dev/null; echo done", "done\n"},
		{"cat missing 2>/dev/null || echo failed", "failed\n"},
		{"echo err >&2 2>&1", ""},
		{"echo a > f; test -f f && echo yes; [ -e nope ] || echo no", "yes\nno\n"},
		{"echo a > a.txt; echo b > b.txt; echo *.txt", "a.txt b.txt\n"},
		{"echo 'quoted {braces}' \"and {more}\"", "quoted {braces} and {more}\n"},
		{": && echo colon", "colon\n"},
		{"echo hi | fake args web | grep -c '^argv='", "1\n"},
		{"jq -n -c env", "{}\n"},
		{"echo '{\"a\":[1,2]}' > j; jq -c '.a | map(. * 2)' j", "[2,4]\n"},
	}
	for _, tt := range tests {
		t.Run(tt.script, func(t *testing.T) {
			res := run(t, sandbox(t), tt.script)
			if res.Exit != 0 || res.Stdout != tt.want {
				t.Errorf("got exit %d stdout %q stderr %q, want %q", res.Exit, res.Stdout, res.Stderr, tt.want)
			}
		})
	}
}

// TestPathEscape tries to read or write outside the scratch dir. Nothing may
// show the secret or change files outside.
func TestPathEscape(t *testing.T) {
	sb := sandbox(t)
	parent := filepath.Dir(sb.Dir)
	secret := filepath.Join(parent, "secret")
	home, _ := os.UserHomeDir()
	scripts := []string{
		"cat /etc/passwd", "cat ../secret", "cat " + secret, "cat < ../secret", "cat < " + secret,
		"cat ./../secret", "cat sub/../../secret", "cat .//..//secret", "cat '../secret'", `cat ..\/secret`,
		"cat //etc/passwd", "cat /proc/self/environ", "cat /proc/self/root" + secret, "cat /dev/stdin",
		"cat link", "cat linkdir/secret", "cat < link", "head ../secret", "tail ../secret", "grep . ../secret",
		"sort ../secret", "uniq ../secret", "wc ../secret", "cut -c1- ../secret", "jq . ../secret.json",
		"jq . linkjson", "jq -n env", "jq -n '$ENV'", "cat ~/.deck/config.json", "cat ~root/.bashrc", "cat " + home,
		"echo ../*", "echo /*", "echo " + parent + "/*", "echo */../*", "echo ../s*", "cat ../s*",
		"test -e ../secret && echo leak-SECRET", "[ -r ../secret ] && echo leak-SECRET",
		"[ -e " + secret + " ] && echo leak-SECRET", "test -s link && echo leak-SECRET",
		"echo x > ../evil", "echo x > " + filepath.Join(parent, "evil"), "echo x >> link", "echo x > link",
		"echo x > linkdir/evil", "echo x > /dev/stdout", "echo x > /tmp/vsh-evil", "cat link > copy; cat copy",
		"fake args ../secret", "fake args --flag ../secret web",
	}
	for _, script := range scripts {
		t.Run(script, func(t *testing.T) {
			sb := sandbox(t)
			parent := filepath.Dir(sb.Dir)
			secret := filepath.Join(parent, "secret")
			if err := os.Symlink(secret, filepath.Join(sb.Dir, "link")); err != nil {
				t.Skip("no symlinks:", err)
			}
			_ = os.Symlink(parent, filepath.Join(sb.Dir, "linkdir"))
			_ = os.Symlink(filepath.Join(parent, "secret.json"), filepath.Join(sb.Dir, "linkjson"))
			_ = os.Mkdir(filepath.Join(sb.Dir, "sub"), 0o700)
			res := run(t, sb, script)
			if strings.Contains(res.Stdout+res.Stderr, "SECRET") {
				t.Errorf("secret leaked: stdout %q stderr %q", res.Stdout, res.Stderr)
			}
			if strings.Contains(res.Stdout, filepath.Base(secret)) && strings.Contains(script, "*") {
				t.Errorf("glob listed outside: %q", res.Stdout)
			}
			if b, _ := os.ReadFile(secret); string(b) != "SECRET\n" {
				t.Errorf("secret changed to %q", b)
			}
			for _, p := range []string{filepath.Join(parent, "evil"), "/tmp/vsh-evil"} {
				if _, err := os.Stat(p); err == nil {
					os.Remove(p)
					t.Errorf("%s was written", p)
				}
			}
		})
	}
}

// TestGlobBlowup checks that */../*/../... cannot grow as a power of the
// number of subdirs.
func TestGlobBlowup(t *testing.T) {
	sb := sandbox(t)
	for i := range 10 {
		if err := os.Mkdir(filepath.Join(sb.Dir, strconv.Itoa(i)), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	start := time.Now()
	res := run(t, sb, "echo */../*/../*/../*/../*/../*/../*/../*/../*")
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("took %v", took)
	}
	if !strings.Contains(res.Stderr, "globs read too many directories") || len(res.Stdout) > 1<<20 {
		t.Errorf("stdout %d bytes, stderr %q", len(res.Stdout), res.Stderr)
	}
}

// TestSymlinkInside checks that a symlink that stays inside the dir works.
func TestSymlinkInside(t *testing.T) {
	sb := sandbox(t)
	write(t, filepath.Join(sb.Dir, "data"), "inside\n")
	if err := os.Symlink("data", filepath.Join(sb.Dir, "ok")); err != nil {
		t.Skip("no symlinks:", err)
	}
	if res := run(t, sb, "cat ok"); res.Stdout != "inside\n" {
		t.Errorf("got %+v", res)
	}
}

func TestShimRun(t *testing.T) {
	sb := sandbox(t)
	res := run(t, sb, "echo input | fake args --flag '{{.Names}} {{.Status}}' web")
	if res.Exit != 0 {
		t.Fatalf("exit %d: %q", res.Exit, res.Stderr)
	}
	for _, want := range []string{
		`argv=["--flag","{{.Names}} {{.Status}}","web"]`,
		"cwd=" + sb.Dir,
		`env=["PATH=/usr/bin:/bin","LANG=C"]`,
		`stdin=""`,
	} {
		if !strings.Contains(res.Stdout, want) {
			t.Errorf("stdout %q lacks %q", res.Stdout, want)
		}
	}
	if res := run(t, sb, "fake exit 3"); res.Exit != 3 {
		t.Errorf("exit = %d, want 3", res.Exit)
	}
}

// TestShimPatterns checks argv matching. A refused shim command fails with
// 126 and the script goes on.
func TestShimPatterns(t *testing.T) {
	ok := []string{
		"fake args web", "fake args db-1", "fake args --flag '{{json .}}' web", "fake args -n 0",
		"fake args -n 100000", "fake args -n 005", "fake args --since 2026-10-19T10:00:00Z", "fake args --since 10m",
	}
	refused := []string{
		"fake args", "fake args web --extra", "fake args --extra web", "fake args web web", "fake args db",
		"fake args WEB", "fake args ''", "fake args 'web '", "fake args 'web;id'", "fake args 'web id'", `fake args web\;id`,
		"fake args -n 100001", "fake args -n 9999999", "fake args -n -1", "fake args -n +5", "fake args -n 1e5",
		"fake args -n ' 5'", "fake args -n 0x10", "fake args --flag web", "fake args --flag '$(id)' web",
		"fake args --flag 'a;b' web", "fake args --flag 'a`id`' web", "fake args --flag $'a\\nb' web",
		"fake args --flag '' web", "fake args --since '10m;id'", "fake args --since " + strings.Repeat("1", 41),
		"fake args --flag " + strings.Repeat("a", 257) + " web", "fake rm web", "fake", "fake sleep extra",
		"fake args --flag=x web",
	}
	sb := sandbox(t)
	for _, script := range ok {
		if res := run(t, sb, script); res.Exit != 0 {
			t.Errorf("%q: exit %d stderr %q", script, res.Exit, res.Stderr)
		}
	}
	for _, script := range refused {
		res := run(t, sb, script+" || echo next")
		if res.Stdout != "next\n" || !strings.Contains(res.Stderr, "not permitted by contract") {
			t.Errorf("%q: stdout %q stderr %q", script, res.Stdout, res.Stderr)
		}
		if res := run(t, sb, script); res.Exit != exitRefused {
			t.Errorf("%q: exit %d, want 126", script, res.Exit)
		}
	}
}

func TestPresetPatterns(t *testing.T) {
	podman := presets["podman-read"][0]
	podman.Binary = fakeBin
	systemctl := presets["systemd-read"][0]
	systemctl.Binary = fakeBin
	journalctl := presets["systemd-read"][1]
	journalctl.Binary = fakeBin
	targets := []string{"web"}
	tests := []struct {
		shim Shim
		argv string
		ok   bool
	}{
		{podman, "ps", true}, {podman, "ps -a --format {{.Names}}", true}, {podman, "logs --tail 50 web", true},
		{podman, "logs --timestamps --tail 50 web", true}, {podman, "inspect web", true},
		{podman, "inspect --format {{.State.Status}} web", true}, {podman, "stats --no-stream", true},
		{podman, "stats --no-stream web", true}, {podman, "top web", true},
		{podman, "events --since 10m --until 0m", true},
		{podman, "logs --tail 50 db", false}, {podman, "logs --tail 500000 web", false}, {podman, "logs -f web", false},
		{podman, "stats", false}, {podman, "events", false}, {podman, "events --since 10m", false},
		{podman, "rm web", false}, {podman, "run alpine", false}, {podman, "exec web sh", false}, {podman, "ps --all", false},
		{systemctl, "status web", true}, {systemctl, "status --no-pager web", true}, {systemctl, "is-active web", true},
		{systemctl, "list-units --type=service", true}, {systemctl, "stop web", false}, {systemctl, "status ssh", false},
		{systemctl, "list-units --type=socket", false},
		{journalctl, "-u web -n 100 --no-pager", true}, {journalctl, "-u web -n 100", false}, {journalctl, "-f", false},
	}
	for _, tt := range tests {
		c, err := compileShim(tt.shim)
		if err != nil {
			t.Fatal(err)
		}
		if got := c.match(strings.Fields(tt.argv), targets); got != tt.ok {
			t.Errorf("%s %s: match = %v, want %v", tt.shim.Name, tt.argv, got, tt.ok)
		}
	}
}

func TestNeverGrantable(t *testing.T) {
	dir := t.TempDir()
	link := func(name, to string) string {
		p := filepath.Join(dir, name)
		if err := os.Symlink(to, p); err != nil {
			t.Skip("no symlinks:", err)
		}
		return p
	}
	shims := []Shim{
		{Name: "bash", Binary: fakeBin},
		{Name: "npx", Binary: fakeBin},
		{Name: "tool", Binary: link("python3.12", fakeBin)},
		{Name: "tool", Binary: link("pip3", fakeBin)},
		{Name: "tool", Binary: link("curl", fakeBin)},
		{Name: "tool", Binary: link("SUDO", fakeBin)},
		{Name: "tool", Binary: link("node.exe", fakeBin)},
		{Name: "podman", Binary: fakeBin, Allow: []Pattern{{"rm", "{target}"}}},
		{Name: "tool", Binary: link("podman", fakeBin), Allow: []Pattern{{"[--log-level x]", "run", "{target}"}}},
		{Name: "docker", Binary: fakeBin, Allow: []Pattern{{"exec", "{target}", "ls"}}},
		{Name: "podman", Binary: fakeBin, Allow: []Pattern{{"[cp]"}}},
	}
	if _, err := os.Stat("/bin/sh"); err == nil {
		// The resolved binary counts too, not only the name of the link.
		shims = append(shims, Shim{Name: "tool", Binary: link("innocent", "/bin/sh")})
	}
	for _, s := range shims {
		sb := sandbox(t)
		sb.Shims = []Shim{s}
		_, err := Run(context.Background(), sb, "true")
		if err == nil || !strings.Contains(err.Error(), "never grantable") {
			t.Errorf("shim %s (%s %v): err = %v", s.Name, s.Binary, s.Allow, err)
		}
	}
}

func TestBadSandbox(t *testing.T) {
	base := sandbox(t)
	bad := map[string]func(*Sandbox){
		"relative dir":     func(sb *Sandbox) { sb.Dir = "scratch" },
		"missing dir":      func(sb *Sandbox) { sb.Dir = filepath.Join(sb.Dir, "nope") },
		"no timeout":       func(sb *Sandbox) { sb.Timeout = 0 },
		"duplicate shim":   func(sb *Sandbox) { sb.Shims = append(sb.Shims, fakeShim()) },
		"builtin name":     func(sb *Sandbox) { sb.Shims[0].Name = "cat" },
		"bad name":         func(sb *Sandbox) { sb.Shims[0].Name = "a b" },
		"relative binary":  func(sb *Sandbox) { sb.Shims[0].Binary = "fakeshim" },
		"missing binary":   func(sb *Sandbox) { sb.Shims[0].Binary = "/nonexistent/fakeshim" },
		"bad placeholder":  func(sb *Sandbox) { sb.Shims[0].Allow = []Pattern{{"{path}"}} },
		"bad group":        func(sb *Sandbox) { sb.Shims[0].Allow = []Pattern{{"[--a"}} },
		"empty group":      func(sb *Sandbox) { sb.Shims[0].Allow = []Pattern{{"[]"}} },
		"nested group":     func(sb *Sandbox) { sb.Shims[0].Allow = []Pattern{{"[a [b]]"}} },
		"brace in literal": func(sb *Sandbox) { sb.Shims[0].Allow = []Pattern{{"x{y}"}} },
	}
	for name, f := range bad {
		sb := base
		sb.Shims = []Shim{fakeShim()}
		f(&sb)
		if _, err := Run(context.Background(), sb, "true"); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestOutputCap(t *testing.T) {
	sb := sandbox(t)
	sb.OutputCap = 1000
	for _, script := range []string{"jq -n '[range(100000)]'", "fake spam | head -n 5000", "fake spam"} {
		sb.Timeout = 2 * time.Second
		res := run(t, sb, script)
		if len(res.Stdout)+len(res.Stderr) > sb.OutputCap+200 || len(res.Stdout) > sb.OutputCap || !res.Truncated {
			t.Errorf("%q: kept %d+%d bytes, truncated %v", script, len(res.Stdout), len(res.Stderr), res.Truncated)
		}
	}
	// A refusal line is kept even when the cap is used up.
	sb.OutputCap = 0
	if res := run(t, sb, "eval x"); !strings.Contains(res.Stderr, "eval") {
		t.Errorf("refusal lost: %+v", res)
	}
}

func TestQuota(t *testing.T) {
	sb := sandbox(t)
	sb.QuotaBytes = 100
	big := strings.Repeat("x", 150)
	res := run(t, sb, "printf "+big+" > f")
	if fi, err := os.Stat(filepath.Join(sb.Dir, "f")); err != nil || fi.Size() > 100 {
		t.Errorf("f: %v %v", fi, err)
	}
	_ = res
	// Appends stop at the quota, also from a shim.
	res = run(t, sb, "fake spam > out")
	if fi, err := os.Stat(filepath.Join(sb.Dir, "out")); err != nil || fi.Size() > 100 {
		t.Errorf("out: %v %v (%+v)", fi, err, res)
	}
	os.Remove(filepath.Join(sb.Dir, "out"))
	// Files already in the dir count, and truncating one gives its bytes back.
	write(t, filepath.Join(sb.Dir, "f"), strings.Repeat("y", 90))
	// echo ignores the write error; the write is dropped and Run says why.
	if res := run(t, sb, "echo 12345678901 >> f"); !strings.Contains(res.Stderr, "vsh: scratch quota exceeded") {
		t.Errorf("append past quota: %+v", res)
	}
	if fi, _ := os.Stat(filepath.Join(sb.Dir, "f")); fi.Size() != 90 {
		t.Errorf("f grew to %d bytes", fi.Size())
	}
	if res := run(t, sb, "echo small > f && cat f"); res.Stdout != "small\n" {
		t.Errorf("truncate: %+v", res)
	}
	res = run(t, sb, "echo 0123456789012345678901234567890123456789 > a; cat a a a > b")
	if res.Exit == 0 || !strings.Contains(res.Stderr, "quota") {
		t.Errorf("cat past quota: %+v", res)
	}
	var total int64
	entries, _ := os.ReadDir(sb.Dir)
	for _, e := range entries {
		fi, _ := e.Info()
		total += fi.Size()
	}
	if total > sb.QuotaBytes {
		t.Errorf("dir holds %d bytes, quota %d", total, sb.QuotaBytes)
	}
}

func TestTimeout(t *testing.T) {
	sb := sandbox(t)
	sb.Timeout = 300 * time.Millisecond
	for _, script := range []string{"fake sleep", "fake sleep | cat", "jq -n '[range(1e10)] | length'", "jq -n 'def f: f; f'"} {
		start := time.Now()
		res := run(t, sb, script)
		if took := time.Since(start); took > 5*time.Second {
			t.Errorf("%q took %v", script, took)
		}
		if res.Exit != exitTimeout || !strings.Contains(res.Stderr, "timed out") {
			t.Errorf("%q: %+v", script, res)
		}
	}
}

// TestTimeoutKillsChildren checks that the shim's whole process group dies.
func TestTimeoutKillsChildren(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("reads /proc")
	}
	sb := sandbox(t)
	sb.Timeout = 500 * time.Millisecond
	run(t, sb, "fake child")
	b, err := os.ReadFile(filepath.Join(sb.Dir, "child.pid"))
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := strconv.Atoi(string(b))
	deadline := time.Now().Add(3 * time.Second)
	for {
		stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		// Gone, or a zombie (state Z after the name) that nobody reaped yet.
		if err != nil || strings.Contains(string(stat), ") Z ") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("child %d still runs: %s", pid, stat)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestContextCancel(t *testing.T) {
	sb := sandbox(t)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(200*time.Millisecond, cancel)
	if _, err := Run(ctx, sb, "fake sleep"); err == nil {
		t.Error("no error after cancel")
	}
}

func TestPrintfLimits(t *testing.T) {
	sb := sandbox(t)
	for _, script := range []string{
		"printf '%099999999d' 1", "printf '%*d' 99999999 1", "printf '%.99999s' x", "printf -v x hi",
		"printf -- '%099999999d' 1", "echo " + strings.Repeat("x", maxScript),
	} {
		if res := run(t, sb, script); res.Exit != exitRefused {
			t.Errorf("%q: %+v", script, res)
		}
	}
	if res := run(t, sb, "printf '%5d|%%|%-3s|\\n' 42 a"); res.Stdout != "   42|%|a  |\n" {
		t.Errorf("printf: %q", res.Stdout)
	}
}

func TestPresetsResolve(t *testing.T) {
	for name, shims := range Presets() {
		sb := sandbox(t)
		sb.Shims = shims
		if _, err := Run(context.Background(), sb, "true"); err != nil {
			t.Errorf("preset %s: %v", name, err)
		}
	}
}

// TestPodmanLive runs the real podman when it is there. Only the refusals
// are certain; podman itself may fail without a service.
func TestPodmanLive(t *testing.T) {
	shims, ok := Presets()["podman-read"]
	if !ok {
		t.Skip("podman not found")
	}
	sb := sandbox(t)
	sb.Shims = shims
	if res := run(t, sb, "podman ps --format '{{.Names}}'"); res.Exit == exitRefused {
		t.Errorf("podman ps refused: %+v", res)
	}
	for _, script := range []string{"podman rm web", "podman run alpine", "podman ps --all; podman logs web"} {
		if res := run(t, sb, script); res.Exit != exitRefused {
			t.Errorf("%q: %+v", script, res)
		}
	}
}

func TestSystemdLive(t *testing.T) {
	shims, ok := Presets()["systemd-read"]
	if !ok {
		t.Skip("systemctl or journalctl not found")
	}
	sb := sandbox(t)
	sb.Shims = shims
	if res := run(t, sb, "systemctl is-active web"); res.Exit == exitRefused {
		t.Errorf("is-active refused: %+v", res)
	}
	if res := run(t, sb, "systemctl stop web"); res.Exit != exitRefused {
		t.Errorf("stop: %+v", res)
	}
}

func TestBuiltins(t *testing.T) {
	sb := sandbox(t)
	write(t, filepath.Join(sb.Dir, "in"), "b 2\na 10\nc 1\na 10\n")
	write(t, filepath.Join(sb.Dir, "csv"), "x,y,z\n1,2,3\nnodelim\n")
	tests := []struct{ script, want string }{
		{"cat in in | wc -l", "8\n"},
		{"wc in", "4 8 18 in\n"},
		{"wc -w < in", "8\n"},
		{"head -n 2 in", "b 2\na 10\n"},
		{"head -1 in", "b 2\n"},
		{"tail -n 1 in", "a 10\n"},
		{"tail -2 in", "c 1\na 10\n"},
		{"grep a in", "a 10\na 10\n"},
		{"grep -c a in", "2\n"},
		{"grep -v a in", "b 2\nc 1\n"},
		{"grep -n '^c' in", "3:c 1\n"},
		{"grep -i A in | head -n 1", "a 10\n"},
		{"grep -o '[0-9]+' in | head -n 2", "2\n10\n"},
		{"grep -F . in || echo none", "none\n"},
		{"grep -q b in && echo found", "found\n"},
		{"grep -l b in csv", "in\n"},
		{"grep -e 1 in csv", "in:a 10\nin:c 1\nin:a 10\ncsv:1,2,3\n"},
		{"sort in", "a 10\na 10\nb 2\nc 1\n"},
		{"sort -u in", "a 10\nb 2\nc 1\n"},
		{"sort -r in | head -n 1", "c 1\n"},
		{"sort -n -k 2 in", "c 1\nb 2\na 10\na 10\n"},
		{"sort -t , -k2 csv", "nodelim\n1,2,3\nx,y,z\n"},
		{"sort in | uniq", "a 10\nb 2\nc 1\n"},
		{"sort in | uniq -c", "      2 a 10\n      1 b 2\n      1 c 1\n"},
		{"cut -d , -f 2 csv", "y\n2\nnodelim\n"},
		{"cut -d, -f1,3- csv", "x,z\n1,3\nnodelim\n"},
		{"cut -c 1-2 csv", "x,\n1,\nno\n"},
		{"echo hello | tr -d l", "heo\n"},
		{"echo 'a  b   c' | tr -s ' '", "a b c\n"},
		{"echo Hello | tr '[:upper:]' '[:lower:]'", "hello\n"},
		{"echo abc | tr a-c x", "xxx\n"},
		{"printf 'a\\nb\\n' | tr '\\n' ,", "a,b,"},
		{"jq -r '.k' < /dev/null; echo end", "null\nend\n"},
		{"echo '[3,1,2]' | jq -c sort", "[1,2,3]\n"},
		{"echo '{\"a\":\"x\"}' | jq -r --arg k a '.[$k]'", "x\n"},
		{"cat - < in | head -n 1", "b 2\n"},
	}
	for _, tt := range tests {
		res := run(t, sb, tt.script)
		if res.Stdout != tt.want {
			t.Errorf("%q: stdout %q, want %q (exit %d, stderr %q)", tt.script, res.Stdout, tt.want, res.Exit, res.Stderr)
		}
	}
	bad := map[string]int{
		"grep '(' in":       2,
		"grep zzz in":       1,
		"head -n x in":      1,
		"sort -k 0 in":      1,
		"cut in":            1,
		"cut -f 3-1 in":     1,
		"tr":                1,
		"uniq in csv":       1,
		"wc -z in":          1,
		"cat nope":          1,
		"jq -n 'error(1)'":  5,
		"jq . in":           2,
		"jq -n --argjson":   2,
		"cat in | jq '.a('": 3,
	}
	for script, exit := range bad {
		if res := run(t, sb, script); res.Exit != exit {
			t.Errorf("%q: exit %d, want %d (stderr %q)", script, res.Exit, exit, res.Stderr)
		}
	}
}

// TestInputCap checks that a builtin stops at 64 MiB of input.
func TestInputCap(t *testing.T) {
	sb := sandbox(t)
	sb.Timeout = time.Minute
	res := run(t, sb, "fake spam | sort | wc -l")
	if !strings.Contains(res.Stderr, "input is larger than 64 MiB") {
		t.Errorf("got %+v", res)
	}
}
