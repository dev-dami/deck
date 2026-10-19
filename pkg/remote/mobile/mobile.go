// Package mobile runs Deck Mobile's always-on side: it assembles the daemon
// from its parts (identity, device store, project registry, queue, relay
// client, project providers over local IPC) and serves the CLI's control
// channel. `deck mobile daemon` is a thin wrapper around Run.
package mobile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/dev-dami/deck/internal/lock"
	"github.com/dev-dami/deck/pkg/remote/daemon"
	"github.com/dev-dami/deck/pkg/remote/devstore"
	"github.com/dev-dami/deck/pkg/remote/identity"
	"github.com/dev-dami/deck/pkg/remote/ipc"
	"github.com/dev-dami/deck/pkg/remote/passkey"
	"github.com/dev-dami/deck/pkg/remote/projects"
	"github.com/dev-dami/deck/pkg/remote/queue"
	"github.com/dev-dami/deck/pkg/remote/relayclient"
)

// ErrAlreadyRunning is returned by Run when another daemon holds the lock.
var ErrAlreadyRunning = errors.New("deck mobile is already running for this user")

const (
	lockFile   = "daemon.lock"
	configFile = "mobile.json"
)

// Variables so tests can shorten them.
var (
	reloadEvery   = 3 * time.Second
	providerRetry = 5 * time.Second
)

// Config is the user's settings, kept in dir/mobile.json.
type Config struct {
	// RelayURL is the hosted app and relay, https://app.example.dev. Empty
	// means no relay: the daemon still serves local providers and the CLI, but
	// no phone can reach it.
	RelayURL string `json:"relay_url,omitempty"`
	// AllowLANHTTP permits private IPv4 relay addresses for explicit LAN testing.
	AllowLANHTTP bool `json:"allow_lan_http,omitempty"`
	// Name is how this computer appears on the phone.
	Name string `json:"name,omitempty"`
	// AndroidOrigins allows only the explicitly pinned signing certificates of
	// native Deck apps. Browser origin and RP ID still come from RelayURL.
	AndroidOrigins []string `json:"android_origins,omitempty"`
}

// LoadConfig reads dir/mobile.json; a missing file is the zero Config.
func LoadConfig(dir string) (Config, error) {
	raw, err := os.ReadFile(filepath.Join(dir, configFile))
	if errors.Is(err, os.ErrNotExist) {
		return Config{}, nil
	}
	if err != nil {
		return Config{}, err
	}
	var c Config
	if err := json.Unmarshal(raw, &c); err != nil {
		return Config{}, fmt.Errorf("%s is not valid: %w", configFile, err)
	}
	if err := validateAndroidOrigins(c.AndroidOrigins); err != nil {
		return Config{}, err
	}
	return c, nil
}

func validateAndroidOrigins(origins []string) error {
	for _, origin := range origins {
		if !passkey.ValidAndroidOrigin(origin) {
			return errors.New("android origin must be android:apk-key-hash: followed by an unpadded base64url SHA-256 certificate hash")
		}
	}
	return nil
}

// SaveConfig writes dir/mobile.json after validating it.
func SaveConfig(dir string, c Config) error {
	if err := validateAndroidOrigins(c.AndroidOrigins); err != nil {
		return err
	}
	if c.RelayURL != "" {
		u, err := url.Parse(c.RelayURL)
		if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || (u.Scheme != "https" && !(u.Scheme == "http" && (isLoopback(u.Hostname()) || (c.AllowLANHTTP && isPrivateIPv4(u.Hostname()))))) {
			return errors.New("the relay URL must be an https origin (private IPv4 http requires --allow-lan-http)")
		}
		c.RelayURL = strings.TrimRight(c.RelayURL, "/")
	}
	raw, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, configFile), append(raw, '\n'), 0o600)
}

func isPrivateIPv4(host string) bool {
	ip := net.ParseIP(host)
	return ip != nil && ip.To4() != nil && ip.IsPrivate()
}

func isLoopback(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

// Options configures Run.
type Options struct {
	// Dir is the computer-wide state directory (identity.DefaultDir()).
	Dir     string
	Config  Config
	Version string
	// QueueSettle overrides how long the mailbox must be quiet before queued
	// actions run (default 2 s).
	QueueSettle time.Duration
	// Spawn starts a headless Deck for the project at path. Nil disables
	// starting projects on demand.
	Spawn func(path string) error
}

// StatusResult is the control channel's answer to "status".
type StatusResult struct {
	CID         string        `json:"cid"`
	Name        string        `json:"name"`
	RelayURL    string        `json:"relay_url"`
	RelayOnline bool          `json:"relay_online"`
	Devices     int           `json:"devices"`
	Projects    []ProjectInfo `json:"projects"`
}

// ProjectInfo is one project in StatusResult.
type ProjectInfo struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Path   string `json:"path"`
	Online bool   `json:"online"`
}

