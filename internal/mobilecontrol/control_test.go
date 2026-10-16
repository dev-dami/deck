package mobilecontrol

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dev-dami/deck/pkg/remote/devstore"
	"github.com/dev-dami/deck/pkg/remote/ipc"
	"github.com/dev-dami/deck/pkg/remote/mobile"
	"github.com/dev-dami/deck/pkg/remote/projects"
)

func fakeControl(t *testing.T, dir string, st mobile.StatusResult, hooks ...ipc.ControlHandler) {
	t.Helper()
	listener, err := ipc.Listen()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	ep := ipc.Endpoint{Addr: listener.Addr().String(), Secret: strings.Repeat("a", 64)}
	if err := ipc.WriteEndpoint(dir, ep); err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			_, _, conn, err := ipc.AcceptAny(listener, ep.Secret)
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				_ = ipc.ServeControl(context.Background(), conn, func(_ context.Context, cmd string, _ json.RawMessage) (any, error) {
					if len(hooks) > 0 {
						return hooks[0](context.Background(), cmd, nil)
					}
					if cmd == "pair" {
						return mobile.PairResult{URL: "https://relay.test/#secret-pair", ExpiresAt: time.Now().Add(time.Minute)}, nil
					}
					return st, nil
				})
			}()
		}
	}()
}

func TestReuseDaemonAndExplicitSharing(t *testing.T) {
	dir := t.TempDir()
	cfg := mobile.Config{Name: "PC", RelayURL: "https://relay.test"}
	if err := mobile.SaveConfig(dir, cfg); err != nil {
		t.Fatal(err)
	}
	fakeControl(t, dir, mobile.StatusResult{Name: "PC", RelayURL: cfg.RelayURL, RelayOnline: true, Devices: 2})
	store, err := devstore.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	for i := byte(1); i <= 2; i++ {
		key := make([]byte, 32)
		key[0] = i
		if _, _, err := store.Add("phone", key); err != nil {
			t.Fatal(err)
		}
	}
	project := t.TempDir()
	result, err := Run(context.Background(), Options{Dir: dir, Project: project})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Online || result.Devices != 2 || result.PairURL == "" {
		t.Fatalf("unexpected result: %+v", result)
	}
	reg, err := projects.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.List()) != 1 || reg.List()[0].Path != project {
		t.Fatal("explicit project not shared")
	}
	for _, name := range []string{"daemon-managed.json", "relay-managed.json"} {
		if _, e := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(e) {
			t.Fatal("existing daemon should not spawn helpers")
		}
	}
	files, _ := os.ReadDir(dir)
	for _, file := range files {
		b, _ := os.ReadFile(filepath.Join(dir, file.Name()))
		if strings.Contains(string(b), "secret-pair") {
			t.Fatal("pair secret persisted")
		}
	}
}

