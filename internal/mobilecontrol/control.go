// Package mobilecontrol implements the one-step CLI/TUI mobile setup workflow.
package mobilecontrol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/dev-dami/deck/internal/lock"
	"github.com/dev-dami/deck/pkg/remote/devstore"
	"github.com/dev-dami/deck/pkg/remote/ipc"
	"github.com/dev-dami/deck/pkg/remote/mobile"
	"github.com/dev-dami/deck/pkg/remote/projects"
)

type Options struct {
	Dir, Executable, Action, Project, RelayDir, LANIP, DeviceID string
	LAN                                                         bool
	Port                                                        int
}
type Result struct {
	Name       string      `json:"name"`
	RelayURL   string      `json:"relay_url"`
	Online     bool        `json:"online"`
	Devices    int         `json:"devices"`
	DeviceList []DeviceRef `json:"device_list"`
	Projects   int         `json:"projects"`
	PairURL    string      `json:"pair_url,omitempty"`
	ExpiresAt  *time.Time  `json:"expires_at,omitempty"`
	Message    string      `json:"message,omitempty"`
}

// DeviceRef is the public paired-phone summary. Keys and relay tokens never
// cross this CLI/UI boundary.
type DeviceRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func withDevices(dir string, r Result) (Result, error) {
	store, err := devstore.Open(dir)
	if err != nil {
		return Result{}, err
	}
	r.DeviceList = make([]DeviceRef, 0)
	for _, d := range store.List() {
		r.DeviceList = append(r.DeviceList, DeviceRef{ID: d.ID, Name: d.Name})
	}
	r.Devices = len(r.DeviceList)
	return r, nil
}

func control(ctx context.Context, dir string, method string, out any) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	ep, err := ipc.ReadEndpoint(dir)
	if err != nil {
		return err
	}
	cc, err := ipc.DialControl(ctx, ep)
	if err != nil {
		return err
	}
	defer cc.Close()
	return cc.Call(ctx, method, nil, out)
}
func status(ctx context.Context, dir string) (Result, error) {
	var st mobile.StatusResult
	if err := control(ctx, dir, "status", &st); err != nil {
		return Result{}, err
	}
	return withDevices(dir, Result{Name: st.Name, RelayURL: st.RelayURL, Online: st.RelayOnline, Projects: len(st.Projects)})
}

func currentStatus(ctx context.Context, dir string) (Result, error) {
	if r, err := status(ctx, dir); err == nil {
		return r, nil
	}
	cfg, err := mobile.LoadConfig(dir)
	if err != nil {
		return Result{}, err
	}
	reg, err := projects.Open(dir)
	if err != nil {
		return Result{}, err
	}
	count := 0
	for _, p := range reg.List() {
		if p.Enabled {
			count++
		}
	}
	name := cfg.Name
	if name == "" {
		name, _ = os.Hostname()
	}
	return withDevices(dir, Result{Name: name, RelayURL: cfg.RelayURL, Projects: count, Message: "Background process is not running."})
}

// A link uses saved settings, including hosted HTTPS, and falls back to LAN
// only on a fresh installation. It does not replace an existing relay choice.
func linkOptions(opt Options, cfg mobile.Config) Options {
	if opt.Action == "link" {
		opt.LAN = cfg.RelayURL == ""
	}
	return opt
}

