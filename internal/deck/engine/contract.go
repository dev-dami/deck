package engine

import (
	"context"
	"encoding/json"
	"errors"
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

// fetchTimeout bounds one fetch of a contract's base or result from a
// teammate.
const fetchTimeout = 5 * time.Minute

// resultWaits are the pauses between tries to fetch a contract's result
// capsule: a teammate whose link dropped just after it reported has about
// an hour to come back.
var resultWaits = []time.Duration{10 * time.Second, time.Minute, 5 * time.Minute, 15 * time.Minute, 45 * time.Minute}

// contracts joins code contracts on the peer link to the threads: the
// session sends its contracts through it, and it runs the ones this Deck's
// person accepts.
type contracts struct {
	mgr *session.Manager
	svc atomic.Pointer[peer.Service]
	// waits are the pauses between tries to fetch a result (resultWaits),
	// and fetch is one try.
	waits []time.Duration
	fetch func(ctx context.Context, name string, id sub.ID) error
}

var _ session.Contractor = (*contracts)(nil)

func newContracts(mgr *session.Manager) *contracts {
	c := &contracts{mgr: mgr, waits: resultWaits}
	c.fetch = func(ctx context.Context, name string, id sub.ID) error {
		svc, s := c.svc.Load(), c.mgr.SubStore()
		if svc == nil || s == nil {
			return fmt.Errorf("the peer link is not running")
		}
		_, err := svc.FetchCapsule(ctx, name, id, s)
		return err
	}
	return c
}

func (c *contracts) hooks() peer.ContractHooks {
	return peer.ContractHooks{Run: c.run, Updated: c.updated, Cancel: c.cancel}
}

// Propose implements session.Contractor.
func (c *contracts) Propose(ctx context.Context, name string, p session.Proposal) (bool, error) {
	svc := c.svc.Load()
	if svc == nil {
		return false, fmt.Errorf("the peer link is not running")
	}
	_, err := svc.ProposeContract(ctx, name, peer.Contract{
		Title: p.Title, Brief: p.Brief, Base: p.Base, Repo: p.Repo, Files: p.Files, Thread: p.Thread, Task: p.Task,
	})
	if errors.As(err, new(peer.QueuedError)) {
		return true, nil
	}
	return false, err
}

// run readies a contract this Deck's person accepted: it fetches the base
// from the teammate and makes the contract's thread.
func (c *contracts) run(k peer.Contract) (string, string, func(), error) {
	if k.Kind == peer.KindOps {
		return c.runOps(k)
	}
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

// runOps readies an ops contract that is inside its requester's
// max_scope: its thread, its sandbox and its agent.
func (c *contracts) runOps(k peer.Contract) (string, string, func(), error) {
	svc := c.svc.Load()
	if svc == nil {
		return "", "", nil, fmt.Errorf("ops contracts cannot run without the peer link")
	}
	tid, task, err := c.mgr.RunOps(session.OpsRun{
		Peer: k.Peer, Title: k.Title, Brief: peer.OpsBrief(k), Scope: k.Scope, Targets: k.Targets,
		Check: func(raw json.RawMessage) (json.RawMessage, error) { return peer.CheckResult(k.Schema, raw) },
		Audit: func(event, detail string) { svc.Audit(k.Peer, k.ID, event, detail) },
	})
	if err != nil {
		return "", "", nil, err
	}
	start := func() {
		if err := c.mgr.StartContract(tid, task); err != nil {
			slog.Warn("Ops contract not started", "peer", k.Peer, "error", err)
		}
	}
	return tid, task, start, nil
}

// cancel stops the work on a contract its requester called off.
func (c *contracts) cancel(k peer.Contract) {
	go func() {
		if err := c.mgr.CancelContractRun(k.Thread, k.Task, k.Peer+" called off its contract"); err != nil {
			slog.Warn("Contract not stopped", "peer", k.Peer, "error", err)
		}
	}()
}

// updated puts a teammate's word on a contract this Deck proposed on its
// task, fetching the result capsule first. A fetch that fails is tried
// again after each of c.waits.
func (c *contracts) updated(k peer.Contract) {
	if k.Kind == peer.KindOps {
		return // no task waits for it: the Team pane shows it
	}
	go func() {
		n := session.ContractNews{State: k.State, Agent: k.Agent, Note: k.Note, Summary: k.Summary}
		if k.State == peer.ContractDone && k.Capsule != "" {
			if err := c.fetchResult(k); err != nil {
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

// fetchResult fetches the result capsule of contract k, trying again after
// each of c.waits.
func (c *contracts) fetchResult(k peer.Contract) error {
	for i := 0; ; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
		err := c.fetch(ctx, k.Peer, k.Capsule)
		cancel()
		if err == nil || i == len(c.waits) {
			return err
		}
		slog.Info("Contract result not fetched yet", "peer", k.Peer, "try", i+1, "error", err)
		time.Sleep(c.waits[i])
	}
}

// taskEnd follows the end of a task that a contract is about. On the
// contractor, it sends the result: a capsule of what changed and the
// agent's last reply, or why it failed. On the requester, a contract task
// ended here (cancelled by the person, or finished by an agent) calls the
// contract off.
func (c *contracts) taskEnd(threadID string, tk store.Task) {
	svc := c.svc.Load()
	if svc == nil {
		return
	}
	if tk.Remote != "" {
		svc.CancelContract(threadID, tk.ID, "its requester's task was "+string(tk.Status))
		return
	}
	var base sub.ID
	for _, k := range svc.Contracts() {
		if !k.Inbound || k.State != peer.ContractAccepted || k.Thread != threadID || k.Task != tk.ID {
			continue
		}
		if k.Kind == peer.KindOps {
			res, ok := c.mgr.OpsResult(threadID)
			why := ""
			switch {
			case tk.Status == store.TaskCancelled:
				why = "its person cancelled it"
			case tk.Status == store.TaskFailed:
				why = hub.Redact(tk.Error)
			case !ok:
				why = "its agent ended without a result that fits"
			}
			svc.FinishOps(threadID, tk.ID, res, why)
			return
		}
		base = k.Base
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
