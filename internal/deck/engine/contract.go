package engine

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/dev-dami/deck/internal/deck/hub"
	"github.com/dev-dami/deck/internal/deck/peer"
	"github.com/dev-dami/deck/internal/deck/session"
	"github.com/dev-dami/deck/internal/deck/store"
	"github.com/dev-dami/deck/internal/deck/sub"
)

// fetchTimeout bounds fetching a contract's base or result from a teammate.
const fetchTimeout = 5 * time.Minute

// contracts joins code contracts on the peer link to the threads: the
// session sends its contracts through it, and it runs the ones this Deck's
// person accepts.
type contracts struct {
	mgr *session.Manager
	svc atomic.Pointer[peer.Service]
}

var _ session.Contractor = (*contracts)(nil)

func (c *contracts) hooks() peer.ContractHooks {
	return peer.ContractHooks{Run: c.run, Updated: c.updated}
}

// Propose implements session.Contractor.
func (c *contracts) Propose(ctx context.Context, name string, p session.Proposal) error {
	svc := c.svc.Load()
	if svc == nil {
		return fmt.Errorf("the peer link is not running")
	}
	_, err := svc.ProposeContract(ctx, name, peer.Contract{
		Title: p.Title, Brief: p.Brief, Base: p.Base, Repo: p.Repo, Files: p.Files, Thread: p.Thread, Task: p.Task,
	})
	return err
}

// run readies a contract this Deck's person accepted: it fetches the base
// from the teammate and makes the contract's thread.
func (c *contracts) run(k peer.Contract) (string, string, func(), error) {
	svc, s := c.svc.Load(), c.mgr.SubStore()
	if svc == nil || s == nil {
		return "", "", nil, fmt.Errorf("contracts cannot run without the peer link and the Deck store")
	}
	ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
	defer cancel()
	if _, err := svc.FetchCapsule(ctx, k.Peer, k.Base, s); err != nil {
		return "", "", nil, fmt.Errorf("could not fetch %s's tree: %w", k.Peer, err)
	}
	tid, task, err := c.mgr.RunContract(session.ContractRun{Peer: k.Peer, Title: k.Title, Brief: k.Brief, Base: k.Base, Kind: k.Agent})
	if err != nil {
		return "", "", nil, err
	}
	start := func() {
		if err := c.mgr.StartContract(tid, task); err != nil {
			slog.Warn("Contract not started", "peer", k.Peer, "error", err)
		}
	}
	return tid, task, start, nil
}

// updated puts a teammate's word on a contract this Deck proposed on its
// task, fetching the result capsule first.
func (c *contracts) updated(k peer.Contract) {
	go func() {
		n := session.ContractNews{State: k.State, Agent: k.Agent, Note: k.Note, Summary: k.Summary}
		if k.State == peer.ContractDone && k.Capsule != "" {
			err := fmt.Errorf("the peer link is not running")
			if svc, s := c.svc.Load(), c.mgr.SubStore(); svc != nil && s != nil {
				ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
				_, err = svc.FetchCapsule(ctx, k.Peer, k.Capsule, s)
				cancel()
			}
			if err != nil {
				n.State, n.Note, n.Summary = peer.ContractFailed, "its result could not be fetched: "+err.Error(), ""
			} else {
				n.Capsule = string(k.Capsule)
			}
		}
		if err := c.mgr.ContractNews(k.Thread, k.Task, n); err != nil {
			slog.Warn("Contract news not applied", "peer", k.Peer, "task", k.Task, "error", err)
		}
	}()
}

// taskEnd sends the result of a contract this Deck did, when its task ends:
// a capsule of what changed and the agent's last reply, or why it failed.
func (c *contracts) taskEnd(threadID string, tk store.Task) {
	svc := c.svc.Load()
	if svc == nil {
		return
	}
	var base sub.ID
	for _, k := range svc.Contracts() {
		if k.Inbound && k.State == peer.ContractAccepted && k.Thread == threadID && k.Task == tk.ID {
			base = k.Base
		}
	}
	if base == "" {
		return
	}
	go func() {
		done, summary := tk.Status == store.TaskDone, tk.Result
		var capsule sub.ID
		switch {
		case done:
			var err error
			if capsule, err = c.mgr.CaptureContract(threadID, base, tk); err != nil {
				done, summary = false, "its changes could not be captured: "+err.Error()
			}
		case tk.Status == store.TaskCancelled:
			summary = "its person cancelled it"
		default:
			summary = tk.Error
		}
		svc.FinishContract(threadID, tk.ID, done, capsule, hub.Redact(summary))
	}()
}
