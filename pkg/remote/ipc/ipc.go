// Package ipc joins the daemon to the processes that own a project.
//
// A provider (the TUI, or a headless `deck remote host`) runs the real
// remote.Backend for one project. It dials the daemon's loopback endpoint,
// proves it holds the secret file, and from then on the daemon calls the
// backend through a Proxy that implements remote.Backend itself. The daemon
// therefore never imports internal/workspace (docs/mobile-contract.md, plan
// §1).
//
// The wire is one JSON object per line over the connection; either side may
// have dialed. Everything the provider returns is data, never a pointer to
// provider state.
package ipc

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/dev-dami/deck/internal/deck/peer"
	"github.com/dev-dami/deck/pkg/remote"
	"github.com/dev-dami/deck/pkg/remote/wire"
)

// Limits.
const (
	maxLine      = 32 << 20 // an image upload plus overhead
	helloTimeout = 5 * time.Second
	callTimeout  = 2 * time.Minute
	eventBuffer  = 1024
	secretBytes  = 32
)

// ErrOffline is returned by a Proxy whose provider has gone away.
var ErrOffline = errors.New("ipc: provider offline")

// ErrBadHello is returned by Accept for a connection that did not prove the
// secret or sent something other than a hello.
var ErrBadHello = errors.New("ipc: bad hello")

type hello struct {
	V      int    `json:"v"`
	Secret string `json:"secret"`
	// Role is "" or "provider" for a project provider, "control" for the CLI.
	Role string `json:"role,omitempty"`
	PJ   string `json:"pj,omitempty"`
}

// Roles a connection can announce.
const (
	RoleProvider = "provider"
	RoleControl  = "control"
)

type frame struct {
	T  string          `json:"t"` // call, ret, ev
	ID uint64          `json:"id,omitempty"`
	M  string          `json:"m,omitempty"`
	A  *args           `json:"a,omitempty"`
	R  json.RawMessage `json:"r,omitempty"`
	E  *errBody        `json:"e,omitempty"`
	Ev *evBody         `json:"ev,omitempty"`
	// N is the text of a "notice" frame (daemon to provider): something a
	// person at the provider's screen should see.
	N string `json:"n,omitempty"`
}

type errBody struct {
	Kind string `json:"kind"` // not_found, invalid, other
	Msg  string `json:"msg"`
}

type evBody struct {
	Name      string          `json:"name"`
	SessionID string          `json:"session_id,omitempty"`
	Scoped    bool            `json:"scoped,omitempty"`
	Payload   json.RawMessage `json:"payload,omitempty"`
}

// args is the union of every Backend method's parameters.
type args struct {
	SessionID string  `json:"session_id,omitempty"`
	ID        string  `json:"id,omitempty"`
	Title     string  `json:"title,omitempty"`
	Before    string  `json:"before,omitempty"`
	Limit     int     `json:"limit,omitempty"`
	Text      string  `json:"text,omitempty"`
	MessageID string  `json:"message_id,omitempty"`
	Agent     string  `json:"agent,omitempty"`
	Kind      string  `json:"kind,omitempty"`
	Model     string  `json:"model,omitempty"`
	Effort    string  `json:"effort,omitempty"`
	Mode      string  `json:"mode,omitempty"`
	Decision  string  `json:"decision,omitempty"`
	Images    []image `json:"images,omitempty"`
}

type image struct {
	Name string `json:"name"`
	Mime string `json:"mime"`
	Data []byte `json:"data"`
}

type infoBody struct {
	Version string     `json:"version"`
	Project string     `json:"project"`
	Kinds   []string   `json:"kinds"`
	Theme   wire.Theme `json:"theme"`
	// UI says a person is looking at this provider (the terminal UI), so the
	// daemon may show it notices and give it time to cancel queued work.
	UI bool `json:"ui,omitempty"`
}

type messagesBody struct {
	Messages []wire.Message `json:"messages"`
	More     bool           `json:"more"`
	Next     string         `json:"next"`
}

// Endpoint is where the daemon listens and the secret a provider must hold.
type Endpoint struct {
	Addr   string `json:"addr"`
	Secret string `json:"secret"`
}

const endpointFile = "ipc.json"

// WriteEndpoint publishes the daemon's endpoint to dir, readable only by the
// current user.
func WriteEndpoint(dir string, e Endpoint) error {
	raw, err := json.Marshal(e)
	if err != nil {
		return err
	}
	return peer.WritePrivateFile(filepath.Join(dir, endpointFile), raw)
}

