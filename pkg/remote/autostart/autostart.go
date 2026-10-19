// Package autostart makes the Deck Mobile daemon start when its user logs in
// (docs/mobile-contract.md, plan §6): a per-user Scheduled Task on Windows (a
// Run key if tasks are blocked), a LaunchAgent on macOS, a systemd user unit
// on Linux. It never needs administrator rights.
//
// The files and commands are built by pure functions so they can be checked
// without touching the machine; Install and Uninstall run them through a
// Runner.
package autostart

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf16"
)

// Names the entries go by.
const (
	taskName   = "Deck Mobile"
	runKeyName = "DeckMobile"
	launchdID  = "dev.deck.mobile"
	unitName   = "deck-mobile.service"
	runKeyPath = `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`
)

// Spec says what to start.
type Spec struct {
	// Exe is the absolute path of the deck binary.
	Exe string
	// Args follow Exe; for the daemon, ["mobile", "daemon"] plus "--detach" on
	// Windows so no console window stays open.
	Args []string
	// LogPath receives the daemon's output where the platform allows it.
	LogPath string
}

// Runner runs a command and returns its combined output.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) (string, error)
}

// ExecRunner runs real commands.
type ExecRunner struct{}

// Run implements Runner.
func (ExecRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	var out bytes.Buffer
	c := exec.CommandContext(ctx, name, args...)
	c.Stdout, c.Stderr = &out, &out
	err := c.Run()
	return strings.TrimSpace(out.String()), err
}

// Installer installs for one platform.
type Installer struct {
	// Platform is a runtime.GOOS value; empty means this machine.
	Platform string
	// Home is the user's home directory; empty means os.UserHomeDir().
	Home string
	// UID is the numeric user id for launchd's gui domain.
	UID    int
	Runner Runner
}

// Method names how an installation was done.
type Method string

// Methods.
const (
	MethodTask    Method = "scheduled task"
	MethodRunKey  Method = "run key"
	MethodLaunchd Method = "launch agent"
	MethodSystemd Method = "systemd user unit"
	MethodNone    Method = ""
)

// ErrUnsupported is returned on a platform with no autostart support.
var ErrUnsupported = errors.New("autostart is not supported on this platform")

func (i Installer) platform() string {
	if i.Platform != "" {
		return i.Platform
	}
	return runtime.GOOS
}

func (i Installer) home() (string, error) {
	if i.Home != "" {
		return i.Home, nil
	}
	return os.UserHomeDir()
}

func (i Installer) runner() Runner {
	if i.Runner != nil {
		return i.Runner
	}
	return ExecRunner{}
}

func (i Installer) uid() int {
	if i.UID != 0 {
		return i.UID
	}
	return os.Getuid()
}