// QueueItem is one waiting action in the control channel's "queue.list".
type QueueItem struct {
	QID       string    `json:"qid"`
	Device    string    `json:"device"`
	Project   string    `json:"project"`
	Kind      string    `json:"kind"`
	Text      string    `json:"text,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// QueueCancelArgs selects what "queue.cancel" cancels.
type QueueCancelArgs struct {
	QID string `json:"qid,omitempty"`
	All bool   `json:"all,omitempty"`
}

// PairResult is the control channel's answer to "pair".
type PairResult struct {
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Run assembles and runs the daemon until ctx ends.
func Run(ctx context.Context, opt Options) error {
	if err := validateAndroidOrigins(opt.Config.AndroidOrigins); err != nil {
		return err
	}
	if err := os.MkdirAll(opt.Dir, 0o700); err != nil {
		return err
	}
	release, err := lock.TryFile(filepath.Join(opt.Dir, lockFile))
	if err != nil {
		if errors.Is(err, lock.ErrContended) {
			return ErrAlreadyRunning
		}
		return err
	}
	defer release()

	ident, err := identity.LoadOrCreate(opt.Dir)
	if err != nil {
		return err
	}
	devs, err := devstore.Open(opt.Dir)
	if err != nil {
		return err
	}
	reg, err := projects.Open(opt.Dir)
	if err != nil {
		return err
	}
	q, err := queue.Open(opt.Dir)
	if err != nil {
		return err
	}
	defer q.Close()

	name := opt.Config.Name
	if name == "" {
		name, _ = os.Hostname()
	}
	cfg := daemon.Config{
		Identity: ident, Devices: devs, Projects: reg, Name: name, Version: opt.Version, Queue: q,
		QueueSettle: opt.QueueSettle,
	}
	if u, err := url.Parse(opt.Config.RelayURL); err == nil && u.Host != "" {
		cfg.Gate = daemon.NewGate(daemon.GateConfig{
			CID: ident.CID(), Devices: devs, Projects: reg,
			Verifier: passkey.Verifier{RPID: u.Hostname(), Origin: strings.TrimRight(opt.Config.RelayURL, "/"), AndroidOrigins: slices.Clone(opt.Config.AndroidOrigins)},
		})
	}
	if opt.Spawn != nil {
		cfg.Spawn = func(pj string) error {
			p, err := reg.Get(pj)
			if err != nil {
				return err
			}
			return opt.Spawn(p.Path)
		}
	}
	d := daemon.New(cfg)

	var rc *relayclient.Client
	if opt.Config.RelayURL != "" {
		rc = relayclient.New(relayclient.Config{
			Identity: ident,
			URL:      opt.Config.RelayURL,
			Hashes:   devs.TokenHashes,
			Handlers: relayclient.Handlers{
				Lane: func(ctx context.Context, l *relayclient.Lane, pair bool) {
					var err error
					if pair {
						err = d.ServePair(ctx, l)
					} else {
						err = d.Serve(ctx, l)
					}
					if err != nil && ctx.Err() == nil {
						slog.Debug("remote: phone connection ended", "pair", pair, "error", err)
					}
				},
				Mail: d.HandleMail,
			},
			OnState: func(online bool) {
				if online {
					d.Refresh()
				}
			},
		})
		d.SetRelay(rc, rc)
	}

	ln, err := ipc.Listen()
	if err != nil {
		return err
	}
	defer ln.Close()
	secret, err := ipc.NewSecret()
	if err != nil {
		return err
	}
	if err := ipc.WriteEndpoint(opt.Dir, ipc.Endpoint{Addr: ln.Addr().String(), Secret: secret}); err != nil {
		return err
	}
	defer os.Remove(filepath.Join(opt.Dir, "ipc.json"))

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go d.RunQueue(ctx)
	go d.RunSnapshots(ctx)
	if rc != nil {
		go rc.Run(ctx)
	}
	go watch(ctx, devs, reg, rc, q)

	control := func(ctx context.Context, cmd string, args json.RawMessage) (any, error) {
		switch cmd {
		case "pair":
			if opt.Config.RelayURL == "" {
				return nil, errors.New("no relay is set up; run 'deck mobile setup --relay https://...' first")
			}
			inv, err := d.ArmPair(opt.Config.RelayURL, nil, "")
			if err != nil {
				return nil, err
			}
			return PairResult{URL: inv.URL, ExpiresAt: inv.ExpiresAt}, nil
		case "queue.list":
			pending, err := d.PendingActions()
			if err != nil {
				return nil, err
			}
			items := []QueueItem{}
			for _, a := range pending {
				it := QueueItem{QID: a.QID, Device: a.DeviceID, Kind: a.Kind, CreatedAt: a.CreatedAt, ExpiresAt: a.ExpiresAt}
				if dev, err := devs.Get(a.DeviceID); err == nil {
					it.Device = dev.Name
				}
				if p, err := reg.Get(a.PJ); err == nil {
					it.Project = p.Name
				}
				var pl struct {
					Text string `json:"text"`
				}
				if json.Unmarshal(a.Payload, &pl) == nil {
					it.Text = pl.Text
				}
				items = append(items, it)
			}
			return items, nil
		case "queue.cancel":
			var ca QueueCancelArgs
			if err := json.Unmarshal(args, &ca); err != nil || (ca.QID == "" && !ca.All) {
				return nil, errors.New("say which action to cancel, or --all")
			}
			ids := []string{ca.QID}
			if ca.All {
				pending, err := d.PendingActions()
				if err != nil {
					return nil, err
				}
				ids = ids[:0]
				for _, a := range pending {
					ids = append(ids, a.QID)
				}
			}
			n := 0
			for _, id := range ids {
				ok, err := d.CancelAction(id)
				if err != nil {
					return nil, err
				}
				if ok {
					n++
				}
			}
			if n == 0 && !ca.All {
				return nil, errors.New("no waiting action with that id")
			}
			return n, nil
		case "unpair":
			d.DisarmPair()
			return struct{}{}, nil
		case "status":
			st := StatusResult{CID: ident.CID(), Name: name, RelayURL: opt.Config.RelayURL, Devices: len(devs.List())}
			if rc != nil {
				st.RelayOnline = rc.Online()
			}
			for _, p := range reg.List() {
				st.Projects = append(st.Projects, ProjectInfo{ID: p.ID, Name: p.Name, Path: p.Path, Online: d.Online(p.ID)})
			}
			return st, nil
		}
		return nil, fmt.Errorf("unknown command %q", cmd)
	}

	accepted := make(chan struct{})
	go func() {
		defer close(accepted)
		for {
			role, pj, conn, err := ipc.AcceptAny(ln, secret)
			if err != nil {
				if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
					return
				}
				if errors.Is(err, ipc.ErrBadHello) {
					continue
				}
				slog.Warn("remote: ipc accept", "error", err)
				return
			}
			switch role {
			case ipc.RoleControl:
				go func() {
					defer conn.Close()
					_ = ipc.ServeControl(ctx, conn, control)
				}()
			default:
				go attach(ctx, d, reg, pj, conn)
			}
		}
	}()

	<-ctx.Done()
	ln.Close()
	<-accepted
	return nil
}

// attach makes the process on conn the provider of pj for as long as it lives.
func attach(ctx context.Context, d *daemon.Daemon, reg *projects.Registry, pj string, conn interface {
	Read([]byte) (int, error)
	Write([]byte) (int, error)
	Close() error
}) {
	defer conn.Close()
	if _, err := reg.Get(pj); err != nil {
		slog.Warn("remote: a Deck offered a project that is not shared with the phone", "project", pj)
		return
	}
	proxy, err := ipc.NewProxy(conn)
	if err != nil {
		slog.Warn("remote: provider did not introduce itself", "project", pj, "error", err)
		return
	}
	d.Attach(pj, proxy)
	select {
	case <-proxy.Done():
	case <-ctx.Done():
	}
	d.Detach(pj, proxy)
}

// watch keeps the daemon in step with files the CLI edits: a revoked phone is
// dropped from the relay and forgotten by the queue, a changed project list is
// picked up.
func watch(ctx context.Context, devs *devstore.Store, reg *projects.Registry, rc *relayclient.Client, q *queue.Store) {
	known := ids(devs)
	t := time.NewTicker(reloadEvery)
	defer t.Stop()
	prune := time.NewTicker(time.Hour)
	defer prune.Stop()
	for {
		select {
		case <-t.C:
			if _, err := reg.Reload(); err != nil {
				slog.Warn("remote: projects file", "error", err)
			}
			changed, err := devs.Reload()
			if err != nil {
				slog.Warn("remote: devices file", "error", err)
				continue
			}
			if !changed {
				continue
			}
			now := ids(devs)
			for _, id := range known {
				if !slices.Contains(now, id) {
					if err := q.ForgetDevice(id); err != nil {
						slog.Warn("remote: queue not cleared for revoked phone", "error", err)
					}
				}
			}
			known = now
			if rc != nil {
				_ = rc.SendDevices()
			}
		case <-prune.C:
			if _, err := devs.Prune(); err != nil {
				slog.Warn("remote: device prune", "error", err)
			}
		case <-ctx.Done():
			return
		}
	}
}

func ids(devs *devstore.Store) []string {
	var out []string
	for _, d := range devs.List() {
		out = append(out, d.ID)
	}
	return out
}
