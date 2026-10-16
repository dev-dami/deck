// Package relaytest is an in-process relay for the host side: it speaks the
// relay/v1 host protocol of docs/mobile-contract.md §5 well enough to test
// pkg/remote/relayclient (challenge, auth, lanes, mail), the way
// remotetest stands in for Deck.
package relaytest

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/dev-dami/deck/pkg/remote/identity"
	"github.com/dev-dami/deck/pkg/remote/wire"
)

// Relay is the fake. Create it with New.
type Relay struct {
	// URL is the base URL to give the client.
	URL string
	// Frames receives every frame the host sends after authenticating.
	Frames chan wire.HostFrame

	srv *httptest.Server
	up  websocket.Upgrader

	mu         sync.Mutex
	registered map[string]ed25519.PublicKey // cid -> key
	host       *websocket.Conn
	hosts      int
	wmu        sync.Mutex
	// AuthFailures counts hosts that failed the challenge.
	authFailures int
}

// New starts a fake relay and stops it when the test ends.
func New(t *testing.T) *Relay {
	t.Helper()
	r := &Relay{Frames: make(chan wire.HostFrame, 256), registered: map[string]ed25519.PublicKey{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/r/v1/register", r.register)
	mux.HandleFunc("/r/v1/c/", r.hostWS)
	r.srv = httptest.NewServer(mux)
	r.URL = r.srv.URL
	t.Cleanup(r.srv.Close)
	return r
}

// Register records a computer as if it had registered, so a test can start
// with the cid already known.
func (r *Relay) Register(id *identity.Identity) {
	r.mu.Lock()
	r.registered[id.CID()] = id.RelayPublic()
	r.mu.Unlock()
}

func (r *Relay) register(w http.ResponseWriter, req *http.Request) {
	var body wire.RegisterRequest
	if json.NewDecoder(req.Body).Decode(&body) != nil {
		http.Error(w, "bad", http.StatusBadRequest)
		return
	}
	raw, err := base64.RawURLEncoding.DecodeString(body.RelayPub)
	if err != nil || len(raw) != ed25519.PublicKeySize {
		http.Error(w, "bad key", http.StatusBadRequest)
		return
	}
	cid := identity.CIDOf(raw)
	r.mu.Lock()
	r.registered[cid] = ed25519.PublicKey(raw)
	r.mu.Unlock()
	_ = json.NewEncoder(w).Encode(wire.RegisterResponse{CID: cid})
}

func (r *Relay) hostWS(w http.ResponseWriter, req *http.Request) {
	parts := strings.Split(strings.TrimPrefix(req.URL.Path, "/r/v1/c/"), "/")
	if len(parts) != 2 || parts[1] != "host" {
		http.NotFound(w, req)
		return
	}
	r.mu.Lock()
	pub, known := r.registered[parts[0]]
	r.mu.Unlock()
	if !known {
		http.Error(w, "unknown computer", http.StatusNotFound)
		return
	}
	ws, err := r.up.Upgrade(w, req, nil)
	if err != nil {
		return
	}
	nonce := make([]byte, 16)
	_, _ = rand.Read(nonce)
	n := base64.RawURLEncoding.EncodeToString(nonce)
	_ = ws.WriteJSON(wire.HostFrame{T: wire.HostChallenge, Nonce: n})
	var auth wire.HostFrame
	sig, _ := base64.RawURLEncoding.DecodeString("")
	if ws.ReadJSON(&auth) == nil {
		sig, _ = base64.RawURLEncoding.DecodeString(auth.Sig)
	}
	if auth.T != wire.HostAuth || !ed25519.Verify(pub, []byte(wire.RelayChallengeContext+parts[0]+"|"+n), sig) {
		r.mu.Lock()
		r.authFailures++
		r.mu.Unlock()
		_ = ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(wire.CloseTokenInvalid, "bad signature"), time.Now().Add(time.Second))
		ws.Close()
		return
	}
	r.mu.Lock()
	if r.host != nil {
		_ = r.host.Close() // the newest host wins
	}
	r.host = ws
	r.hosts++
	r.mu.Unlock()
	r.wmu.Lock()
	_ = ws.WriteJSON(wire.HostFrame{T: wire.HostReady})
	r.wmu.Unlock()
	for {
		var f wire.HostFrame
		if err := ws.ReadJSON(&f); err != nil {
			return
		}
		select {
		case r.Frames <- f:
		default:
		}
	}
}

// Hosts is how many authenticated host connections the relay has accepted.
func (r *Relay) Hosts() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.hosts
}

// AuthFailures is how many hosts failed the challenge.
func (r *Relay) AuthFailures() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.authFailures
}

// Send writes a frame to the connected host.
func (r *Relay) Send(f wire.HostFrame) bool {
	r.mu.Lock()
	ws := r.host
	r.mu.Unlock()
	if ws == nil {
		return false
	}
	r.wmu.Lock()
	defer r.wmu.Unlock()
	return ws.WriteJSON(f) == nil
}

// DropHost closes the host connection as a relay restart would.
func (r *Relay) DropHost() {
	r.mu.Lock()
	ws := r.host
	r.host = nil
	r.mu.Unlock()
	if ws != nil {
		_ = ws.Close()
	}
}

// OpenLane tells the host a phone connected.
func (r *Relay) OpenLane(lane, deviceHash string, pair bool) bool {
	return r.Send(wire.HostFrame{T: wire.HostOpen, Lane: lane, Device: deviceHash, Pair: pair})
}

// PhoneData delivers a Noise message from a phone on lane.
func (r *Relay) PhoneData(lane string, msg []byte) bool {
	return r.Send(wire.HostFrame{T: wire.HostData, Lane: lane, B64: base64.StdEncoding.EncodeToString(msg)})
}

// Mail delivers queued mail for a device.
func (r *Relay) Mail(id, deviceHash string, data []byte) bool {
	return r.Send(wire.HostFrame{T: wire.HostMail, ID: id, Device: deviceHash, B64: base64.StdEncoding.EncodeToString(data)})
}

// WaitFrame returns the next frame of type t the host sent, or false after
// the timeout.
func (r *Relay) WaitFrame(t string, timeout time.Duration) (wire.HostFrame, bool) {
	deadline := time.After(timeout)
	for {
		select {
		case f := <-r.Frames:
			if f.T == t {
				return f, true
			}
		case <-deadline:
			return wire.HostFrame{}, false
		}
	}
}