// ReadEndpoint reads the endpoint a running daemon published.
func ReadEndpoint(dir string) (Endpoint, error) {
	raw, err := os.ReadFile(filepath.Join(dir, endpointFile))
	if err != nil {
		return Endpoint{}, err
	}
	var e Endpoint
	if err := json.Unmarshal(raw, &e); err != nil || e.Addr == "" || e.Secret == "" {
		return Endpoint{}, errors.New("ipc: endpoint file is not valid")
	}
	return e, nil
}

// NewSecret makes a random secret.
func NewSecret() (string, error) {
	b := make([]byte, secretBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// Listen opens the daemon's loopback listener on a free port.
func Listen() (net.Listener, error) { return net.Listen("tcp", "127.0.0.1:0") }

// Accept takes one provider connection from ln, checks its hello against
// secret in constant time and returns the project it serves. A control
// connection is refused; use AcceptAny to take both.
func Accept(ln net.Listener, secret string) (pj string, conn net.Conn, err error) {
	role, pj, conn, err := AcceptAny(ln, secret)
	if err != nil {
		return "", nil, err
	}
	if role != RoleProvider {
		conn.Close()
		return "", nil, ErrBadHello
	}
	return pj, conn, nil
}

// AcceptAny is Accept for either role.
func AcceptAny(ln net.Listener, secret string) (role, pj string, conn net.Conn, err error) {
	conn, err = ln.Accept()
	if err != nil {
		return "", "", nil, err
	}
	h, err := readHello(conn, secret)
	if err != nil {
		conn.Close()
		return "", "", nil, err
	}
	if h.Role == RoleControl {
		return RoleControl, "", conn, nil
	}
	return RoleProvider, h.PJ, conn, nil
}

func readHello(conn net.Conn, secret string) (hello, error) {
	if err := conn.SetReadDeadline(time.Now().Add(helloTimeout)); err != nil {
		return hello{}, err
	}
	defer conn.SetReadDeadline(time.Time{})
	// Read byte by byte: anything buffered past the newline would be lost to
	// the frame reader that takes over the connection, and a stranger must not
	// make us buffer more than a hello's worth.
	var line []byte
	one := make([]byte, 1)
	for {
		if len(line) >= 1<<10 {
			return hello{}, ErrBadHello
		}
		if _, err := conn.Read(one); err != nil {
			return hello{}, ErrBadHello
		}
		if one[0] == '\n' {
			break
		}
		line = append(line, one[0])
	}
	var h hello
	if json.Unmarshal(line, &h) != nil || h.V != 1 {
		return hello{}, ErrBadHello
	}
	switch h.Role {
	case "", RoleProvider:
		if h.PJ == "" {
			return hello{}, ErrBadHello
		}
	case RoleControl:
	default:
		return hello{}, ErrBadHello
	}
	if subtle.ConstantTimeCompare([]byte(h.Secret), []byte(secret)) != 1 {
		return hello{}, ErrBadHello
	}
	return h, nil
}

// Dial connects to the daemon as the provider of project pj.
func Dial(ctx context.Context, e Endpoint, pj string) (net.Conn, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", e.Addr)
	if err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(hello{V: 1, Secret: e.Secret, Role: RoleProvider, PJ: pj})
	if _, err := conn.Write(append(raw, '\n')); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}

// writer serialises frames onto a connection.
type writer struct {
	mu sync.Mutex
	w  io.Writer
}

func (w *writer) send(f frame) error {
	raw, err := json.Marshal(f)
	if err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	_, err = w.w.Write(append(raw, '\n'))
	return err
}

func scanner(r io.Reader) *bufio.Scanner {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), maxLine)
	return sc
}

func toErrBody(err error) *errBody {
	switch {
	case errors.Is(err, remote.ErrNotFound):
		return &errBody{Kind: "not_found", Msg: err.Error()}
	case errors.Is(err, remote.ErrInvalid):
		return &errBody{Kind: "invalid", Msg: err.Error()}
	}
	return &errBody{Kind: "other", Msg: err.Error()}
}

func fromErrBody(e *errBody) error {
	switch e.Kind {
	case "not_found":
		return fmt.Errorf("%w: %s", remote.ErrNotFound, strings.TrimPrefix(e.Msg, remote.ErrNotFound.Error()+": "))
	case "invalid":
		return fmt.Errorf("%w: %s", remote.ErrInvalid, strings.TrimPrefix(e.Msg, remote.ErrInvalid.Error()+": "))
	}
	return errors.New(e.Msg)
}
