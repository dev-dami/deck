package rpc

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/dev-dami/deck/internal/db"
	"github.com/dev-dami/deck/internal/usage"
	"github.com/dev-dami/deck/pkg/remote/remotetest"
	"github.com/dev-dami/deck/pkg/rpc/deckv1"
)

// projectRig serves a fake backend with a project folder (and a usage
// record, when one is given) and dials it.
func projectRig(t *testing.T, dir string, u usage.Service) deckv1.DeckClient {
	t.Helper()
	srv := New(remotetest.New(), "secret").WithProject(dir, u)
	t.Cleanup(srv.Close)
	l, sock, closeSock, err := Listen(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	g := srv.GRPC()
	go func() { _ = g.Serve(l) }()
	t.Cleanup(func() { g.Stop(); closeSock() })
	cc, c, err := Dial(sock, "secret")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cc.Close() })
	return c
}

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestGitStatusCheckoutAndCommit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	for _, kv := range [][2]string{
		{"GIT_AUTHOR_NAME", "Test"}, {"GIT_AUTHOR_EMAIL", "t@example.com"},
		{"GIT_COMMITTER_NAME", "Test"}, {"GIT_COMMITTER_EMAIL", "t@example.com"},
		{"GIT_CONFIG_GLOBAL", os.DevNull}, {"GIT_CONFIG_NOSYSTEM", "1"},
	} {
		t.Setenv(kv[0], kv[1])
	}
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "-m", "first")

	c := projectRig(t, dir, nil)
	ctx := ctxT(t)
	st, err := c.GitStatus(ctx, &deckv1.Empty{})
	if err != nil {
		t.Fatal(err)
	}
	if !st.GetRepo() || st.GetBranch() != "main" || st.GetDirty() != 0 {
		t.Fatalf("status %+v", st)
	}
	if _, err := c.GitCheckout(ctx, &deckv1.GitCheckoutRequest{Branch: "feature/x", Create: true}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	st, _ = c.GitStatus(ctx, &deckv1.Empty{})
	if st.GetBranch() != "feature/x" || st.GetDirty() != 1 {
		t.Fatalf("after checkout: %+v", st)
	}
	if _, err := c.GitCommit(ctx, &deckv1.GitCommitRequest{Message: "Add b"}); err != nil {
		t.Fatal(err)
	}
	st, _ = c.GitStatus(ctx, &deckv1.Empty{})
	if st.GetDirty() != 0 || len(st.GetBranches()) != 2 || st.GetBranches()[0].GetSubject() != "Add b" {
		t.Fatalf("after commit: %+v", st)
	}
	// git's own words come back on a refused call.
	if _, err := c.GitCheckout(ctx, &deckv1.GitCheckoutRequest{Branch: "nope"}); err == nil {
		t.Fatal("a missing branch must fail")
	}
	if _, err := c.GitCommit(ctx, &deckv1.GitCommitRequest{Message: " "}); err == nil {
		t.Fatal("an empty message must be refused")
	}
}

func TestGitStatusOutsideARepo(t *testing.T) {
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(t.TempDir()))
	c := projectRig(t, t.TempDir(), nil)
	st, err := c.GitStatus(ctxT(t), &deckv1.Empty{})
	if err != nil {
		t.Fatal(err)
	}
	if st.GetRepo() {
		t.Fatalf("status %+v", st)
	}
}

