package hub

import (
	"context"
	"errors"
	"strings"
	"time"
)

// The team ops: this Deck's teammates, the other Decks it is paired with
// through the peer link (docs/peers-design.md).
const (
	OpPeersList   = "peers.list"
	OpPeersStatus = "peers.status"
	OpTeamRead    = "team.read"
	// OpTeamPost goes through the TaskBoard, which knows the calling agent.
	OpTeamPost = "team.post"
)

// Team is the peer link as agents see it: the teammates this Deck is paired
// with. The engine implements it over the peer service.
type Team interface {
	// List describes this Deck and every teammate: name, role and how the
	// link to it is doing. It reads local state only.
	List() string
	// Names are the teammates paired with this Deck, while its peer link
	// is on; it reads local state only.
	Names() []string
	// Status asks one teammate's Deck how it is, over the link.
	Status(ctx context.Context, name string) (string, error)
	// Read is the team chat after message since, oldest first.
	Read(since int) string
	// Post sends text from agent, an agent of this Deck, to every
	// teammate's team chat, or only to to: "vps", or "vps/codex" for one
	// agent of that Deck.
	Post(ctx context.Context, agent, to, text string) (string, error)
	// News is what teammates said in the team chat after message since,
	// for agent (an agent of this Deck): the messages to all its agents and
	// to agent, none older than after. last is the newest message number
	// seen; pass it as since next time. text is "" when there is none.
	News(agent string, since uint64, after time.Time) (text string, last uint64)
}

var errNoTeam = errors.New("this Deck has no peer link; pair a teammate in Settings › Team or with `deck peer pair`")

func opPeersList(_ context.Context, env *Env, _ args) (string, error) {
	if env.Team == nil {
		return "", errNoTeam
	}
	return env.Team.List(), nil
}

func opPeersStatus(ctx context.Context, env *Env, a args) (string, error) {
	if env.Team == nil {
		return "", errNoTeam
	}
	name := strings.TrimSpace(a.str("peer"))
	if name == "" {
		return "", errors.New("peer is required: a teammate's name from peers.list")
	}
	return env.Team.Status(ctx, name)
}

func opTeamRead(_ context.Context, env *Env, a args) (string, error) {
	if env.Team == nil {
		return "", errNoTeam
	}
	since, _ := a["since"].(int)
	return env.Team.Read(since), nil
}
