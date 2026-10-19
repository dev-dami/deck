package peer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/dev-dami/deck/internal/deck/config"
	"github.com/dev-dami/deck/internal/deck/sub"
)

// Code contracts (docs/multiplayer-design.md §8.4): one Deck asks a
// teammate's Deck to do a coding task. The requester proposes it
// (contract.propose) with a snapshot of its tree as the base. The
// teammate's person confirms it on a card and picks the agent. The work
// runs on the teammate's machine, in a folder of its own, under its
// person's login and plan. What comes back is a capsule and a short summary
// (contract.status, contract.result), never the transcript.
//
// Each side shares only what the contract needs, and only with the other
// Deck: the requester the base snapshot while the contract is open, the
// contractor the result capsule. Nothing is published to other teammates.

const (
	OpContractPropose = "contract.propose"
	OpContractStatus  = "contract.status"
	OpContractResult  = "contract.result"
)

// The states of a contract.
const (
	ContractProposed = "proposed" // waiting for the contractor's person
	ContractAccepted = "accepted" // confirmed: an agent works on it
	ContractDeclined = "declined"
	ContractDone     = "done"
	ContractFailed   = "failed"
)

const (
	// maxContracts caps the records kept; the oldest final ones go first.
	maxContracts = 100
	// maxProposed caps the proposals one teammate may have waiting here.
	maxProposed = 5
	// contractTTL drops a final record this old.
	contractTTL = 30 * 24 * time.Hour
	// The caps on a contract's text, in bytes.
	maxContractTitle = 120
	maxContractBrief = 4000
	maxContractText  = 4000
	// contractTimeout bounds proposing a contract to a teammate.
	contractTimeout = 30 * time.Second
)

var contractOps = []OpInfo{
	{Name: OpContractPropose, Args: "{id*, title*, brief, base*, repo, files}", Desc: "propose a coding contract to this Deck: its person confirms it, an agent does it on a copy of the base snapshot, and a capsule comes back"},
	{Name: OpContractStatus, Args: "{id*, state*, agent, note}", Desc: "how a contract this Deck proposed goes: accepted or declined"},
	{Name: OpContractResult, Args: "{id*, state*, capsule, summary}", Desc: "the result of a contract this Deck proposed: done or failed, the capsule and a summary"},
}

// Contract is one contract as either Deck records it.
type Contract struct {
	ID string `json:"id"`
	// Peer is the other Deck; Inbound is set when it proposed the contract
	// to this one.
	Peer    string `json:"peer"`
	Inbound bool   `json:"inbound,omitempty"`
	Title   string `json:"title"`
	Brief   string `json:"brief,omitempty"`
	// Base is the snapshot the work starts from; Repo the root commit of the
	// requester's repository, Files how many files the base has.
	Base  sub.ID `json:"base"`
	Repo  string `json:"repo,omitempty"`
	Files int    `json:"files,omitempty"`
	State string `json:"state"`
	// Agent is the kind of agent the contractor's person picked.
	Agent string `json:"agent,omitempty"`
	// Note says why a contract was declined or failed.
	Note string `json:"note,omitempty"`
	// Capsule and Summary are the result: what the work changed, and the
	// agent's last report.
	Capsule sub.ID `json:"capsule,omitempty"`
	Summary string `json:"summary,omitempty"`
	// Thread and Task are where the contract lives on this Deck: the task
	// on the requester's board, or the thread and task the contractor runs.
	Thread  string    `json:"thread,omitempty"`
	Task    string    `json:"task,omitempty"`
	At      time.Time `json:"at"`
	Updated time.Time `json:"updated"`
}

// Final reports whether c can no longer change.
func (c Contract) Final() bool {
	return c.State == ContractDeclined || c.State == ContractDone || c.State == ContractFailed
}

// ContractHooks connect contracts to the engine's threads.
type ContractHooks struct {
	// Run readies an inbound contract the person accepted, with an agent of
	// kind c.Agent, and returns the thread and task it runs as, and start,
	// which starts the work once the contract records them.
	Run func(c Contract) (thread, task string, start func(), err error)
	// Updated hears every change a teammate made to a contract this Deck
	// proposed. It is called from the link's goroutine and must not block.
	Updated func(c Contract)
}

// errNoContract is the error for a contract id this Deck does not have.
var errNoContract = errors.New("no contract")

