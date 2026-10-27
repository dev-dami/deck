package rpc

import (
	"context"
	"errors"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dev-dami/deck/internal/deck/config"
	"github.com/dev-dami/deck/internal/deck/peer"
	"github.com/dev-dami/deck/internal/pubsub"
	"github.com/dev-dami/deck/internal/workspace"
	"github.com/dev-dami/deck/pkg/rpc/deckv1"
)

// sharedWait is how long ReadShared waits for news of a teammate's thread.
const sharedWait = 10 * time.Second

// crew is what the multi-agent and team calls reach: the parts of Deck's
// workspace the terminal UI drives from its Tasks, Agents and Team panes.
// Each is nil when the workspace lacks it, and its calls then say so.
type crew struct {
	orch      workspace.Orchestration
	trees     workspace.Worktrees
	roster    workspace.Roster
	activity  workspace.Activity
	peers     workspace.Peers
	sharing   workspace.Sharing
	contracts workspace.Contracts
	desk      workspace.TeamDesk
	rooms     workspace.Rooms
	settings  workspace.SettingsEditor
	profiles  workspace.Profiles
	limits    workspace.LimitsDesk
	work      workspace.WorkDesk
	usage     workspace.UsageReader
	changes   deckChanges
}

// deckChanges is the workspace's stream of what the message store cannot
// say, the one the remote adapter reads too.
type deckChanges interface {
	SubscribeDeck(ctx context.Context) <-chan pubsub.Event[workspace.DeckChange]
}

// WithWorkspace gives the server Deck's workspace for the task board, the
// agent tree and the team link. Parts the workspace lacks stay off.
func (s *Server) WithWorkspace(ws any) *Server {
	c := &crew{}
	c.orch, _ = ws.(workspace.Orchestration)
	c.trees, _ = ws.(workspace.Worktrees)
	c.roster, _ = ws.(workspace.Roster)
	c.activity, _ = ws.(workspace.Activity)
	c.peers, _ = ws.(workspace.Peers)
	c.sharing, _ = ws.(workspace.Sharing)
	c.contracts, _ = ws.(workspace.Contracts)
	c.desk, _ = ws.(workspace.TeamDesk)
	c.rooms, _ = ws.(workspace.Rooms)
	c.settings, _ = ws.(workspace.SettingsEditor)
	c.profiles, _ = ws.(workspace.Profiles)
	c.limits, _ = ws.(workspace.LimitsDesk)
	c.work, _ = ws.(workspace.WorkDesk)
	c.usage, _ = ws.(workspace.UsageReader)
	c.changes, _ = ws.(deckChanges)
	s.crew = c
	return s
}

var errNoCrew = status.Error(codes.Unimplemented, "this Deck does not serve that")

// refused keeps Deck's own words for a refused action.
func refused(err error) error {
	if err == nil {
		return nil
	}
	var q peer.QueuedError
	if errors.As(err, &q) {
		return nil
	}
	if s, ok := status.FromError(err); ok && s.Code() != codes.Unknown {
		return err
	}
	return status.Error(codes.FailedPrecondition, err.Error())
}

func (s *Server) Board(_ context.Context, r *deckv1.SessionRef) (*deckv1.BoardResponse, error) {
	if s.crew == nil || s.crew.orch == nil {
		return nil, errNoCrew
	}
	if err := needSession(r.GetSessionId()); err != nil {
		return nil, err
	}
	rep := s.crew.orch.Board(r.GetSessionId())
	return &deckv1.BoardResponse{
		Level:       rep.Level,
		Orchestrate: config.Levels[rep.Level].Schedule,
		Tasks: each(rep.Tasks, func(t workspace.BoardTask) *deckv1.BoardTask {
			return &deckv1.BoardTask{
				Id: t.ID, Title: t.Title, Status: t.Status, Owner: t.Owner, Deps: t.Deps, Brief: t.Brief,
				Result: t.Result, Error: t.Error, Attempts: int32(t.Attempts), Tries: int32(t.Tries), Branch: t.Branch,
				UpdatedAt: ms(t.Updated), Race: t.Race, Entry: t.Entry, Stats: t.Stats, Files: t.Files,
				Reviewer: t.Reviewer, Reviews: int32(t.Reviews),
			}
		}),
	}, nil
}

