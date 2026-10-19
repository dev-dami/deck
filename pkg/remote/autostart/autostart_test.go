package autostart

import (
	"context"
	"encoding/xml"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
)

type call struct{ name, args string }

type fakeRunner struct {
	calls []call
	// fail maps "name arg0" prefixes to the error and output to return.
	fail map[string]string
}

func (f *fakeRunner) Run(_ context.Context, name string, args ...string) (string, error) {
	line := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, call{name, strings.Join(args, " ")})
	for prefix, out := range f.fail {
		if strings.HasPrefix(line, prefix) {
			return out, errors.New("exit status 1")
		}
	}
	return "", nil
}

func (f *fakeRunner) ran(prefix string) bool {
	for _, c := range f.calls {
		if strings.HasPrefix(c.name+" "+c.args, prefix) {
			return true
		}
	}
	return false
}

var spec = Spec{Exe: filepath.FromSlash("/opt/deck/deck"), Args: []string{"mobile", "daemon"}, LogPath: "/var/log/deck mobile.log"}

func TestTaskXMLIsWellFormedAndRunsOnlyAtLogonWithoutElevation(t *testing.T) {
	x := TaskXML(Spec{Exe: `C:\Program Files\Deck\deck.exe`, Args: []string{"mobile", "daemon", "--detach"}}, `PC\erio & co`)
	var doc struct {
		Triggers struct {
			Logon []struct {
				UserID string `xml:"UserId"`
			} `xml:"LogonTrigger"`
		}
		Principals struct {
			Principal struct{ RunLevel, LogonType string }
		}
		Settings struct {
			ExecutionTimeLimit string
			Restart            struct{ Interval, Count string } `xml:"RestartOnFailure"`
		}
		Actions struct {
			Exec struct{ Command, Arguments string }
		}
	}
	// encoding/xml refuses UTF-16 declarations without a charset reader; the
	// body is what matters here.
	body := strings.Replace(x, `encoding="UTF-16"`, `encoding="UTF-8"`, 1)
	if err := xml.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("not well-formed: %v\n%s", err, x)
	}
	if len(doc.Triggers.Logon) != 1 || doc.Triggers.Logon[0].UserID != `PC\erio & co` {
		t.Fatalf("trigger %+v (the user name must survive escaping)", doc.Triggers)
	}
	if doc.Principals.Principal.RunLevel != "LeastPrivilege" || doc.Principals.Principal.LogonType != "InteractiveToken" {
		t.Fatalf("principal %+v", doc.Principals.Principal)
	}
	if doc.Settings.ExecutionTimeLimit != "PT0S" || doc.Settings.Restart.Count != "3" || doc.Settings.Restart.Interval != "PT1M" {
		t.Fatalf("settings %+v", doc.Settings)
	}
	if doc.Actions.Exec.Command != `C:\Program Files\Deck\deck.exe` || doc.Actions.Exec.Arguments != "mobile daemon --detach" {
		t.Fatalf("action %+v", doc.Actions.Exec)
	}
}

func TestWindowsArgQuoting(t *testing.T) {
	for _, c := range []struct {
		in   []string
		want string
	}{
		{[]string{"mobile", "daemon"}, "mobile daemon"},
		{[]string{"--cwd", `C:\My Projects\a`}, `--cwd "C:\My Projects\a"`},
		{[]string{`say "hi"`}, `"say \"hi\""`},
		{[]string{""}, `""`},
	} {
		if got := windowsArgs(c.in); got != c.want {
			t.Errorf("windowsArgs(%q) = %s, want %s", c.in, got, c.want)
		}
	}
}

func TestUTF16LEHasABOMAndRoundTrips(t *testing.T) {
	in := "<Task>é✻</Task>"
	b := utf16LE(in)
	if b[0] != 0xff || b[1] != 0xfe {
		t.Fatal("missing byte order mark")
	}
	units := make([]uint16, 0, len(b)/2)
	for i := 2; i+1 < len(b); i += 2 {
		units = append(units, uint16(b[i])|uint16(b[i+1])<<8)
	}
	if got := string(utf16.Decode(units)); got != in {
		t.Fatalf("round trip %q", got)
	}
}

