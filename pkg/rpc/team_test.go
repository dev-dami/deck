package rpc

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dev-dami/deck/internal/deck/peer"
	decks "github.com/dev-dami/deck/internal/deck/session"
	"github.com/dev-dami/deck/internal/workspace"
	"github.com/dev-dami/deck/pkg/remote/remotetest"
	"github.com/dev-dami/deck/pkg/rpc/deckv1"
)

// fakeCrew is the workspace parts the board, agent and team calls drive.
type fakeCrew struct {
	board   workspace.BoardReport
	did     []string
	shares  map[string]string
	link    *fakeLink
	posted  []string
	applied []string
}

func (f *fakeCrew) Board(string) workspace.BoardReport { return f.board }
func (f *fakeCrew) CancelTask(_, id string) error      { f.did = append(f.did, "cancel "+id); return nil }
func (f *fakeCrew) RetryTask(_, id string) error {
	return errors.New(id + " is not failed or blocked")
}
func (f *fakeCrew) SteerTask(_, id, text string) error {
	f.did = append(f.did, "steer "+id+" "+text)
	return nil
}
func (f *fakeCrew) SetOrchestrate(_ string, on bool) error {
	f.board.Orchestrate = on
	return nil
}
func (f *fakeCrew) ApplyTask(_, id string) ([]string, error) {
	f.applied = append(f.applied, id)
	return []string{"a.go", "b.go"}, nil
}
func (f *fakeCrew) DiscardTask(_, id string) error { f.did = append(f.did, "discard "+id); return nil }
func (f *fakeCrew) Roster(string) []workspace.AgentLine {
	return []workspace.AgentLine{
		{ID: "claude", Kind: "claude", Model: "opus", Status: "tool", Depth: 0},
		{ID: "codex-sub", Kind: "codex", Status: "working", Depth: 1, Parent: "claude", Branch: "deck/t2", Leases: "internal/** (exclusive)"},
	}
}
func (f *fakeCrew) Activity(string) workspace.ActivityReport {
	bash := workspace.RunningTool{AgentName: "claude", AgentKind: "claude", ToolID: "t1", Name: "Bash", Summary: "go test ./...", Started: time.Now()}
	return workspace.ActivityReport{Commands: []workspace.RunningTool{bash}, Tools: []workspace.RunningTool{bash}}
}
func (f *fakeCrew) PeerLink() workspace.PeerLink {
	if f.link == nil {
		return nil
	}
	return f.link
}
func (f *fakeCrew) ShareThread(_, name, role string) error {
	if role == "" {
		delete(f.shares, name)
	} else {
		f.shares[name] = role
	}
	return nil
}
func (f *fakeCrew) ThreadShares(string) map[string]string { return f.shares }
func (f *fakeCrew) ProposeContract(_, name, _ string) (string, bool, error) {
	return "t9", name == "away", nil
}
func (f *fakeCrew) ContractKinds() []string             { return []string{"claude", "codex"} }
func (f *fakeCrew) Capsules(string) []decks.CapsuleInfo { return nil }
func (f *fakeCrew) SetCapsulePublic(string, bool) error { return nil }
func (f *fakeCrew) SendTeamChat(_ context.Context, text string) ([]string, error) {
	f.posted = append(f.posted, text)
	return []string{"vps"}, nil
}

// fakeLink answers the peer link calls the team calls use; any other call
// would panic on the nil embedded interface.
type fakeLink struct {
	workspace.PeerLink
	answered []string
}

func (l *fakeLink) Snapshot() peer.Snapshot {
	return peer.Snapshot{
		Enabled: true,
		Name:    "laptop",
		Peers: []peer.PeerState{{
			Name: "vps", Role: "contractor", Pinned: true,
			Link: peer.Link{Inbound: 1, LastSeen: time.Now()},
			Presence: &peer.Presence{
				Agents:  []peer.AgentPresence{{Name: "claude2", Kind: "claude", Status: "working", Task: "fix the flaky test", ContextPct: 40}},
				Threads: []peer.SharedThread{{ID: "th1", Title: "Deploy", Role: "participant", Mode: "ask", Entries: 12}},
			},
		}},
		Chat:      []peer.ChatMessage{{Seq: 1, From: "vps", Agent: "claude2", Text: "on it", At: time.Now()}},
		Contracts: []peer.Contract{{ID: "c1", Peer: "vps", Inbound: true, Title: "Port the cache", State: peer.ContractProposed}},
	}
}