// Run performs one action. Pairing secrets are returned to the caller only;
// they are never written to process arguments, logs or management state.
func Run(ctx context.Context, opt Options) (Result, error) {
	if opt.Action == "" {
		opt.Action = "start"
	}
	if opt.Port == 0 {
		opt.Port = 8787
	}
	if opt.Port < 1 || opt.Port > 65535 {
		return Result{}, errors.New("relay port must be between 1 and 65535")
	}
	if opt.Dir == "" {
		return Result{}, errors.New("mobile state directory is required")
	}
	if err := os.MkdirAll(opt.Dir, 0700); err != nil {
		return Result{}, err
	}
	release, err := lock.TryFile(filepath.Join(opt.Dir, "connect.lock"))
	if err != nil {
		return Result{}, errors.New("another mobile connect action is running; try again shortly")
	}
	defer release()
	switch opt.Action {
	case "status":
		return currentStatus(ctx, opt.Dir)
	case "stop":
		return stopManaged(ctx, opt.Dir)
	case "pair":
		return pair(ctx, opt.Dir)
	case "revoke":
		if opt.DeviceID == "" {
			return Result{}, errors.New("choose a phone to disconnect with --device ID (see --action status --json)")
		}
		store, err := devstore.Open(opt.Dir)
		if err != nil {
			return Result{}, err
		}
		if err := store.Revoke(opt.DeviceID); err != nil {
			if errors.Is(err, devstore.ErrNotFound) {
				return Result{}, errors.New("that phone is no longer paired; refresh the phone list")
			}
			return Result{}, err
		}
		r, err := currentStatus(ctx, opt.Dir)
		if err != nil {
			return Result{}, err
		}
		r.Message = "Phone disconnected. Its active connection will close within seconds."
		return r, nil
	case "start", "link":
	default:
		return Result{}, errors.New("action must be start, link, status, pair, revoke or stop")
	}
	cfg, err := mobile.LoadConfig(opt.Dir)
	if err != nil {
		return Result{}, err
	}
	previous := cfg
	opt = linkOptions(opt, cfg)
	local := opt.LAN
	if !local && cfg.AllowLANHTTP {
		u, _ := url.Parse(cfg.RelayURL)
		local = u != nil && u.Scheme == "http" && net.ParseIP(u.Hostname()) != nil && net.ParseIP(u.Hostname()).IsPrivate()
	}
	if local {
		ip := opt.LANIP
		if ip == "" && previous.AllowLANHTTP {
			u, _ := url.Parse(cfg.RelayURL)
			if candidate, e := lanIP(u.Hostname()); e == nil {
				ip = candidate
			}
			if p, e := strconv.Atoi(u.Port()); e == nil && !opt.LAN {
				opt.Port = p
			}
		}
		ip, err = lanIP(ip)
		if err != nil {
			return Result{}, err
		}
		cfg.RelayURL = "http://" + net.JoinHostPort(ip, strconv.Itoa(opt.Port))
		cfg.AllowLANHTTP = true
		opt.LANIP = ip
	}
	if cfg.RelayURL == "" {
		return Result{}, errors.New("no relay configured; use /mobile lan or deck mobile connect --lan, or configure an HTTPS relay with deck mobile setup")
	}
	if opt.Project != "" {
		if st, e := os.Stat(opt.Project); e != nil || !st.IsDir() {
			return Result{}, errors.New("project must name an existing directory")
		}
	}
	current, runningErr := status(ctx, opt.Dir)
	if runningErr == nil && (current.RelayURL != cfg.RelayURL || previous.AllowLANHTTP != cfg.AllowLANHTTP) {
		if !managedAlive(ctx, opt.Dir, "daemon") {
			return Result{}, errors.New("a separately started mobile daemon uses different settings; stop it with Ctrl+C in its terminal, then retry")
		}
		if err := stopRole(ctx, opt.Dir, "daemon"); err != nil {
			return Result{}, err
		}
		runningErr = errors.New("restarting")
	}
	if local {
		if err := ensureRelay(ctx, opt); err != nil {
			return Result{}, err
		}
	}
	if !sameConfig(cfg, previous) {
		if err := mobile.SaveConfig(opt.Dir, cfg); err != nil {
			return Result{}, err
		}
	}
	if opt.Project != "" {
		reg, err := projects.Open(opt.Dir)
		if err != nil {
			return Result{}, err
		}
		abs, err := filepath.Abs(opt.Project)
		if err != nil {
			return Result{}, err
		}
		// Preserve names chosen earlier when re-enabling a shared project.
		name := ""
		for _, p := range reg.List() {
			if p.Path == abs {
				name = p.Name
			}
		}
		if _, err := reg.Add(abs, name); err != nil {
			return Result{}, err
		}
	}
	if runningErr != nil {
		if err := startManaged(ctx, opt, "daemon", opt.Executable, []string{"mobile", "daemon"}, ""); err != nil {
			return Result{}, err
		}
	}
	deadline := time.Now().Add(45 * time.Second)
	for {
		r, err := status(ctx, opt.Dir)
		if err == nil && r.Online {
			return pair(ctx, opt.Dir)
		}
		if time.Now().After(deadline) {
			return Result{}, fmt.Errorf("mobile relay did not connect; check %s and Windows Private-network firewall access", filepath.Join(opt.Dir, "daemon-managed.log"))
		}
		if err := pause(ctx); err != nil {
			return Result{}, err
		}
	}
}
func sameConfig(a, b mobile.Config) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}
func pause(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(200 * time.Millisecond):
		return nil
	}
}
func pair(ctx context.Context, dir string) (Result, error) {
	r, err := status(ctx, dir)
	if err != nil {
		return Result{}, errors.New("mobile is not running; use deck mobile connect first")
	}
	if !r.Online {
		return Result{}, errors.New("relay is offline; start the relay or use deck mobile connect first")
	}
	var p mobile.PairResult
	// Explicitly requesting another QR rotates the one-use pairing secret. The
	// previous code stops working immediately, including when its slot is busy.
	if err := control(ctx, dir, "unpair", nil); err != nil {
		return Result{}, fmt.Errorf("could not replace the previous pairing code: %w", err)
	}
	if err := control(ctx, dir, "pair", &p); err != nil {
		return Result{}, err
	}
	r.PairURL = p.URL
	r.ExpiresAt = &p.ExpiresAt
	return r, nil
}

