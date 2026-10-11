package model

import (
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	deckagent "github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/ui/common"
	"github.com/dev-dami/deck/internal/ui/decklogo"
	"github.com/dev-dami/deck/internal/ui/styles"
	"github.com/dev-dami/deck/internal/workspace"
)

func TestMarkLevels(t *testing.T) {
	t.Parallel()
	lines := []workspace.AgentLine{
		{ID: "claude", Kind: "claude", ContextTokens: 10, ContextWindow: 100},
		{ID: "codex", Kind: "codex"},
		{ID: "codex2", Kind: "codex"},
		{ID: "opencode", Kind: "opencode", ContextTokens: 30, ContextWindow: 100},
		{ID: "oc2", Kind: "opencode", ContextTokens: 60, ContextWindow: 100},
		{ID: "antigravity", Kind: "antigravity"},
	}
	limits := map[string]workspace.LiveLimits{
		"claude": {Limits: deckagent.Limits{Windows: []deckagent.LimitWindow{
			{ID: "seven_day", UsedPct: 90},
			{ID: "five_hour", UsedPct: 40},
		}}},
		"codex": {Limits: deckagent.Limits{Windows: []deckagent.LimitWindow{
			{ID: "secondary", UsedPct: 70},
			{ID: "other", UsedPct: 20},
		}}},
		"opencode":    {Limits: deckagent.Limits{Unavailable: deckagent.LimitsUnsupported}},
		"antigravity": {Limits: deckagent.Limits{Unavailable: deckagent.LimitsUnsupported}},
	}
	want := []decklogo.Edge{
		{Kind: "claude", Pct: 40, Known: true},   // the session window, not the fullest
		{Kind: "codex", Pct: 70, Known: true},    // no primary: the fullest window
		{Kind: "opencode", Pct: 60, Known: true}, // no plan limits: the fullest context
		{Kind: "antigravity"},                    // nothing known
	}
	got := markLevels(lines, limits)
	if len(got) != len(want) {
		t.Fatalf("markLevels = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("edge %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	if got := markLevels(nil, limits); len(got) != 0 {
		t.Errorf("markLevels with no agents = %+v", got)
	}
}

func newMarkHeader(t *testing.T) *header {
	t.Helper()
	sty := styles.DeckDark()
	return newHeader(&common.Common{Styles: &sty})
}

func TestHeaderMarkFollowsEdges(t *testing.T) {
	t.Parallel()
	h := newMarkHeader(t)
	if got := ansi.Strip(h.mark(nil)); got != " "+decklogo.Stack+" deck " {
		t.Errorf("mark with no agents = %q", got)
	}
	edges := []decklogo.Edge{
		{Kind: "claude", Pct: 20, Known: true},
		{Kind: "codex", Pct: 60, Known: true},
		{Kind: "opencode", Pct: 100, Known: true},
	}
	if got := ansi.Strip(h.mark(edges)); got != " ▂▅█ deck " {
		t.Errorf("mark = %q, want %q", got, " ▂▅█ deck ")
	}
	// A level that moves is drawn again.
	edges[0].Pct = 100
	if got := ansi.Strip(h.mark(edges)); got != " █▅█ deck " {
		t.Errorf("mark after a change = %q", got)
	}
}

func TestLandingLogoHasAnEdgePerAgent(t *testing.T) {
	t.Setenv("DECK_REDUCED_MOTION", "1")
	h := newMarkHeader(t)
	edges := []decklogo.Edge{{Kind: "claude", Pct: 50, Known: true}, {Kind: "codex"}}
	out := ansi.Strip(h.landingLogo(uv.Rect(0, 0, 80, 7), edges))
	for _, mark := range []string{"✻", ">_"} {
		if !strings.Contains(out, mark) {
			t.Errorf("no %s under the edges:\n%s", mark, out)
		}
	}
	if strings.Contains(out, "▯") || strings.Contains(out, decklogo.Stack) {
		t.Errorf("more edges than agents:\n%s", out)
	}
}
