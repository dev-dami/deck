package mobilecontrol

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/dev-dami/deck/internal/deck/peer"
)

// A live supervisor, authenticated with a random local capability, owns its
// child. We never signal a PID read from disk: PID reuse cannot stop a stranger.
type managedState struct {
	Address string `json:"address"`
	Token   string `json:"token"`
	Role    string `json:"role"`
	PID     int    `json:"pid"`
}

func statePath(dir, role string) string { return filepath.Join(dir, role+"-managed.json") }
func managedRequest(ctx context.Context, dir, role, action string) error {
	raw, err := os.ReadFile(statePath(dir, role))
	if err != nil {
		return err
	}
	var s managedState
	if json.Unmarshal(raw, &s) != nil || s.Role != role {
		return errors.New("invalid managed process record")
	}
	host, _, err := net.SplitHostPort(s.Address)
	if err != nil || host != "127.0.0.1" || len(s.Token) != 64 {
		return errors.New("invalid managed process endpoint")
	}
	req, err := http.NewRequestWithContext(ctx, "POST", "http://"+s.Address+"/"+action, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+s.Token)
	client := http.Client{Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		return errors.New("managed process is no longer reachable")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return errors.New("managed process identity check failed")
	}
	return nil
}
func managedAlive(ctx context.Context, dir, role string) bool {
	return managedRequest(ctx, dir, role, "status") == nil
}
func stopRole(ctx context.Context, dir, role string) error {
	if err := managedRequest(ctx, dir, role, "stop"); err != nil {
		return err
	}
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if !managedAlive(ctx, dir, role) {
			return nil
		}
		if err := pause(ctx); err != nil {
			return err
		}
	}
	return fmt.Errorf("managed %s did not stop", role)
}
func stopManaged(ctx context.Context, dir string) (Result, error) {
	r, _ := currentStatus(ctx, dir)
	var messages []string
	if managedAlive(ctx, dir, "daemon") {
		if err := stopRole(ctx, dir, "daemon"); err != nil {
			return Result{}, err
		}
	} else if _, err := status(ctx, dir); err == nil {
		messages = append(messages, "The separately started daemon is still running; stop it with Ctrl+C in its own terminal.")
	}
	if managedAlive(ctx, dir, "relay") {
		if err := stopRole(ctx, dir, "relay"); err != nil {
			return Result{}, err
		}
	} else {
		messages = append(messages, "Any separately started relay is left running; stop it in its own terminal.")
	}
	if current, err := status(ctx, dir); err == nil {
		r = current
	} else {
		r.Online = false
	}
	r.Message = "Stopped processes managed by mobile connect. " + strings.Join(messages, " ")
	return r, nil
}

func startManaged(ctx context.Context, opt Options, role, exe string, args []string, cwd string) error {
	if managedAlive(ctx, opt.Dir, role) {
		return nil
	}
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		return err
	}
	raw, _ := json.Marshal(struct {
		Executable string
		Args       []string
		Cwd        string
	}{exe, args, cwd})
	child := exec.Command(opt.Executable, "mobile", "managed", "--role", role)
	child.Env = append(os.Environ(), "DECK_MOBILE_DIR="+opt.Dir, "DECK_MOBILE_MANAGED_TOKEN="+hex.EncodeToString(token), "DECK_MOBILE_MANAGED_SPEC="+string(raw))
	detach(child)
	log, err := os.OpenFile(filepath.Join(opt.Dir, role+"-managed.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer log.Close()
	child.Stdout = log
	child.Stderr = log
	if err := child.Start(); err != nil {
		return fmt.Errorf("could not start managed %s: %w", role, err)
	}
	go func() { _ = child.Wait() }()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if managedAlive(ctx, opt.Dir, role) {
			return nil
		}
		if err := pause(ctx); err != nil {
			return err
		}
	}
	return fmt.Errorf("could not start managed %s; inspect %s", role, filepath.Join(opt.Dir, role+"-managed.log"))
}

// ServeManaged is the hidden CLI supervisor entrypoint. Its child specification
// and authentication capability are inherited through the environment only.
func ServeManaged(ctx context.Context, dir, role string) error {
	if role != "daemon" && role != "relay" {
		return errors.New("invalid managed process role")
	}
	token := os.Getenv("DECK_MOBILE_MANAGED_TOKEN")
	raw := os.Getenv("DECK_MOBILE_MANAGED_SPEC")
	_ = os.Unsetenv("DECK_MOBILE_MANAGED_TOKEN")
	_ = os.Unsetenv("DECK_MOBILE_MANAGED_SPEC")
	if len(token) != 64 {
		return errors.New("managed startup requires a private capability")
	}
	var spec struct {
		Executable string
		Args       []string
		Cwd        string
	}
	if json.Unmarshal([]byte(raw), &spec) != nil || spec.Executable == "" {
		return errors.New("invalid managed process specification")
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer listener.Close()
	child := exec.Command(spec.Executable, spec.Args...)
	child.Dir = spec.Cwd
	child.Stdout = os.Stdout
	child.Stderr = os.Stderr
	child.Env = append(os.Environ(), "DECK_MOBILE_DIR="+dir)
	detach(child)
	if err := child.Start(); err != nil {
		return err
	}
	// Keep the actual child handle, rather than trusting an on-disk PID.
	done := make(chan error, 1)
	go func() { done <- child.Wait() }()
	record, _ := json.Marshal(managedState{Address: listener.Addr().String(), Token: token, Role: role, PID: child.Process.Pid})
	if err := peer.WritePrivateFile(statePath(dir, role), record); err != nil {
		_ = stopChild(child)
		return err
	}
	defer os.Remove(statePath(dir, role))
	stop := make(chan struct{}, 1)
	server := &http.Server{ReadHeaderTimeout: 3 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if r.Method != "POST" || subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
			http.Error(w, "unauthorized", 401)
			return
		}
		switch r.URL.Path {
		case "/status":
			w.WriteHeader(200)
		case "/stop":
			w.WriteHeader(200)
			_ = http.NewResponseController(w).Flush()
			select {
			case stop <- struct{}{}:
			default:
			}
		default:
			http.NotFound(w, r)
		}
	})}
	defer server.Close()
	go func() { _ = server.Serve(listener) }()
	cancelled := ctx.Done()
	for {
		select {
		case err := <-done:
			return err
		case <-stop:
		case <-cancelled:
			cancelled = nil
		}
		err := stopChild(child)
		select {
		case <-done:
			return nil
		case <-time.After(2 * time.Second):
		}
		if forceErr := forceChild(child); forceErr != nil {
			err = forceErr
		}
		select {
		case <-done:
			return nil
		case <-time.After(2 * time.Second):
		}
		// Keep the authenticated endpoint and ownership record until the actual
		// child exits. A caller sees a stop timeout instead of a false success.
		fmt.Fprintf(os.Stderr, "managed %s could not stop its child: %v; ownership retained\n", role, err)
	}
}