// isAbs is filepath.IsAbs for either platform's paths, since the entry may be
// built for a platform other than the one running (and by tests).
func isAbs(p string) bool {
	if strings.HasPrefix(p, "/") || strings.HasPrefix(p, `\`) {
		return true
	}
	return len(p) >= 3 && p[1] == ':' && (p[2] == '\\' || p[2] == '/') &&
		(p[0] >= 'a' && p[0] <= 'z' || p[0] >= 'A' && p[0] <= 'Z')
}

// Install registers the daemon to start at login and reports how. It is
// idempotent: running it again replaces the entry.
func (i Installer) Install(ctx context.Context, s Spec) (Method, error) {
	if !isAbs(s.Exe) {
		return MethodNone, errors.New("autostart: the executable path must be absolute")
	}
	switch i.platform() {
	case "windows":
		return i.installWindows(ctx, s)
	case "darwin":
		return i.installDarwin(ctx, s)
	case "linux":
		return i.installLinux(ctx, s)
	}
	return MethodNone, ErrUnsupported
}

// Uninstall removes whatever Install made. Removing what is not there is not
// an error.
func (i Installer) Uninstall(ctx context.Context) error {
	switch i.platform() {
	case "windows":
		return i.uninstallWindows(ctx)
	case "darwin":
		return i.uninstallDarwin(ctx)
	case "linux":
		return i.uninstallLinux(ctx)
	}
	return ErrUnsupported
}

// Status says whether an entry exists and how it was made.
func (i Installer) Status(ctx context.Context) (Method, error) {
	switch i.platform() {
	case "windows":
		return i.statusWindows(ctx)
	case "darwin":
		return i.statusDarwin()
	case "linux":
		return i.statusLinux()
	}
	return MethodNone, ErrUnsupported
}

// --- Windows --------------------------------------------------------------

// TaskXML is the Scheduled Task definition: at logon of this user, run only
// while logged on, restart on failure three times a minute apart, no time
// limit, and never ask for elevation.
func TaskXML(s Spec, user string) string {
	return `<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo>
    <Description>Keeps this computer reachable from the Deck phone app.</Description>
  </RegistrationInfo>
  <Triggers>
    <LogonTrigger>
      <Enabled>true</Enabled>
      <UserId>` + xmlEscape(user) + `</UserId>
    </LogonTrigger>
  </Triggers>
  <Principals>
    <Principal id="Author">
      <UserId>` + xmlEscape(user) + `</UserId>
      <LogonType>InteractiveToken</LogonType>
      <RunLevel>LeastPrivilege</RunLevel>
    </Principal>
  </Principals>
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <AllowHardTerminate>true</AllowHardTerminate>
    <StartWhenAvailable>true</StartWhenAvailable>
    <AllowStartOnDemand>true</AllowStartOnDemand>
    <Enabled>true</Enabled>
    <Hidden>false</Hidden>
    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>
    <RestartOnFailure>
      <Interval>PT1M</Interval>
      <Count>3</Count>
    </RestartOnFailure>
  </Settings>
  <Actions Context="Author">
    <Exec>
      <Command>` + xmlEscape(s.Exe) + `</Command>
      <Arguments>` + xmlEscape(windowsArgs(s.Args)) + `</Arguments>
    </Exec>
  </Actions>
</Task>
`
}

func xmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")
	return r.Replace(s)
}

// windowsArgs joins arguments the way CommandLineToArgvW will split them.
func windowsArgs(args []string) string {
	parts := make([]string, len(args))
	for i, a := range args {
		if a != "" && !strings.ContainsAny(a, " \t\"") {
			parts[i] = a
			continue
		}
		parts[i] = `"` + strings.ReplaceAll(a, `"`, `\"`) + `"`
	}
	return strings.Join(parts, " ")
}

// utf16LE encodes s as UTF-16 little endian with a byte order mark, the form
// schtasks insists on for /XML.
func utf16LE(s string) []byte {
	units := utf16.Encode([]rune(s))
	out := make([]byte, 0, 2+2*len(units))
	out = append(out, 0xff, 0xfe)
	for _, u := range units {
		out = append(out, byte(u), byte(u>>8))
	}
	return out
}

// RunKeyValue is the command line stored in the Run key.
func RunKeyValue(s Spec) string {
	return `"` + s.Exe + `" ` + windowsArgs(s.Args)
}

func (i Installer) installWindows(ctx context.Context, s Spec) (Method, error) {
	user := os.Getenv("USERDOMAIN") + `\` + os.Getenv("USERNAME")
	if os.Getenv("USERNAME") == "" {
		user = ""
	}
	f, err := os.CreateTemp("", "deck-task-*.xml")
	if err != nil {
		return MethodNone, err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(utf16LE(TaskXML(s, user))); err != nil {
		f.Close()
		return MethodNone, err
	}
	if err := f.Close(); err != nil {
		return MethodNone, err
	}
	r := i.runner()
	if _, err := r.Run(ctx, "schtasks", "/Create", "/TN", taskName, "/XML", f.Name(), "/F"); err == nil {
		// A leftover Run key from an earlier fallback would start a second copy.
		_, _ = r.Run(ctx, "reg", "delete", runKeyPath, "/v", runKeyName, "/f")
		return MethodTask, nil
	}
	// Group Policy can block Scheduled Tasks; a Run key needs no rights.
	if out, err := r.Run(ctx, "reg", "add", runKeyPath, "/v", runKeyName, "/t", "REG_SZ", "/d", RunKeyValue(s), "/f"); err != nil {
		return MethodNone, fmt.Errorf("could not register autostart: %w: %s", err, out)
	}
	return MethodRunKey, nil
}

func (i Installer) uninstallWindows(ctx context.Context) error {
	r := i.runner()
	var errs []error
	if out, err := r.Run(ctx, "schtasks", "/Delete", "/TN", taskName, "/F"); err != nil && !notFound(out) {
		errs = append(errs, fmt.Errorf("remove scheduled task: %w: %s", err, out))
	}
	if out, err := r.Run(ctx, "reg", "delete", runKeyPath, "/v", runKeyName, "/f"); err != nil && !notFound(out) {
		errs = append(errs, fmt.Errorf("remove run key: %w: %s", err, out))
	}
	return errors.Join(errs...)
}

func notFound(out string) bool {
	o := strings.ToLower(out)
	return strings.Contains(o, "does not exist") || strings.Contains(o, "cannot find") ||
		strings.Contains(o, "unable to find") || strings.Contains(o, "not find")
}

func (i Installer) statusWindows(ctx context.Context) (Method, error) {
	r := i.runner()
	if _, err := r.Run(ctx, "schtasks", "/Query", "/TN", taskName); err == nil {
		return MethodTask, nil
	}
	if _, err := r.Run(ctx, "reg", "query", runKeyPath, "/v", runKeyName); err == nil {
		return MethodRunKey, nil
	}
	return MethodNone, nil
}

// --- macOS ----------------------------------------------------------------

// Plist is the LaunchAgent: start at login and keep it running.
func Plist(s Spec) string {
	var args strings.Builder
	for _, a := range append([]string{s.Exe}, s.Args...) {
		args.WriteString("    <string>" + xmlEscape(a) + "</string>\n")
	}
	log := ""
	if s.LogPath != "" {
		log = "  <key>StandardOutPath</key>\n  <string>" + xmlEscape(s.LogPath) + "</string>\n" +
			"  <key>StandardErrorPath</key>\n  <string>" + xmlEscape(s.LogPath) + "</string>\n"
	}
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>` + launchdID + `</string>
  <key>ProgramArguments</key>
  <array>
` + args.String() + `  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>ProcessType</key>
  <string>Background</string>
` + log + `</dict>
</plist>
`
}

func (i Installer) plistPath() (string, error) {
	h, err := i.home()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, "Library", "LaunchAgents", launchdID+".plist"), nil
}

