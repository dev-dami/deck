package peer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dev-dami/deck/internal/deck/config"
)

// OpPeerStatus is the one op phase 1 answers: this Deck's name, version and
// uptime.
const OpPeerStatus = "peer.status"

// ErrUnknownOp is returned by a Handler for an op it does not know.
var ErrUnknownOp = errors.New("unknown op")

// DefaultAllowInbound is what a peer may run when its peers.remote row has
// no allow_inbound, or when it has no row at all. It grows with the ops of
// docs/peers-design.md section 2 as they land; an op is never reachable just
// because a handler happens to know it.
//
// The SUB ops are in it: they only ever serve capsules this Deck's user
// published (sub.capsule.publish), which is the consent to share them.
//
// The contract ops are in it too: contract.propose also needs role
// contractor; contract.status and contract.result only touch contracts this
// Deck proposed to the caller, and contract.cancel only those the caller
// proposed here. The thread ops only touch threads shared with the caller,
// in the role it was given.
var DefaultAllowInbound = []string{OpPeerStatus, OpSubClosure, OpSubObjects, OpConsolePost, OpContractPropose, OpContractStatus, OpContractResult, OpContractCancel,
	OpThreadMirror, OpThreadPost, OpThreadAnswer}

// contractorOps are the ops only a peer with role contractor may run, on top
// of allow_inbound.
var contractorOps = map[string]bool{OpContractPropose: true}

// Caller is the pinned peer on the other end of a connection, as this Deck
// knows it. Name and Fingerprint come from the pinned store, never from the
// peer, so a handler can trust them.
type Caller struct {
	Name        string
	Fingerprint string
	Role        string // "observer" when empty
	// AllowInbound is the ops this peer may run here (config
	// peers.remote[].allow_inbound); empty means DefaultAllowInbound.
	AllowInbound []string
}

// Allowed reports whether the caller may run op. The match is exact: no case
// folding, trimming or Unicode normalization, so "Peer.Status" or a
// look-alike never matches peer.status. Contractor-only ops also need role
// contractor.
func (c Caller) Allowed(op string) bool {
	if op == "" {
		return false
	}
	if contractorOps[op] && c.Role != config.PeerRoleContractor {
		return false
	}
	list := c.AllowInbound
	if len(list) == 0 {
		list = DefaultAllowInbound
	}
	for _, a := range list {
		if a == op {
			return true
		}
	}
	return false
}

// OpInfo is one op as the search tool lists it.
type OpInfo struct {
	Name string // e.g. "peer.status"
	Args string // the arguments, e.g. "{}"
	Desc string
}

// Handler answers the two tools a peer can call. The transport checks the
// caller against its allow list before Execute and drops the ops it may not
// run from Search's answer, so a handler never has to.
type Handler interface {
	// Search returns the ops matching query ("" means all of them).
	Search(ctx context.Context, from Caller, query string) ([]OpInfo, error)
	// Execute runs op with args for the execute tool and returns the text
	// the peer sees. An error's text stays on this Deck: the peer gets a
	// fixed message (ErrUnknownOp has its own).
	Execute(ctx context.Context, from Caller, op string, args json.RawMessage) (string, error)
}

// StatusHandler is the phase 1 handler: execute answers peer.status and
// nothing else, and search lists that op.
type StatusHandler struct {
	Name string
	// NameFunc, when set, gives the name at each call instead of Name, for a
	// Deck renamed while it runs.
	NameFunc func() string
	Version  string
	Started  time.Time
	Now      func() time.Time // nil means time.Now
	// Presence, when set, adds what this Deck's agents do (presence.go);
	// repos are the root commits of the repositories the caller has.
	Presence func(peer string, repos []string) Presence
}

var statusOps = []OpInfo{{Name: OpPeerStatus, Args: "{repos}", Desc: "this Deck's name, version and uptime, its agents and what they do, and its file leases in the repositories (root commits) given"}}

// Search implements Handler.
func (h *StatusHandler) Search(_ context.Context, _ Caller, query string) ([]OpInfo, error) {
	return matchOps(statusOps, query), nil
}

// matchOps keeps the ops whose name or description contains every word of
// query, ignoring case.
func matchOps(ops []OpInfo, query string) []OpInfo {
	words := strings.Fields(strings.ToLower(query))
	var out []OpInfo
	for _, op := range ops {
		text := strings.ToLower(op.Name + " " + op.Desc)
		ok := true
		for _, w := range words {
			if !strings.Contains(text, w) {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, op)
		}
	}
	return out
}

// Execute implements Handler.
func (h *StatusHandler) Execute(_ context.Context, from Caller, op string, args json.RawMessage) (string, error) {
	if op != OpPeerStatus {
		return "", fmt.Errorf("%w %q (call search to list ops)", ErrUnknownOp, op)
	}
	now := time.Now
	if h.Now != nil {
		now = h.Now
	}
	up := now().Sub(h.Started).Truncate(time.Second)
	name := h.Name
	if h.NameFunc != nil {
		name = h.NameFunc()
	}
	out := map[string]any{
		"name":           name,
		"version":        h.Version,
		"uptime":         up.String(),
		"uptime_seconds": int64(up / time.Second),
	}
	if h.Presence != nil {
		var a struct {
			Repos []string `json:"repos"`
		}
		_ = json.Unmarshal(args, &a)
		out["presence"] = h.Presence(from.Name, a.Repos[:min(len(a.Repos), maxRepos)])
	}
	b, err := json.Marshal(out)
	return string(b), err
}
