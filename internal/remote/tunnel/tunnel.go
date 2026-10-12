// Package tunnel exposes the local remote-control port beyond the LAN by
// driving a tunnel program the user installed themselves. Deck never downloads
// one.
package tunnel

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Kinds accepted by New.
const (
	Cloudflare = "cloudflare"
	Tailscale  = "tailscale"
)

// startTimeout is how long a tunnel program gets to report its public address.
var startTimeout = 30 * time.Second

// Tunnel is a running tunnel to a local port.
type Tunnel interface {
	Kind() string
	// Start returns the public base URL (https://host) once the tunnel is up.
	Start(ctx context.Context, localAddr string) (string, error)
	Close() error
}

// ErrNotInstalled says the tunnel program is missing; its message is the
// install hint shown to the user.
type ErrNotInstalled struct {
	Program, Hint string
}

func (e *ErrNotInstalled) Error() string {
	return fmt.Sprintf("%s is not installed (%s)", e.Program, e.Hint)
}

// New returns the tunnel of the given kind.
func New(kind string) (Tunnel, error) {
	switch kind {
	case Cloudflare:
		return &cloudflared{command: exec.CommandContext, lookPath: exec.LookPath}, nil
	case Tailscale:
		return &tailscale{command: exec.CommandContext, lookPath: exec.LookPath}, nil
	}
	return nil, fmt.Errorf("unknown tunnel %q (use %s or %s)", kind, Cloudflare, Tailscale)
}

type commandFunc func(ctx context.Context, name string, args ...string) *exec.Cmd

var quickTunnelURL = regexp.MustCompile(`https://[a-z0-9-]+\.trycloudflare\.com`)

// parseQuickTunnelURL finds a quick tunnel address in a line of cloudflared's
// log.
func parseQuickTunnelURL(line string) string { return quickTunnelURL.FindString(line) }

type cloudflared struct {
	command  commandFunc
	lookPath func(string) (string, error)

	mu  sync.Mutex
	cmd *exec.Cmd
}

func (c *cloudflared) Kind() string { return Cloudflare }

func (c *cloudflared) Start(ctx context.Context, localAddr string) (string, error) {
	path, err := c.lookPath("cloudflared")
	if err != nil {
		return "", &ErrNotInstalled{Program: "cloudflared", Hint: "https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/downloads/"}
	}
	cmd := c.command(ctx, path, "tunnel", "--no-autoupdate", "--url", "http://"+localAddr)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", err
	}
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start cloudflared: %w", err)
	}
	c.mu.Lock()
	c.cmd = cmd
	c.mu.Unlock()

	found := make(chan string, 1)
	tail := &ringLines{max: 8}
	go func() {
		sc := bufio.NewScanner(stderr)
		sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
		sent := false
		for sc.Scan() {
			line := sc.Text()
			tail.add(line)
			if u := parseQuickTunnelURL(line); u != "" && !sent {
				sent = true
				found <- u
			}
		}
		// Keep draining so cloudflared never blocks on a full pipe.
		_, _ = io.Copy(io.Discard, stderr)
		close(found)
	}()

	select {
	case u, ok := <-found:
		if !ok {
			_ = c.Close()
			return "", fmt.Errorf("cloudflared exited before opening a tunnel:\n%s", tail.String())
		}
		return u, nil
	case <-time.After(startTimeout):
		_ = c.Close()
		return "", fmt.Errorf("cloudflared gave no tunnel address within %s:\n%s", startTimeout, tail.String())
	case <-ctx.Done():
		_ = c.Close()
		return "", ctx.Err()
	}
}

func (c *cloudflared) Close() error {
	c.mu.Lock()
	cmd := c.cmd
	c.cmd = nil
	c.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	err := cmd.Process.Kill()
	_ = cmd.Wait()
	if err != nil && !errors.Is(err, os.ErrProcessDone) && !strings.Contains(err.Error(), "already finished") {
		return err
	}
	return nil
}

type ringLines struct {
	mu    sync.Mutex
	max   int
	lines []string
}

func (r *ringLines) add(l string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lines = append(r.lines, l)
	if len(r.lines) > r.max {
		r.lines = r.lines[1:]
	}
}

func (r *ringLines) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.lines, "\n")
}

type tailscale struct {
	command  commandFunc
	lookPath func(string) (string, error)

	mu      sync.Mutex
	started bool
	path    string
}

func (t *tailscale) Kind() string { return Tailscale }

type tsStatus struct {
	Self struct {
		DNSName string `json:"DNSName"`
	} `json:"Self"`
	BackendState string `json:"BackendState"`
}

// tailnetHost reads this machine's MagicDNS name from `tailscale status --json`.
func tailnetHost(out []byte) (string, error) {
	var st tsStatus
	if err := json.Unmarshal(out, &st); err != nil {
		return "", fmt.Errorf("read tailscale status: %w", err)
	}
	if st.BackendState != "" && st.BackendState != "Running" {
		return "", fmt.Errorf("tailscale is %s; run `tailscale up`", st.BackendState)
	}
	host := strings.TrimSuffix(st.Self.DNSName, ".")
	if host == "" {
		return "", errors.New("tailscale reports no DNS name; enable MagicDNS and HTTPS certificates for your tailnet")
	}
	return host, nil
}

func (t *tailscale) Start(ctx context.Context, localAddr string) (string, error) {
	path, err := t.lookPath("tailscale")
	if err != nil {
		return "", &ErrNotInstalled{Program: "tailscale", Hint: "https://tailscale.com/download"}
	}
	var stdout bytes.Buffer
	st := t.command(ctx, path, "status", "--json")
	st.Stdout = &stdout
	if err := st.Run(); err != nil {
		return "", fmt.Errorf("tailscale status: %w", err)
	}
	host, err := tailnetHost(stdout.Bytes())
	if err != nil {
		return "", err
	}
	var combined bytes.Buffer
	serve := t.command(ctx, path, "serve", "--bg", "--https=443", "http://"+localAddr)
	serve.Stdout, serve.Stderr = &combined, &combined
	if err := serve.Run(); err != nil {
		return "", fmt.Errorf("tailscale serve: %w: %s", err, strings.TrimSpace(combined.String()))
	}
	t.mu.Lock()
	t.started, t.path = true, path
	t.mu.Unlock()
	return "https://" + host, nil
}

// Close undoes the serve config Start added, so Deck leaves the user's
// tailscale as it found it.
func (t *tailscale) Close() error {
	t.mu.Lock()
	started, path := t.started, t.path
	t.started = false
	t.mu.Unlock()
	if !started {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var out bytes.Buffer
	cmd := t.command(ctx, path, "serve", "--https=443", "off")
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("tailscale serve off: %w: %s", err, strings.TrimSpace(out.String()))
	}
	return nil
}