func lanIP(requested string) (string, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return "", err
	}
	found := map[string]bool{}
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, a := range addrs {
			ip, _, _ := net.ParseCIDR(a.String())
			if ip != nil && ip.To4() != nil && ip.IsPrivate() {
				found[ip.String()] = true
			}
		}
	}
	if requested != "" {
		if !found[requested] {
			return "", errors.New("--lan-ip must be a private IPv4 address assigned to this PC")
		}
		return requested, nil
	}
	// A UDP connect selects the default-route interface without sending packets.
	if c, e := net.Dial("udp4", "8.8.8.8:80"); e == nil {
		ip := c.LocalAddr().(*net.UDPAddr).IP.String()
		c.Close()
		if found[ip] {
			return ip, nil
		}
	}
	if len(found) == 1 {
		for ip := range found {
			return ip, nil
		}
	}
	return "", errors.New("could not select a Wi-Fi IPv4 address; use --lan-ip with the address shown by ipconfig")
}

func relaySource(opt Options) (string, error) {
	if opt.RelayDir != "" {
		abs, err := filepath.Abs(opt.RelayDir)
		if err != nil {
			return "", err
		}
		for _, name := range []string{"wrangler.jsonc", filepath.Join("node_modules", "wrangler", "bin", "wrangler.js")} {
			if _, e := os.Stat(filepath.Join(abs, name)); e != nil {
				return "", errors.New("--relay-dir must contain wrangler.jsonc and node_modules/wrangler; install relay dependencies with bun install")
			}
		}
		return abs, nil
	}
	candidates := []string{opt.RelayDir}
	var saved struct {
		RelayDir string `json:"relay_dir"`
	}
	if b, e := os.ReadFile(filepath.Join(opt.Dir, "connect-runtime.json")); e == nil {
		_ = json.Unmarshal(b, &saved)
		candidates = append(candidates, saved.RelayDir)
	}
	cwd, _ := os.Getwd()
	for _, base := range []string{cwd, filepath.Dir(opt.Executable)} {
		for p := base; p != ""; p = filepath.Dir(p) {
			candidates = append(candidates, filepath.Join(p, "pkg", "remote", "relay"), filepath.Join(p, "relay"))
			if filepath.Dir(p) == p {
				break
			}
		}
	}
	for _, p := range candidates {
		if p == "" {
			continue
		}
		if _, e := os.Stat(filepath.Join(p, "node_modules", "wrangler", "bin", "wrangler.js")); e == nil {
			if _, e := os.Stat(filepath.Join(p, "wrangler.jsonc")); e == nil {
				abs, _ := filepath.Abs(p)
				return abs, nil
			}
		}
	}
	return "", errors.New("local relay runtime not found; use --relay-dir pointing to pkg/remote/relay after installing its dependencies (bun install)")
}
func relayProbe(ctx context.Context, origin string) bool {
	client := http.Client{Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, _ := http.NewRequestWithContext(ctx, "GET", origin+"/r/v1/vapid", nil)
	res, err := client.Do(req)
	if err != nil {
		return false
	}
	defer res.Body.Close()
	var body struct {
		Error     string `json:"error"`
		PublicKey string `json:"public_key"`
	}
	if json.NewDecoder(io.LimitReader(res.Body, 4096)).Decode(&body) != nil {
		return false
	}
	if !((res.StatusCode == 404 && body.Error == "push_unavailable") || (res.StatusCode == 200 && body.PublicKey != "")) {
		return false
	}
	req, _ = http.NewRequestWithContext(ctx, "GET", origin+"/r/v1/c/AAAAAAAAAAAAAAAAAAAAAA/host", nil)
	res2, err := client.Do(req)
	if err != nil {
		return false
	}
	defer res2.Body.Close()
	return res2.StatusCode == 426
}
func ensureRelay(ctx context.Context, opt Options) error {
	origin := "http://" + net.JoinHostPort(opt.LANIP, strconv.Itoa(opt.Port))
	if opt.LANIP == "" {
		origin = "http://127.0.0.1:" + strconv.Itoa(opt.Port)
	}
	if relayProbe(ctx, origin) {
		return nil
	}
	for _, host := range []string{"127.0.0.1", opt.LANIP} {
		if host == "" {
			continue
		}
		conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(opt.Port)), time.Second)
		if err == nil {
			conn.Close()
			return fmt.Errorf("port %d is already used by something other than the Deck relay; stop that service or choose --port", opt.Port)
		}
	}
	if managedAlive(ctx, opt.Dir, "relay") {
		return errors.New("a managed relay is running but cannot serve this address/port; use deck mobile connect --action stop before changing LAN settings")
	}
	source, err := relaySource(opt)
	if err != nil {
		return err
	}
	node, err := exec.LookPath("node")
	if err != nil {
		return errors.New("Node.js is required to start the local relay; install Node.js and restart Deck")
	}
	if err := startManaged(ctx, opt, "relay", node, []string{filepath.Join(source, "node_modules", "wrangler", "bin", "wrangler.js"), "dev", "--local", "--ip", "0.0.0.0", "--port", strconv.Itoa(opt.Port), "--inspector-port", "0"}, source); err != nil {
		return err
	}
	raw, _ := json.Marshal(map[string]string{"relay_dir": source})
	if err := os.WriteFile(filepath.Join(opt.Dir, "connect-runtime.json"), raw, 0600); err != nil {
		return err
	}
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		if relayProbe(ctx, origin) {
			return nil
		}
		if !managedAlive(ctx, opt.Dir, "relay") {
			return fmt.Errorf("local relay exited; inspect %s", filepath.Join(opt.Dir, "relay-managed.log"))
		}
		if err := pause(ctx); err != nil {
			return err
		}
	}
	return fmt.Errorf("local relay did not become ready; inspect %s", filepath.Join(opt.Dir, "relay-managed.log"))
}