func (s *Server) TaskAction(_ context.Context, r *deckv1.TaskActionRequest) (*deckv1.TaskActionResponse, error) {
	if s.crew == nil || s.crew.orch == nil {
		return nil, errNoCrew
	}
	if err := needSession(r.GetSessionId()); err != nil {
		return nil, err
	}
	sid, tid := r.GetSessionId(), r.GetTaskId()
	if tid == "" {
		return nil, invalid("a task id is needed")
	}
	out := &deckv1.TaskActionResponse{}
	var err error
	switch r.GetAction() {
	case "cancel":
		err = s.crew.orch.CancelTask(sid, tid)
	case "retry":
		err = s.crew.orch.RetryTask(sid, tid)
	case "steer":
		if strings.TrimSpace(r.GetText()) == "" {
			return nil, invalid("steering needs words")
		}
		err = s.crew.orch.SteerTask(sid, tid, r.GetText())
	case "apply", "discard":
		if s.crew.trees == nil {
			return nil, errNoCrew
		}
		if r.GetAction() == "apply" {
			out.Files, err = s.crew.trees.ApplyTask(sid, tid)
		} else {
			err = s.crew.trees.DiscardTask(sid, tid)
		}
	default:
		return nil, invalid("unknown task action")
	}
	if err != nil {
		return nil, refused(err)
	}
	return out, nil
}

func (s *Server) SetOrchestrate(_ context.Context, r *deckv1.SetOrchestrateRequest) (*deckv1.Empty, error) {
	if s.crew == nil || s.crew.orch == nil {
		return nil, errNoCrew
	}
	if err := needSession(r.GetSessionId()); err != nil {
		return nil, err
	}
	level := r.GetLevel()
	switch {
	case level != "" && !config.ValidLevel(level):
		return nil, invalid("level is off, assist, auto or full")
	case level == "" && r.GetOn():
		level = config.LevelAuto
	case level == "":
		level = config.LevelOff
	}
	return &deckv1.Empty{}, refused(s.crew.orch.SetLevel(r.GetSessionId(), level))
}

func (s *Server) Agents(_ context.Context, r *deckv1.SessionRef) (*deckv1.AgentsResponse, error) {
	if s.crew == nil || s.crew.roster == nil {
		return nil, errNoCrew
	}
	if err := needSession(r.GetSessionId()); err != nil {
		return nil, err
	}
	out := &deckv1.AgentsResponse{
		Agents: each(s.crew.roster.Roster(r.GetSessionId()), func(a workspace.AgentLine) *deckv1.AgentNode {
			return &deckv1.AgentNode{
				Name: a.ID, Kind: a.Kind, Model: a.Model, Effort: a.Effort, Status: a.Status,
				ContextTokens: int32(a.ContextTokens), ContextWindow: int32(a.ContextWindow), CostUsd: a.CostUSD,
				Pending: int32(a.Pending), Since: ms(a.Since), Depth: int32(a.Depth), Parent: a.Parent,
				Branch: a.Branch, Leases: a.Leases, Profile: a.Profile, Role: a.Role, Color: a.Color,
			}
		}),
	}
	if s.crew.activity != nil {
		act := s.crew.activity.Activity(r.GetSessionId())
		sub, cmd := map[string]bool{}, map[string]bool{}
		for _, t := range act.Subagents {
			sub[t.ToolID] = true
		}
		for _, t := range act.Commands {
			cmd[t.ToolID] = true
		}
		out.Running = each(act.Tools, func(t workspace.RunningTool) *deckv1.RunningTool {
			summary := t.Summary
			if summary == "" {
				summary = t.Title
			}
			name := t.Display
			if name == "" {
				name = t.Name
			}
			return &deckv1.RunningTool{
				Agent: t.AgentName, Kind: t.AgentKind, Name: name, Summary: summary, Detail: t.Detail,
				Started: ms(t.Started), Subagent: sub[t.ToolID], Command: cmd[t.ToolID],
			}
		})
	}
	return out, nil
}