func (i Installer) installDarwin(ctx context.Context, s Spec) (Method, error) {
	path, err := i.plistPath()
	if err != nil {
		return MethodNone, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return MethodNone, err
	}
	if err := os.WriteFile(path, []byte(Plist(s)), 0o644); err != nil {
		return MethodNone, err
	}
	domain := fmt.Sprintf("gui/%d", i.uid())
	r := i.runner()
	_, _ = r.Run(ctx, "launchctl", "bootout", domain+"/"+launchdID) // replace a running copy
	if out, err := r.Run(ctx, "launchctl", "bootstrap", domain, path); err != nil {
		return MethodNone, fmt.Errorf("launchctl bootstrap: %w: %s", err, out)
	}
	return MethodLaunchd, nil
}

func (i Installer) uninstallDarwin(ctx context.Context) error {
	path, err := i.plistPath()
	if err != nil {
		return err
	}
	_, _ = i.runner().Run(ctx, "launchctl", "bootout", fmt.Sprintf("gui/%d/%s", i.uid(), launchdID))
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (i Installer) statusDarwin() (Method, error) {
	path, err := i.plistPath()
	if err != nil {
		return MethodNone, err
	}
	if _, err := os.Stat(path); err == nil {
		return MethodLaunchd, nil
	}
	return MethodNone, nil
}

// --- Linux ----------------------------------------------------------------

// Unit is the systemd user service.
func Unit(s Spec) string {
	cmd := make([]string, 0, len(s.Args)+1)
	for _, a := range append([]string{s.Exe}, s.Args...) {
		cmd = append(cmd, systemdQuote(a))
	}
	out := "[Unit]\nDescription=Deck Mobile: keeps this computer reachable from your phone\n\n[Service]\nExecStart=" +
		strings.Join(cmd, " ") + "\nRestart=on-failure\nRestartSec=60\n"
	if s.LogPath != "" {
		out += "StandardOutput=append:" + s.LogPath + "\nStandardError=append:" + s.LogPath + "\n"
	}
	return out + "\n[Install]\nWantedBy=default.target\n"
}

func systemdQuote(a string) string {
	if a != "" && !strings.ContainsAny(a, " \t\"'\\$%") {
		return a
	}
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "$", "$$", "%", "%%")
	return `"` + r.Replace(a) + `"`
}

func (i Installer) unitPath() (string, error) {
	h, err := i.home()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, ".config", "systemd", "user", unitName), nil
}

func (i Installer) installLinux(ctx context.Context, s Spec) (Method, error) {
	path, err := i.unitPath()
	if err != nil {
		return MethodNone, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return MethodNone, err
	}
	if err := os.WriteFile(path, []byte(Unit(s)), 0o644); err != nil {
		return MethodNone, err
	}
	r := i.runner()
	if out, err := r.Run(ctx, "systemctl", "--user", "daemon-reload"); err != nil {
		return MethodNone, fmt.Errorf("systemctl daemon-reload: %w: %s", err, out)
	}
	if out, err := r.Run(ctx, "systemctl", "--user", "enable", "--now", unitName); err != nil {
		return MethodNone, fmt.Errorf("systemctl enable: %w: %s", err, out)
	}
	return MethodSystemd, nil
}

func (i Installer) uninstallLinux(ctx context.Context) error {
	path, err := i.unitPath()
	if err != nil {
		return err
	}
	_, _ = i.runner().Run(ctx, "systemctl", "--user", "disable", "--now", unitName)
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	_, _ = i.runner().Run(ctx, "systemctl", "--user", "daemon-reload")
	return nil
}

func (i Installer) statusLinux() (Method, error) {
	path, err := i.unitPath()
	if err != nil {
		return MethodNone, err
	}
	if _, err := os.Stat(path); err == nil {
		return MethodSystemd, nil
	}
	return MethodNone, nil
}
