package remote

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/dev-dami/deck/internal/home"
	"github.com/dev-dami/deck/internal/remote/auth"
	"github.com/dev-dami/deck/internal/remote/push"
	"github.com/dev-dami/deck/internal/remote/tunnel"
)

// DefaultPort is where `deck serve` listens unless told otherwise.
const DefaultPort = 7878

// RunOptions say how to expose the server.
type RunOptions struct {
	Port int // 0 picks a free port
	// LAN binds every interface so a phone on the same Wi-Fi can connect.
	// Without it only this machine can: use a tunnel to reach it from anywhere.
	LAN       bool
	Tunnel    string // "" | tunnel.Cloudflare | tunnel.Tailscale
	AllowYolo bool
	Headless  bool
	Version   string
	// Dir holds devices.json; empty means the per-machine default.
	Dir string
}

// DefaultDir is where paired devices are kept, shared by every project's server.
func DefaultDir() string { return filepath.Join(home.Config(), "deck", "remote") }

// Running is a started server with its addresses.
type Running struct {
	Server    *Server
	Store     *auth.Store
	LocalURL  string
	LANURLs   []string
	PublicURL string // the tunnel's address, when there is one
	Tunnel    string

	httpSrv *http.Server
	tun     tunnel.Tunnel
	cancel  context.CancelFunc
	done    chan struct{}
}

// lanAddrs lists this machine's private IPv4 addresses.
func lanAddrs() []string {
	ifs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	var out []string
	for _, a := range ifs {
		ipn, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		ip := ipn.IP.To4()
		if ip == nil || ip.IsLoopback() || !ip.IsPrivate() {
			continue
		}
		out = append(out, ip.String())
	}
	sort.Strings(out)
	return out
}

// Start listens, starts the tunnel if asked, and serves be until Stop.
func Start(ctx context.Context, be Backend, ro RunOptions) (*Running, error) {
	dir := ro.Dir
	if dir == "" {
		dir = DefaultDir()
	}
	store, err := auth.New(dir)
	if err != nil {
		return nil, err
	}

	bind := "127.0.0.1"
	if ro.LAN {
		bind = "0.0.0.0"
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(bind, strconv.Itoa(ro.Port)))
	if err != nil {
		return nil, fmt.Errorf("listen: %w", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port

	r := &Running{Store: store, Tunnel: ro.Tunnel, done: make(chan struct{})}
	r.LocalURL = "http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	pushSvc, err := push.New(dir, nil)
	if err != nil {
		ln.Close()
		return nil, fmt.Errorf("notifications: %w", err)
	}
	opts := Options{
		Push:      pushSvc,
		AllowYolo: ro.AllowYolo, Headless: ro.Headless, Tunnel: ro.Tunnel, Version: ro.Version,
		TrustProxyIP: ro.Tunnel == tunnel.Cloudflare,
	}
	r.Server = New(be, store, opts)
	if ro.LAN {
		for _, ip := range lanAddrs() {
			r.Server.AddHost(ip)
			r.LANURLs = append(r.LANURLs, "http://"+net.JoinHostPort(ip, strconv.Itoa(port)))
		}
	}

	runCtx, cancel := context.WithCancel(ctx)
	r.cancel = cancel

	if ro.Tunnel != "" {
		tun, err := tunnel.New(ro.Tunnel)
		if err != nil {
			ln.Close()
			cancel()
			return nil, err
		}
		// The tunnel dials 127.0.0.1 whatever the bind address.
		startCtx, startCancel := context.WithTimeout(runCtx, 45*time.Second)
		pub, err := tun.Start(startCtx, net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		startCancel()
		if err != nil {
			ln.Close()
			cancel()
			return nil, fmt.Errorf("%s tunnel: %w", ro.Tunnel, err)
		}
		r.tun, r.PublicURL = tun, pub
		r.Server.AddHost(hostOf(pub))
	}

	r.httpSrv = &http.Server{Handler: r.Server.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		defer close(r.done)
		if err := r.httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			cancel()
		}
	}()
	go r.Server.Run(runCtx)
	return r, nil
}

func hostOf(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	return u.Host
}

// Origin is the address a phone should be sent to: the tunnel if there is one
// (the only one that works off-network and has TLS), else the first LAN
// address, else this machine.
func (r *Running) Origin() string {
	switch {
	case r.PublicURL != "":
		return r.PublicURL
	case len(r.LANURLs) > 0:
		return r.LANURLs[0]
	}
	return r.LocalURL
}

// Pairing mints a one-time link and its QR code.
func (r *Running) Pairing() (link, qr string, err error) {
	link, err = r.Server.PairingURL(r.Origin())
	if err != nil {
		return "", "", err
	}
	qr, err = RenderQR(link)
	return link, qr, err
}

// Stop shuts the server and any tunnel down.
func (r *Running) Stop() {
	r.cancel()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = r.httpSrv.Shutdown(ctx)
	_ = r.httpSrv.Close() // sockets Shutdown left behind: WebSockets are hijacked
	if r.tun != nil {
		_ = r.tun.Close()
	}
	<-r.done
}

// Done is closed once the HTTP server has stopped.
func (r *Running) Done() <-chan struct{} { return r.done }

// active is the server this process is running, for the TUI's remote-control
// dialog; nil when there is none.
var active atomic.Pointer[Running]

// SetActive records r as this process's server (nil clears it).
func SetActive(r *Running) { active.Store(r) }

// Active returns the server started with --remote, or nil.
func Active() *Running { return active.Load() }

// Devices lists the paired phones.
func (r *Running) Devices() ([]auth.Device, error) { return r.Store.List() }

// Revoke unpairs a phone now: its open connection is closed too.
func (r *Running) Revoke(id string) error {
	if err := r.Store.Revoke(id); err != nil {
		return err
	}
	r.Server.kickDevice(id)
	return nil
}