func (s *Server) link() workspace.PeerLink {
	if s.crew == nil || s.crew.peers == nil {
		return nil
	}
	return s.crew.peers.PeerLink()
}

// linkWord says whether a teammate can be reached now.
func linkWord(l peer.Link) string {
	switch {
	case l.Inbound > 0 || (l.Supervised && l.Status.State == peer.StateConnected):
		return "online"
	case l.Supervised && l.Status.State == peer.StateDialing:
		return "connecting"
	case l.Supervised || !l.LastSeen.IsZero():
		return "offline"
	}
	return ""
}

func toTeamThread(t peer.SharedThread) *deckv1.TeamThread {
	return &deckv1.TeamThread{Id: t.ID, Title: t.Title, Role: t.Role, Mode: t.Mode, Members: t.Members, Entries: int32(t.Entries)}
}

func (s *Server) Team(_ context.Context, _ *deckv1.Empty) (*deckv1.TeamResponse, error) {
	pl := s.link()
	if pl == nil {
		return &deckv1.TeamResponse{Available: false}, nil
	}
	snap := pl.Snapshot()
	out := &deckv1.TeamResponse{
		Available:   true,
		Enabled:     snap.Enabled,
		Name:        snap.Name,
		Fingerprint: snap.Fingerprint,
		Teammates: each(snap.Peers, func(p peer.PeerState) *deckv1.Teammate {
			t := &deckv1.Teammate{
				Name: p.Name, Role: p.Role, Pinned: p.Pinned, Link: linkWord(p.Link),
				LastSeen: ms(p.Link.LastSeen), Present: p.Presence != nil,
			}
			if pr := p.Presence; pr != nil {
				t.Agents = each(pr.Agents, func(a peer.AgentPresence) *deckv1.TeamAgent {
					return &deckv1.TeamAgent{Name: a.Name, Kind: a.Kind, Status: a.Status, Task: a.Task, ContextPct: int32(a.ContextPct)}
				})
				t.Threads = each(pr.Threads, toTeamThread)
			}
			return t
		}),
		Chat: each(snap.Chat, func(m peer.ChatMessage) *deckv1.TeamChat {
			return &deckv1.TeamChat{Seq: m.Seq, From: m.From, Agent: m.Agent, Text: m.Text, At: ms(m.At), Mine: m.Mine}
		}),
		Contracts: each(snap.Contracts, func(c peer.Contract) *deckv1.TeamContract {
			return &deckv1.TeamContract{
				Id: c.ID, Peer: c.Peer, Inbound: c.Inbound, Title: c.Title, Brief: c.Brief, State: c.State,
				Agent: c.Agent, Note: c.Note, Summary: c.Summary, Kind: c.Kind,
			}
		}),
	}
	if s.crew.contracts != nil {
		out.ContractKinds = s.crew.contracts.ContractKinds()
	}
	return out, nil
}

func (s *Server) TeamPost(ctx context.Context, r *deckv1.TeamPostRequest) (*deckv1.TeamPostResponse, error) {
	if s.crew == nil || s.crew.desk == nil {
		return nil, errNoCrew
	}
	if strings.TrimSpace(r.GetText()) == "" {
		return nil, invalid("an empty message")
	}
	reached, err := s.crew.desk.SendTeamChat(ctx, r.GetText())
	if err != nil {
		return nil, refused(err)
	}
	return &deckv1.TeamPostResponse{Reached: reached}, nil
}

func (s *Server) ShareThread(_ context.Context, r *deckv1.ShareThreadRequest) (*deckv1.Empty, error) {
	if s.crew == nil || s.crew.sharing == nil {
		return nil, errNoCrew
	}
	if err := needSession(r.GetSessionId()); err != nil {
		return nil, err
	}
	switch r.GetRole() {
	case "", "observer", "participant", "editor":
	default:
		return nil, invalid("the role is observer, participant or editor")
	}
	if r.GetPeer() == "" {
		return nil, invalid("a teammate is needed")
	}
	return &deckv1.Empty{}, refused(s.crew.sharing.ShareThread(r.GetSessionId(), r.GetPeer(), r.GetRole()))
}