// contractBook holds the contracts, in contracts.json in the peers
// directory.
type contractBook struct {
	path string
	mu   sync.Mutex
	list []Contract
	// grants is what each peer may fetch for the open contracts, by peer;
	// nil until next needed.
	grants map[string]map[sub.ID]bool
}

func newContractBook(dir string) *contractBook {
	b := &contractBook{}
	if dir != "" {
		b.path = filepath.Join(dir, "contracts.json")
		if raw, err := os.ReadFile(b.path); err == nil {
			_ = json.Unmarshal(raw, &b.list)
		}
	}
	return b
}

// saveLocked trims and writes the book; b.mu is held.
func (b *contractBook) saveLocked() {
	b.grants = nil
	now := time.Now()
	kept := b.list[:0]
	for _, c := range b.list {
		if !c.Final() || now.Sub(c.Updated) < contractTTL {
			kept = append(kept, c)
		}
	}
	for len(kept) > maxContracts {
		i := 0
		for i < len(kept)-1 && !kept[i].Final() {
			i++
		}
		kept = append(kept[:i], kept[i+1:]...)
	}
	b.list = kept
	if b.path == "" {
		return
	}
	if raw, err := json.Marshal(b.list); err == nil {
		_ = writePrivateFile(b.path, raw, false)
	}
}

// findLocked is the index of the contract id with peer, or -1; b.mu is held.
func (b *contractBook) findLocked(peer, id string, inbound bool) int {
	for i, c := range b.list {
		if c.ID == id && c.Peer == peer && c.Inbound == inbound {
			return i
		}
	}
	return -1
}

func (b *contractBook) all() []Contract {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]Contract(nil), b.list...)
}

// update changes the contract with fn under the lock and saves it. When fn
// fails, the contract stays as it was.
func (b *contractBook) update(peer, id string, inbound bool, fn func(c *Contract) error) (Contract, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	i := b.findLocked(peer, id, inbound)
	if i < 0 {
		return Contract{}, fmt.Errorf("%w %s with %s", errNoContract, id, peer)
	}
	if err := fn(&b.list[i]); err != nil {
		return b.list[i], err
	}
	b.list[i].Updated = time.Now()
	c := b.list[i]
	b.saveLocked()
	return c, nil
}

// granted reports whether peer may fetch object id for a contract: the base
// of an open contract this Deck proposed to it, or the capsule of one it
// did for it.
func (b *contractBook) granted(store *sub.Store, peer string, id sub.ID) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.grants == nil {
		b.grants = map[string]map[sub.ID]bool{}
		for _, c := range b.list {
			root := c.Capsule
			if !c.Inbound {
				root = ""
				if !c.Final() {
					root = c.Base
				}
			}
			if root == "" || store == nil {
				continue
			}
			ids, err := store.Closure(root)
			if err != nil {
				continue
			}
			g := b.grants[c.Peer]
			if g == nil {
				g = map[sub.ID]bool{}
				b.grants[c.Peer] = g
			}
			for _, i := range ids {
				g[i] = true
			}
		}
	}
	return b.grants[peer][id]
}

// cleanLine is s with control characters gone, on one line, capped at n
// bytes.
func cleanLine(s string, n int) string {
	return strings.Join(strings.Fields(cleanText(s, n)), " ")
}

// cleanText is s with control and format characters gone (newlines and tabs
// stay), capped at n bytes.
func cleanText(s string, n int) string {
	s = cleanChat(s)
	if len(s) > n {
		s = strings.ToValidUTF8(s[:n], "") + "…"
	}
	return s
}