func (l *fakeLink) AnswerContract(name, id string, accept bool, agent string) error {
	l.answered = append(l.answered, name+" "+id+" "+agent)
	return nil
}

func (l *fakeLink) ReadShared(_ context.Context, name, thread string, after int, _ time.Duration) (peer.Mirror, error) {
	return peer.Mirror{Thread: peer.SharedThread{ID: thread, Title: "Deploy", Role: "participant"}, Lines: []string{"[claude2] building"}, Next: after + 1}, nil
}

func (l *fakeLink) PostShared(context.Context, string, string, string) error { return nil }

func (l *fakeLink) AnswerShared(context.Context, string, string, string, bool) error {
	return errors.New("only an editor answers approvals")
}

func crewRig(t *testing.T, f *fakeCrew) deckv1.DeckClient {
	t.Helper()
	srv := New(remotetest.New(), "secret").WithWorkspace(f)
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

func TestBoardAndTaskActions(t *testing.T) {
	f := &fakeCrew{shares: map[string]string{}, board: workspace.BoardReport{Tasks: []workspace.BoardTask{
		{ID: "t1", Title: "Race the fix", Status: "running", Race: true},
		{ID: "t2", Title: "entry", Status: "done", Entry: true, Owner: "claude-sub", Stats: "2 files +10 -3", Files: []string{"a.go"}},
	}}}
	c := crewRig(t, f)
	ctx := ctxT(t)
	b, err := c.Board(ctx, &deckv1.SessionRef{SessionId: "s"})
	if err != nil || len(b.GetTasks()) != 2 || !b.GetTasks()[0].GetRace() || b.GetTasks()[1].GetStats() != "2 files +10 -3" {
		t.Fatalf("board %+v %v", b, err)
	}
	r, err := c.TaskAction(ctx, &deckv1.TaskActionRequest{SessionId: "s", TaskId: "t2", Action: "apply"})
	if err != nil || len(r.GetFiles()) != 2 || f.applied[0] != "t2" {
		t.Fatalf("apply %+v %v", r, err)
	}
	for _, a := range []string{"cancel", "discard"} {
		if _, err := c.TaskAction(ctx, &deckv1.TaskActionRequest{SessionId: "s", TaskId: "t1", Action: a}); err != nil {
			t.Fatal(a, err)
		}
	}
	if _, err := c.TaskAction(ctx, &deckv1.TaskActionRequest{SessionId: "s", TaskId: "t1", Action: "steer", Text: "use the cache"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.TaskAction(ctx, &deckv1.TaskActionRequest{SessionId: "s", TaskId: "t1", Action: "steer"}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("steer without words: %v", err)
	}
	_, err = c.TaskAction(ctx, &deckv1.TaskActionRequest{SessionId: "s", TaskId: "t1", Action: "retry"})
	if status.Code(err) != codes.FailedPrecondition || status.Convert(err).Message() != "t1 is not failed or blocked" {
		t.Fatalf("retry refused: %v", err)
	}
	if _, err := c.TaskAction(ctx, &deckv1.TaskActionRequest{SessionId: "s", TaskId: "t1", Action: "explode"}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("unknown action: %v", err)
	}
	if got := f.did; len(got) != 3 || got[2] != "steer t1 use the cache" {
		t.Fatalf("did %v", got)
	}
	if _, err := c.SetOrchestrate(ctx, &deckv1.SetOrchestrateRequest{SessionId: "s", On: true}); err != nil || !f.board.Orchestrate {
		t.Fatalf("orchestrate %v", err)
	}
}

func TestAgentsTree(t *testing.T) {
	c := crewRig(t, &fakeCrew{})
	r, err := c.Agents(ctxT(t), &deckv1.SessionRef{SessionId: "s"})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.GetAgents()) != 2 || r.GetAgents()[1].GetParent() != "claude" || r.GetAgents()[1].GetDepth() != 1 {
		t.Fatalf("agents %+v", r.GetAgents())
	}
	if len(r.GetRunning()) != 1 || !r.GetRunning()[0].GetCommand() || r.GetRunning()[0].GetSummary() != "go test ./..." {
		t.Fatalf("running %+v", r.GetRunning())
	}
}

func TestTeam(t *testing.T) {
	c := crewRig(t, &fakeCrew{shares: map[string]string{}})
	r, err := c.Team(ctxT(t), &deckv1.Empty{})
	if err != nil || r.GetAvailable() {
		t.Fatalf("no link: %+v %v", r, err)
	}

	f := &fakeCrew{shares: map[string]string{}, link: &fakeLink{}}
	c = crewRig(t, f)
	ctx := ctxT(t)
	r, err = c.Team(ctx, &deckv1.Empty{})
	if err != nil {
		t.Fatal(err)
	}
	mate := r.GetTeammates()[0]
	if !r.GetAvailable() || mate.GetLink() != "online" || mate.GetAgents()[0].GetTask() != "fix the flaky test" || mate.GetThreads()[0].GetTitle() != "Deploy" {
		t.Fatalf("team %+v", r)
	}
	if len(r.GetChat()) != 1 || r.GetContracts()[0].GetState() != "proposed" || len(r.GetContractKinds()) != 2 {
		t.Fatalf("chat and contracts %+v", r)
	}
	if p, err := c.TeamPost(ctx, &deckv1.TeamPostRequest{Text: "shipping at 5"}); err != nil || p.GetReached()[0] != "vps" {
		t.Fatalf("post %+v %v", p, err)
	}
	if _, err := c.ShareThread(ctx, &deckv1.ShareThreadRequest{SessionId: "s", Peer: "vps", Role: "editor"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ShareThread(ctx, &deckv1.ShareThreadRequest{SessionId: "s", Peer: "vps", Role: "owner"}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("bad role: %v", err)
	}
	sh, _ := c.ThreadShares(ctx, &deckv1.SessionRef{SessionId: "s"})
	if sh.GetShares()["vps"] != "editor" {
		t.Fatalf("shares %+v", sh)
	}
	pc, err := c.ProposeContract(ctx, &deckv1.ProposeContractRequest{SessionId: "s", Peer: "away", Brief: "port the cache"})
	if err != nil || pc.GetTaskId() != "t9" || !pc.GetQueued() {
		t.Fatalf("propose %+v %v", pc, err)
	}
	if _, err := c.AnswerContract(ctx, &deckv1.AnswerContractRequest{Peer: "vps", Id: "c1", Accept: true, Agent: "codex"}); err != nil || f.link.answered[0] != "vps c1 codex" {
		t.Fatalf("answer %v %v", err, f.link.answered)
	}
	m, err := c.ReadShared(ctx, &deckv1.ReadSharedRequest{Peer: "vps", Thread: "th1", After: 4})
	if err != nil || m.GetNext() != 5 || m.GetLines()[0] != "[claude2] building" {
		t.Fatalf("mirror %+v %v", m, err)
	}
	if _, err := c.PostShared(ctx, &deckv1.PostSharedRequest{Peer: "vps", Thread: "th1", Text: "looks good"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.AnswerShared(ctx, &deckv1.AnswerSharedRequest{Peer: "vps", Thread: "th1", Approval: "a1", Allow: true}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("answer refused: %v", err)
	}
}

func TestCrewCallsWithoutAWorkspace(t *testing.T) {
	_, _, c, _ := rig(t)
	if _, err := c.Board(ctxT(t), &deckv1.SessionRef{SessionId: "s"}); status.Code(err) != codes.Unimplemented {
		t.Fatalf("board without a workspace: %v", err)
	}
}