func (s *Server) ThreadShares(_ context.Context, r *deckv1.SessionRef) (*deckv1.ThreadSharesResponse, error) {
	if s.crew == nil || s.crew.sharing == nil {
		return nil, errNoCrew
	}
	if err := needSession(r.GetSessionId()); err != nil {
		return nil, err
	}
	return &deckv1.ThreadSharesResponse{Shares: s.crew.sharing.ThreadShares(r.GetSessionId())}, nil
}

func (s *Server) ProposeContract(_ context.Context, r *deckv1.ProposeContractRequest) (*deckv1.ProposeContractResponse, error) {
	if s.crew == nil || s.crew.contracts == nil {
		return nil, errNoCrew
	}
	if err := needSession(r.GetSessionId()); err != nil {
		return nil, err
	}
	if r.GetPeer() == "" || strings.TrimSpace(r.GetBrief()) == "" {
		return nil, invalid("a teammate and a brief are needed")
	}
	id, queued, err := s.crew.contracts.ProposeContract(r.GetSessionId(), r.GetPeer(), r.GetBrief())
	if err != nil {
		return nil, refused(err)
	}
	return &deckv1.ProposeContractResponse{TaskId: id, Queued: queued}, nil
}

func (s *Server) AnswerContract(_ context.Context, r *deckv1.AnswerContractRequest) (*deckv1.Empty, error) {
	pl := s.link()
	if pl == nil {
		return nil, errNoCrew
	}
	if r.GetPeer() == "" || r.GetId() == "" {
		return nil, invalid("a teammate and a contract are needed")
	}
	return &deckv1.Empty{}, refused(pl.AnswerContract(r.GetPeer(), r.GetId(), r.GetAccept(), r.GetAgent()))
}

func (s *Server) ReadShared(ctx context.Context, r *deckv1.ReadSharedRequest) (*deckv1.SharedMirror, error) {
	pl := s.link()
	if pl == nil {
		return nil, errNoCrew
	}
	if r.GetPeer() == "" || r.GetThread() == "" {
		return nil, invalid("a teammate and a thread are needed")
	}
	m, err := pl.ReadShared(ctx, r.GetPeer(), r.GetThread(), int(r.GetAfter()), sharedWait)
	if err != nil {
		return nil, refused(err)
	}
	return &deckv1.SharedMirror{
		Thread: toTeamThread(m.Thread),
		Lines:  m.Lines,
		Next:   int32(m.Next),
		Pending: each(m.Pending, func(p peer.PendingApproval) *deckv1.SharedApproval {
			return &deckv1.SharedApproval{Id: p.ID, Agent: p.Agent, Kind: p.Kind, Summary: p.Summary}
		}),
	}, nil
}

func (s *Server) PostShared(ctx context.Context, r *deckv1.PostSharedRequest) (*deckv1.Empty, error) {
	pl := s.link()
	if pl == nil {
		return nil, errNoCrew
	}
	if r.GetPeer() == "" || r.GetThread() == "" || strings.TrimSpace(r.GetText()) == "" {
		return nil, invalid("a teammate, a thread and a message are needed")
	}
	return &deckv1.Empty{}, refused(pl.PostShared(ctx, r.GetPeer(), r.GetThread(), r.GetText()))
}

func (s *Server) AnswerShared(ctx context.Context, r *deckv1.AnswerSharedRequest) (*deckv1.Empty, error) {
	pl := s.link()
	if pl == nil {
		return nil, errNoCrew
	}
	if r.GetPeer() == "" || r.GetThread() == "" || r.GetApproval() == "" {
		return nil, invalid("a teammate, a thread and an approval are needed")
	}
	return &deckv1.Empty{}, refused(pl.AnswerShared(ctx, r.GetPeer(), r.GetThread(), r.GetApproval(), r.GetAllow()))
}