// validKey reports whether s can be a contract id: 1 to 64 lower-case
// letters and digits.
func validKey(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

// ContractHandler is Base plus the contract ops.
type ContractHandler struct {
	Base Handler
	S    *Service
}

// Search implements Handler.
func (h *ContractHandler) Search(ctx context.Context, from Caller, query string) ([]OpInfo, error) {
	ops, err := h.Base.Search(ctx, from, query)
	if err != nil {
		return nil, err
	}
	return append(ops, matchOps(contractOps, query)...), nil
}

// Execute implements Handler.
func (h *ContractHandler) Execute(ctx context.Context, from Caller, op string, args json.RawMessage) (string, error) {
	switch op {
	case OpContractPropose:
		return h.S.proposed(from, args)
	case OpContractStatus, OpContractResult:
		return h.S.reported(from, op, args)
	}
	return h.Base.Execute(ctx, from, op, args)
}

// proposed records a contract a teammate proposed, for this Deck's person
// to confirm or decline.
func (s *Service) proposed(from Caller, args json.RawMessage) (string, error) {
	var a struct {
		ID    string `json:"id"`
		Title string `json:"title"`
		Brief string `json:"brief"`
		Base  sub.ID `json:"base"`
		Repo  string `json:"repo"`
		Files int    `json:"files"`
	}
	if json.Unmarshal(args, &a) != nil || !validKey(a.ID) || !a.Base.Valid() {
		return "", errors.New("contract.propose needs an id and a base snapshot")
	}
	c := Contract{
		ID: a.ID, Peer: from.Name, Inbound: true, Title: cleanLine(a.Title, maxContractTitle), Brief: cleanText(a.Brief, maxContractBrief),
		Base: a.Base, Repo: cleanLine(a.Repo, 64), Files: max(a.Files, 0), State: ContractProposed, At: time.Now(), Updated: time.Now(),
	}
	if c.Title == "" {
		return "", errors.New("contract.propose needs a title")
	}
	b := s.contracts
	b.mu.Lock()
	if b.findLocked(c.Peer, c.ID, true) >= 0 {
		b.mu.Unlock()
		return "proposed", nil // a repeat
	}
	n := 0
	for _, o := range b.list {
		if o.Inbound && o.Peer == c.Peer && o.State == ContractProposed {
			n++
		}
	}
	if n >= maxProposed {
		b.mu.Unlock()
		return "", fmt.Errorf("%d contracts from you already wait for this Deck's person", n)
	}
	b.list = append(b.list, c)
	b.saveLocked()
	b.mu.Unlock()
	s.changed()
	return "proposed: it waits for this Deck's person to confirm it", nil
}

// reported takes a teammate's word on a contract this Deck proposed to it.
func (s *Service) reported(from Caller, op string, args json.RawMessage) (string, error) {
	var a struct {
		ID      string `json:"id"`
		State   string `json:"state"`
		Agent   string `json:"agent"`
		Note    string `json:"note"`
		Capsule sub.ID `json:"capsule"`
		Summary string `json:"summary"`
	}
	if json.Unmarshal(args, &a) != nil || !validKey(a.ID) {
		return "", fmt.Errorf("%w: no such contract", ErrUnknownOp)
	}
	allowed := map[string]bool{ContractAccepted: true, ContractDeclined: true}
	if op == OpContractResult {
		allowed = map[string]bool{ContractDone: true, ContractFailed: true}
	}
	if !allowed[a.State] || a.Capsule != "" && (!a.Capsule.Valid() || a.State != ContractDone) {
		return "", fmt.Errorf("%s: bad state %q", op, a.State)
	}
	same := false
	c, err := s.contracts.update(from.Name, a.ID, false, func(c *Contract) error {
		switch {
		case c.State == a.State:
			same = true // a repeat
			return nil
		case c.Final() || c.State == ContractAccepted && op == OpContractStatus:
			return fmt.Errorf("contract %s is %s", c.ID, c.State)
		}
		c.State, c.Note = a.State, cleanLine(a.Note, 300)
		if config.ValidPeerName(a.Agent) {
			c.Agent = a.Agent
		}
		if op == OpContractResult {
			c.Capsule, c.Summary = a.Capsule, cleanText(a.Summary, maxContractText)
		}
		return nil
	})
	if errors.Is(err, errNoContract) {
		return "", fmt.Errorf("%w: no such contract", ErrUnknownOp)
	} else if err != nil {
		return "", err
	}
	if !same {
		s.changed()
		if s.hooks.Updated != nil {
			s.hooks.Updated(c)
		}
	}
	return "noted", nil
}

// Contracts lists the contracts both ways, oldest first.
func (s *Service) Contracts() []Contract { return s.contracts.all() }

// granted reports whether peer may fetch object id for a contract.
func (s *Service) granted(peer string, id sub.ID) bool {
	var store *sub.Store
	if s.subStore != nil {
		store = s.subStore()
	}
	return s.contracts.granted(store, peer, id)
}

// ProposeContract asks the pinned peer to do c (its Title, Brief, Base,
// Repo, Files, and where it lives here: Thread, Task). The base is shared
// with that peer alone until the contract ends. It returns the contract as
// recorded, or why the peer could not be asked or refused.
func (s *Service) ProposeContract(ctx context.Context, peer string, c Contract) (Contract, error) {
	if _, ok, err := OpenStore(s.dir).Get(peer); err != nil || !ok {
		return Contract{}, fmt.Errorf("no teammate %q is paired", peer)
	}
	c.ID, c.Peer, c.Inbound, c.State = newCallKey(), peer, false, ContractProposed
	c.Title, c.Brief = cleanLine(c.Title, maxContractTitle), cleanText(c.Brief, maxContractBrief)
	c.At, c.Updated = time.Now(), time.Now()
	b := s.contracts
	b.mu.Lock()
	b.list = append(b.list, c)
	b.saveLocked()
	b.mu.Unlock()
	args := map[string]any{"id": c.ID, "title": c.Title, "brief": c.Brief, "base": c.Base, "repo": c.Repo, "files": c.Files}
	cctx, cancel := context.WithTimeout(ctx, contractTimeout)
	defer cancel()
	err := s.withPeer(cctx, peer, func(cl *Client) error { return execOK(cctx, cl, OpContractPropose, args) })
	if err != nil {
		b.mu.Lock()
		if i := b.findLocked(peer, c.ID, false); i >= 0 {
			b.list = append(b.list[:i], b.list[i+1:]...)
			b.saveLocked()
		}
		b.mu.Unlock()
		return Contract{}, fmt.Errorf("%s did not take the contract: %w", peer, err)
	}
	s.changed()
	return c, nil
}

// AnswerContract is this Deck's person accepting a teammate's contract,
// with an agent of kind agent, or declining it. The teammate hears of it.
func (s *Service) AnswerContract(peer, id string, accept bool, agent string) error {
	s.answerMu.Lock() // one answer at a time: a contract never starts twice
	defer s.answerMu.Unlock()
	c, err := s.contracts.update(peer, id, true, func(c *Contract) error {
		if c.State != ContractProposed {
			return fmt.Errorf("contract %s is %s", c.ID, c.State)
		}
		return nil
	})
	if err != nil {
		return err
	}
	args := map[string]any{"id": c.ID, "state": ContractDeclined, "note": "declined by its person"}
	if accept {
		if s.hooks.Run == nil {
			return errors.New("contracts cannot run on this Deck")
		}
		c.Agent = agent
		thread, task, start, err := s.hooks.Run(c)
		if err != nil {
			return err
		}
		c, err = s.contracts.update(peer, id, true, func(c *Contract) error {
			c.State, c.Agent, c.Thread, c.Task = ContractAccepted, agent, thread, task
			return nil
		})
		if err != nil {
			return err
		}
		if start != nil {
			start()
		}
		args = map[string]any{"id": c.ID, "state": ContractAccepted, "agent": agent}
	} else if _, err := s.contracts.update(peer, id, true, func(c *Contract) error {
		c.State, c.Note = ContractDeclined, "declined by its person"
		return nil
	}); err != nil {
		return err
	}
	s.changed()
	s.queue(peer, OpContractStatus, c.ID+"s", args)
	return nil
}

// FinishContract reports the inbound contract that runs as task on thread
// to its requester: done with capsule (shared with the requester alone;
// "" when nothing changed) and summary, or failed with summary as why. It
// reports whether that task was a contract's.
func (s *Service) FinishContract(thread, task string, done bool, capsule sub.ID, summary string) bool {
	b := s.contracts
	b.mu.Lock()
	i := -1
	for j, c := range b.list {
		if c.Inbound && c.State == ContractAccepted && c.Thread == thread && c.Task == task {
			i = j
		}
	}
	if i < 0 {
		b.mu.Unlock()
		return false
	}
	c := &b.list[i]
	c.State, c.Summary, c.Updated = ContractFailed, cleanText(summary, maxContractText), time.Now()
	if done {
		c.State, c.Capsule = ContractDone, capsule
	} else {
		c.Note = cleanLine(summary, 300)
	}
	out := *c
	b.saveLocked()
	b.mu.Unlock()
	s.changed()
	args := map[string]any{"id": out.ID, "state": out.State, "summary": out.Summary}
	if out.Capsule != "" {
		args["capsule"] = out.Capsule
	}
	s.queue(out.Peer, OpContractResult, out.ID+"r", args)
	return true
}