func TestWindowsInstallPrefersTheTaskAndClearsAStaleRunKey(t *testing.T) {
	r := &fakeRunner{}
	m, err := Installer{Platform: "windows", Runner: r}.Install(context.Background(), spec)
	if err != nil || m != MethodTask {
		t.Fatalf("%v %v", m, err)
	}
	if !r.ran("schtasks /Create /TN Deck Mobile /XML") || !strings.HasSuffix(r.calls[0].args, "/F") {
		t.Fatalf("calls %v", r.calls)
	}
	if !r.ran("reg delete") {
		t.Fatal("a leftover Run key would start a second daemon")
	}
}

func TestWindowsFallsBackToTheRunKeyWhenTasksAreBlocked(t *testing.T) {
	r := &fakeRunner{fail: map[string]string{"schtasks /Create": "ERROR: Access is denied."}}
	m, err := Installer{Platform: "windows", Runner: r}.Install(context.Background(), spec)
	if err != nil || m != MethodRunKey {
		t.Fatalf("%v %v", m, err)
	}
	last := r.calls[len(r.calls)-1]
	if last.name != "reg" || !strings.Contains(last.args, "add") || !strings.Contains(last.args, `"`+spec.Exe+`" mobile daemon`) {
		t.Fatalf("run key command %v", last)
	}
}

func TestWindowsInstallReportsWhenNothingWorks(t *testing.T) {
	r := &fakeRunner{fail: map[string]string{"schtasks /Create": "denied", "reg add": "denied"}}
	if m, err := (Installer{Platform: "windows", Runner: r}).Install(context.Background(), spec); err == nil || m != MethodNone {
		t.Fatalf("%v %v", m, err)
	}
}

func TestWindowsUninstallToleratesMissingEntries(t *testing.T) {
	r := &fakeRunner{fail: map[string]string{
		"schtasks /Delete": "ERROR: The system cannot find the file specified.",
		"reg delete":       "ERROR: The system was unable to find the specified registry key or value.",
	}}
	if err := (Installer{Platform: "windows", Runner: r}).Uninstall(context.Background()); err != nil {
		t.Fatalf("removing what is not there: %v", err)
	}
	r = &fakeRunner{fail: map[string]string{"schtasks /Delete": "ERROR: Access is denied."}}
	if err := (Installer{Platform: "windows", Runner: r}).Uninstall(context.Background()); err == nil {
		t.Fatal("a real failure was swallowed")
	}
}

func TestWindowsStatus(t *testing.T) {
	ctx := context.Background()
	if m, _ := (Installer{Platform: "windows", Runner: &fakeRunner{}}).Status(ctx); m != MethodTask {
		t.Fatalf("task: %v", m)
	}
	r := &fakeRunner{fail: map[string]string{"schtasks /Query": "x"}}
	if m, _ := (Installer{Platform: "windows", Runner: r}).Status(ctx); m != MethodRunKey {
		t.Fatalf("run key: %v", m)
	}
	r = &fakeRunner{fail: map[string]string{"schtasks /Query": "x", "reg query": "x"}}
	if m, _ := (Installer{Platform: "windows", Runner: r}).Status(ctx); m != MethodNone {
		t.Fatalf("none: %v", m)
	}
}

func TestPlistRunsAtLoadAndKeepsAlive(t *testing.T) {
	var doc struct {
		Dict struct {
			Keys   []string `xml:"key"`
			Values []string `xml:"string"`
		} `xml:"dict"`
	}
	p := Plist(Spec{Exe: "/Applications/Deck & Co/deck", Args: []string{"mobile", "daemon"}, LogPath: "/tmp/d.log"})
	if err := xml.Unmarshal([]byte(strings.Replace(p, "<!DOCTYPE", "<!-- ", 1)[0:0]+stripDoctype(p)), &doc); err != nil {
		t.Fatalf("not well-formed: %v\n%s", err, p)
	}
	for _, want := range []string{"<key>RunAtLoad</key>\n  <true/>", "<key>KeepAlive</key>\n  <true/>",
		"<string>/Applications/Deck &amp; Co/deck</string>", "<string>mobile</string>", "<string>daemon</string>",
		"<string>" + launchdID + "</string>", "/tmp/d.log"} {
		if !strings.Contains(p, want) {
			t.Errorf("plist lacks %q:\n%s", want, p)
		}
	}
}