func TestStatusPairDoNotShareOrOverwriteSettings(t *testing.T) {
	dir := t.TempDir()
	cfg := mobile.Config{Name: "PC", RelayURL: "https://relay.test"}
	if err := mobile.SaveConfig(dir, cfg); err != nil {
		t.Fatal(err)
	}
	fakeControl(t, dir, mobile.StatusResult{Name: "PC", RelayURL: cfg.RelayURL, RelayOnline: true})
	for _, action := range []string{"status", "pair"} {
		if _, err := Run(context.Background(), Options{Dir: dir, Action: action, Project: t.TempDir(), LAN: true, LANIP: "invalid"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, e := os.Stat(filepath.Join(dir, "projects.json")); !os.IsNotExist(e) {
		t.Fatal("read actions must not share projects")
	}
	actual, _ := mobile.LoadConfig(dir)
	if !sameConfig(actual, cfg) {
		t.Fatal("read actions changed config")
	}
}

func TestStartErrorsWithoutMutation(t *testing.T) {
	for _, tc := range []struct {
		name string
		opt  Options
		want string
	}{{"unset", Options{}, "no relay configured"}, {"port", Options{Port: 65536}, "relay port"}, {"ip", Options{LAN: true, LANIP: "203.0.113.2"}, "--lan-ip"}, {"action", Options{Action: "erase"}, "action must"}} {
		t.Run(tc.name, func(t *testing.T) {
			tc.opt.Dir = t.TempDir()
			_, err := Run(context.Background(), tc.opt)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v", err)
			}
			if _, e := os.Stat(filepath.Join(tc.opt.Dir, "mobile.json")); !os.IsNotExist(e) {
				t.Fatal("failed validation wrote config")
			}
		})
	}
	if _, err := relaySource(Options{Dir: t.TempDir(), RelayDir: "missing"}); err == nil {
		t.Fatal("expected missing runtime")
	}
}

func TestForeignPortRejectedAndRelayProbe(t *testing.T) {
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/vapid") {
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"error":"push_unavailable"}`))
			return
		}
		w.WriteHeader(426)
	}))
	defer good.Close()
	if !relayProbe(context.Background(), good.URL) {
		t.Fatal("relay should match")
	}
	foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("hello")) }))
	defer foreign.Close()
	if relayProbe(context.Background(), foreign.URL) {
		t.Fatal("foreign service matched")
	}
	u, _ := url.Parse(foreign.URL)
	port, _ := strconv.Atoi(u.Port())
	if err := ensureRelay(context.Background(), Options{Dir: t.TempDir(), Port: port}); err == nil || !strings.Contains(err.Error(), "already used") {
		t.Fatalf("foreign port: %v", err)
	}
}

func TestMissingNodeHasActionableError(t *testing.T) {
	source := t.TempDir()
	_ = os.MkdirAll(filepath.Join(source, "node_modules", "wrangler", "bin"), 0700)
	_ = os.WriteFile(filepath.Join(source, "wrangler.jsonc"), []byte("{}"), 0600)
	_ = os.WriteFile(filepath.Join(source, "node_modules", "wrangler", "bin", "wrangler.js"), nil, 0600)
	t.Setenv("PATH", "")
	if err := ensureRelay(context.Background(), Options{Dir: t.TempDir(), RelayDir: source, Port: 1}); err == nil || !strings.Contains(err.Error(), "Node.js") {
		t.Fatalf("missing runtime: %v", err)
	}
}

func TestManagedHelper(t *testing.T) {
	if os.Getenv("DECK_TEST_SUPERVISOR") != "1" {
		return
	}
	if err := ServeManaged(context.Background(), os.Getenv("DECK_MOBILE_DIR"), "daemon"); err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}
func TestManagedSleepChild(t *testing.T) {
	if os.Getenv("DECK_TEST_SUPERVISOR") == "1" {
		time.Sleep(time.Minute)
	}
}

func TestManagedLifecycleAndCapability(t *testing.T) {
	dir := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	spec, _ := json.Marshal(struct {
		Executable string
		Args       []string
		Cwd        string
	}{exe, []string{"-test.run=^TestManagedSleepChild$"}, ""})
	child := exec.Command(exe, "-test.run=^TestManagedHelper$")
	child.Env = append(os.Environ(), "DECK_TEST_SUPERVISOR=1", "DECK_MOBILE_DIR="+dir, "DECK_MOBILE_MANAGED_TOKEN="+strings.Repeat("b", 64), "DECK_MOBILE_MANAGED_SPEC="+string(spec))
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = child.Process.Kill() })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for !managedAlive(ctx, dir, "daemon") {
		if err := pause(ctx); err != nil {
			t.Fatal("supervisor did not start")
		}
	}
	b, err := os.ReadFile(statePath(dir, "daemon"))
	if err != nil {
		t.Fatal(err)
	}
	var state managedState
	_ = json.Unmarshal(b, &state)
	state.Token = strings.Repeat("c", 64)
	bad, _ := json.Marshal(state)
	_ = os.WriteFile(statePath(dir, "daemon"), bad, 0600)
	if managedAlive(ctx, dir, "daemon") {
		t.Fatal("wrong capability accepted")
	}
	_ = os.WriteFile(statePath(dir, "daemon"), b, 0600)
	if err := stopRole(ctx, dir, "daemon"); err != nil {
		t.Fatal(err)
	}
	if err := child.Wait(); err != nil {
		t.Fatal(err)
	}
	if managedAlive(ctx, dir, "daemon") {
		t.Fatal("supervisor still live")
	}
}

func TestManagedEndpointRejectsUntrustedIdentity(t *testing.T) {
	dir := t.TempDir()
	b, _ := json.Marshal(managedState{Address: "192.168.1.2:90", Token: strings.Repeat("a", 64), Role: "daemon", PID: 1})
	_ = os.WriteFile(statePath(dir, "daemon"), b, 0600)
	if managedAlive(context.Background(), dir, "daemon") {
		t.Fatal("nonloopback state accepted")
	}
	result, err := Run(context.Background(), Options{Dir: dir, Action: "stop"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Message, "separately started relay") {
		t.Fatal("missing external ownership explanation")
	}
}
