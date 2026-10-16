package server

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync/atomic"
	"time"

	deckconfig "github.com/dev-dami/deck/internal/deck/config"
	"github.com/dev-dami/deck/internal/deck/peer"
	"github.com/dev-dami/deck/pkg/remote"
	"github.com/dev-dami/deck/pkg/remote/auth"
)

// DefaultPort is where `deck remote` listens unless told otherwise.
const DefaultPort = 7878

// RunOptions say how to expose the server.
type RunOptions struct {
	Port int // 0 picks a free port
	// LAN binds every interface and serves self-signed HTTPS (R9).
	// Without it only this machine can connect, over plain HTTP.
	LAN      bool
	Headless bool
	Version  string
	// DataDir is the project's data dir; the store lives in its remote/.
	DataDir string
	// Paired is told of every new device, for the console.
	Paired func(auth.Device)
}

// Running is a started server with its addresses.
type Running struct {
	Server *Server
	Store  *auth.Store
	// URL is the origin a phone should open; Hosts are every address it
	// is reachable by, best first.
	URL   string
	Hosts []string
	Port  int
	TLS   bool
	// Cert is the LAN certificate; zero without --lan.
	Cert auth.Certificate

	httpSrv *http.Server
	cancel  context.CancelFunc
	done    chan struct{}
	runDone chan struct{}
}

// Start listens and serves be until Stop.
func Start(ctx context.Context, be remote.Backend, ro RunOptions) (*Running, error) {
	store, err := auth.Open(auth.Dir(ro.DataDir))
	if err != nil {
		return nil, err
	}
	bind := "127.0.0.1"
	if ro.LAN {
		bind = ""
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(bind, strconv.Itoa(ro.Port)))
	if err != nil {
		return nil, fmt.Errorf("listen: %w", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port

	r := &Running{Store: store, Port: port, TLS: ro.LAN, done: make(chan struct{}), runDone: make(chan struct{})}
	opts := Options{Version: ro.Version, Headless: ro.Headless, TLS: ro.LAN, Paired: ro.Paired}
	scheme := "http"
	if ro.LAN {
		scheme = "https"
		r.Hosts = peer.InviteHosts("")
		cert, err := store.EnsureCertificate(r.Hosts)
		if err != nil {
			ln.Close()
			return nil, fmt.Errorf("lan certificate: %w", err)
		}
		r.Cert = cert
		opts.Hosts = r.Hosts
		ln = tls.NewListener(ln, &tls.Config{Certificates: []tls.Certificate{cert.TLS}, MinVersion: tls.VersionTLS12})
	}
	r.Hosts = append(r.Hosts, "127.0.0.1")
	r.URL = scheme + "://" + net.JoinHostPort(r.Hosts[0], strconv.Itoa(port))
	r.Server = New(be, store, opts)

	runCtx, cancel := context.WithCancel(ctx)
	r.cancel = cancel
	r.httpSrv = &http.Server{Handler: r.Server.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		defer close(r.done)
		if err := r.httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			cancel()
		}
	}()
	go func() { defer close(r.runDone); r.Server.Run(runCtx) }()
	_ = store.WriteAdvert(auth.Advert{
		URL: r.URL, Hosts: r.Hosts, Port: port, TLS: ro.LAN, Fingerprint: r.Cert.Fingerprint,
		PID: os.Getpid(), StartedAt: time.Now().UTC(),
	})
	return r, nil
}

// Stop shuts the server down.
func (r *Running) Stop() {
	r.cancel()
	_ = r.Store.ClearAdvert()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = r.httpSrv.Shutdown(ctx)
	_ = r.httpSrv.Close() // sockets Shutdown left behind: WebSockets are hijacked
	<-r.done
	<-r.runDone
}

// Done is closed once the HTTP server has stopped.
func (r *Running) Done() <-chan struct{} { return r.done }

// Pairing arms a code and returns what to show: the QR's URL and, over
// TLS, the invite line a person can type instead of scanning.
func (r *Running) Pairing(project string) (auth.Code, Pairing, error) {
	code, err := r.Store.Arm()
	if err != nil {
		return auth.Code{}, Pairing{}, err
	}
	return code, NewPairing(auth.Advert{URL: r.URL, Hosts: r.Hosts, Port: r.Port, TLS: r.TLS, Fingerprint: r.Cert.Fingerprint}, code, project), nil
}

// Pairing is what a person is shown to pair a phone.
type Pairing struct {
	// URL carries the code in its fragment, so no request line sees it.
	URL string
	// Invite is the deck://pair?… line (R5); "" over plain HTTP, which
	// has no fingerprint to name.
	Invite string
}

// NewPairing builds the URL and the invite for an armed code.
func NewPairing(a auth.Advert, code auth.Code, project string) Pairing {
	p := Pairing{URL: a.URL + "/#pair=" + code.Code}
	if !a.TLS || a.Fingerprint == "" || len(a.Hosts) == 0 {
		return p
	}
	inv := peer.Invite{Host: a.Hosts[0], Port: strconv.Itoa(a.Port), Code: code.Code, Fingerprint: a.Fingerprint, Expires: code.ExpiresAt}
	if deckconfig.ValidPeerName(project) {
		inv.Name = project
	}
	for _, h := range a.Hosts[1:] {
		if h != "127.0.0.1" && len(inv.Alt) < 3 {
			inv.Alt = append(inv.Alt, h)
		}
	}
	p.Invite = inv.Encode()
	return p
}

// active is the server this process runs for the TUI; nil when none.
var active atomic.Pointer[Running]

// SetActive records r as this process's server (nil clears it).
func SetActive(r *Running) { active.Store(r) }

// Active returns the server started with --remote, or nil.
func Active() *Running { return active.Load() }