func stripDoctype(s string) string {
	i := strings.Index(s, "<!DOCTYPE")
	j := strings.Index(s[i:], ">")
	return s[:i] + s[i+j+1:]
}

func TestDarwinInstallWritesThePlistAndBootstraps(t *testing.T) {
	home := t.TempDir()
	r := &fakeRunner{}
	m, err := Installer{Platform: "darwin", Home: home, UID: 501, Runner: r}.Install(context.Background(), spec)
	if err != nil || m != MethodLaunchd {
		t.Fatalf("%v %v", m, err)
	}
	path := filepath.Join(home, "Library", "LaunchAgents", launchdID+".plist")
	raw, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(raw), "<string>daemon</string>") {
		t.Fatalf("plist: %v", err)
	}
	if !r.ran("launchctl bootout gui/501/"+launchdID) || !r.ran("launchctl bootstrap gui/501 "+path) {
		t.Fatalf("calls %v", r.calls)
	}
	if got, _ := (Installer{Platform: "darwin", Home: home}).Status(context.Background()); got != MethodLaunchd {
		t.Fatalf("status %v", got)
	}
	if err := (Installer{Platform: "darwin", Home: home, UID: 501, Runner: r}).Uninstall(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("plist left behind")
	}
	if err := (Installer{Platform: "darwin", Home: home, UID: 501, Runner: r}).Uninstall(context.Background()); err != nil {
		t.Fatalf("second uninstall: %v", err)
	}
}

func TestUnitQuotesAndInstalls(t *testing.T) {
	u := Unit(Spec{Exe: "/home/e rio/bin/deck", Args: []string{"mobile", "daemon"}, LogPath: "/tmp/deck.log"})
	for _, want := range []string{`ExecStart="/home/e rio/bin/deck" mobile daemon`, "Restart=on-failure", "WantedBy=default.target", "append:/tmp/deck.log"} {
		if !strings.Contains(u, want) {
			t.Errorf("unit lacks %q:\n%s", want, u)
		}
	}
	if !strings.Contains(Unit(Spec{Exe: "/a/50%/deck"}), "50%%") {
		t.Error("a % in a path must be doubled for systemd")
	}

	home := t.TempDir()
	r := &fakeRunner{}
	m, err := Installer{Platform: "linux", Home: home, Runner: r}.Install(context.Background(), spec)
	if err != nil || m != MethodSystemd {
		t.Fatalf("%v %v", m, err)
	}
	if !r.ran("systemctl --user daemon-reload") || !r.ran("systemctl --user enable --now "+unitName) {
		t.Fatalf("calls %v", r.calls)
	}
	if err := (Installer{Platform: "linux", Home: home, Runner: r}).Uninstall(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, _ := (Installer{Platform: "linux", Home: home}).Status(context.Background()); got != MethodNone {
		t.Fatalf("status after uninstall: %v", got)
	}
}

func TestLinuxInstallFailureIsReported(t *testing.T) {
	r := &fakeRunner{fail: map[string]string{"systemctl --user enable": "Failed to connect to bus"}}
	if _, err := (Installer{Platform: "linux", Home: t.TempDir(), Runner: r}).Install(context.Background(), spec); err == nil ||
		!strings.Contains(err.Error(), "Failed to connect to bus") {
		t.Fatalf("got %v", err)
	}
}

func TestRelativePathsAndUnknownPlatforms(t *testing.T) {
	if _, err := (Installer{Platform: "linux"}).Install(context.Background(), Spec{Exe: "deck"}); err == nil {
		t.Fatal("a relative executable path was accepted")
	}
	if _, err := (Installer{Platform: "plan9"}).Install(context.Background(), spec); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("got %v", err)
	}
}