func TestUsage(t *testing.T) {
	c := projectRig(t, t.TempDir(), nil)
	r, err := c.Usage(ctxT(t), &deckv1.UsageRequest{Days: 7})
	if err != nil || r.GetAvailable() {
		t.Fatalf("no record: %+v %v", r, err)
	}

	conn, err := db.Connect(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	svc := usage.NewServiceWithDB(db.New(conn), conn)
	at := time.Now().Add(-time.Minute)
	for _, rec := range []usage.Record{
		{SessionID: "s1", AgentID: "claude-1", Kind: "claude", Model: "opus", InputTokens: 1000, OutputTokens: 200, CostUSD: 0.5, Duration: time.Second, At: at},
		{SessionID: "s1", AgentID: "codex-1", Kind: "codex", Model: "gpt", InputTokens: 300, OutputTokens: 100, CostUSD: 0.1, Duration: time.Second, At: at},
	} {
		if err := svc.Record(t.Context(), rec); err != nil {
			t.Fatal(err)
		}
	}
	c = projectRig(t, t.TempDir(), svc)
	r, err = c.Usage(ctxT(t), &deckv1.UsageRequest{Days: 7})
	if err != nil {
		t.Fatal(err)
	}
	ov := r.GetOverview()
	if !r.GetAvailable() || ov.GetTurns() != 2 || ov.GetInputTokens() != 1300 || len(r.GetKinds()) != 2 || len(r.GetDays()) != 7 {
		t.Fatalf("usage %+v", r)
	}
	if h := r.GetHabits(); h == nil || len(h.GetHours()) != 7*24 {
		t.Fatalf("habits %+v", r.GetHabits())
	}
	if len(r.GetHours()) != 0 || len(r.GetLimitHistory()) != 0 {
		t.Fatalf("history without asking: %+v", r.GetHours())
	}
	if _, err := svc.RecordLimits(t.Context(), []usage.LimitSnapshot{{AgentKind: "codex", WindowID: "primary", UsedPct: 12, At: at}}); err != nil {
		t.Fatal(err)
	}
	r, err = c.Usage(ctxT(t), &deckv1.UsageRequest{Days: 7, HistoryDays: 14})
	if err != nil {
		t.Fatal(err)
	}
	if hs := r.GetHours(); len(hs) != 2 || hs[0].GetAgentId() != "claude-1" || hs[0].GetTokens() != 1200 || hs[0].GetAt()%3_600_000 != 0 {
		t.Fatalf("hours %+v", hs)
	}
	if lh := r.GetLimitHistory(); len(lh) != 1 || lh[0].GetUsedPct() != 12 {
		t.Fatalf("limit history %+v", lh)
	}
	if _, err := c.Usage(ctxT(t), &deckv1.UsageRequest{Days: -1}); err == nil {
		t.Fatal("a negative range must be refused")
	}
}

func TestTerminal(t *testing.T) {
	// The shell prints its folder (pwd, or the prompt on Windows), works out
	// hi-3 and exits with 7.
	script := "pwd; echo hi-$((1+2)); exit 7\n"
	if runtime.GOOS == "windows" {
		// PowerShell or cmd; both echo and both take exit 7.
		script = "echo hi-3\r\nexit 7\r\n"
	} else {
		t.Setenv("SHELL", "/bin/sh")
	}
	dir := t.TempDir()
	c := projectRig(t, dir, nil)
	ctx := ctxT(t)
	ref, err := c.TerminalOpen(ctx, &deckv1.TerminalOpenRequest{Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	out, err := c.TerminalOutput(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.TerminalResize(ctx, &deckv1.TerminalSize{TerminalId: ref.GetTerminalId(), Cols: 120, Rows: 40}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.TerminalWrite(ctx, &deckv1.TerminalInput{TerminalId: ref.GetTerminalId(), Data: []byte(script)}); err != nil {
		t.Fatal(err)
	}
	var seen strings.Builder
	code := int32(-100)
	for {
		ch, err := out.Recv()
		if err != nil {
			t.Fatalf("stream ended before the exit: %v (saw %q)", err, seen.String())
		}
		seen.Write(ch.GetData())
		if ch.GetExited() {
			code = ch.GetExitCode()
			break
		}
	}
	if !strings.Contains(seen.String(), "hi-3") || !strings.Contains(seen.String(), filepath.Base(dir)) || code != 7 {
		t.Fatalf("code %d, output %q", code, seen.String())
	}

	// A later subscriber gets the scrollback and the exit.
	again, err := c.TerminalOutput(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	first, err := again.Recv()
	if err != nil || !strings.Contains(string(first.GetData()), "hi-3") {
		t.Fatalf("scrollback %q %v", first.GetData(), err)
	}
	if last, err := again.Recv(); err != nil || !last.GetExited() {
		t.Fatalf("exit %+v %v", last, err)
	}

	if _, err := c.TerminalClose(ctx, ref); err != nil {
		t.Fatal(err)
	}
	if _, err := c.TerminalWrite(context.Background(), &deckv1.TerminalInput{TerminalId: ref.GetTerminalId(), Data: []byte("x")}); err == nil {
		t.Fatal("a closed terminal must be gone")
	}
}

func TestTerminalCloseEndsAShell(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Setenv("SHELL", "/bin/sh")
	}
	c := projectRig(t, t.TempDir(), nil)
	ctx := ctxT(t)
	ref, err := c.TerminalOpen(ctx, &deckv1.TerminalOpenRequest{Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	out, err := c.TerminalOutput(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.TerminalClose(ctx, ref); err != nil {
		t.Fatal(err)
	}
	// The stream ends (with or without an exit chunk) instead of hanging.
	for {
		ch, err := out.Recv()
		if err != nil || ch.GetExited() {
			return
		}
	}
}
