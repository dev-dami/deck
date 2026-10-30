package model

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/textarea"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"

	"github.com/dev-dami/deck/internal/config"
	"github.com/dev-dami/deck/internal/session"
	"github.com/dev-dami/deck/internal/ui/common"
	"github.com/dev-dami/deck/internal/ui/completions"
	"github.com/dev-dami/deck/internal/ui/dialog"
	"github.com/dev-dami/deck/internal/ui/notice"
	"github.com/dev-dami/deck/internal/workspace"
)

// mentionWorkspace has a roster and profiles for the @ popup.
type mentionWorkspace struct {
	workspace.Workspace
	lines    []workspace.AgentLine
	profiles []workspace.Profile
}

func (w *mentionWorkspace) Config() *config.Config              { return nil }
func (w *mentionWorkspace) Roster(string) []workspace.AgentLine { return w.lines }
func (w *mentionWorkspace) ListProfiles() []workspace.Profile   { return w.profiles }
func (w *mentionWorkspace) SaveProfile(workspace.Profile) error { return nil }
func (w *mentionWorkspace) DeleteProfile(string, string) error  { return nil }
func (w *mentionWorkspace) AgentReadyErr() error                { return nil }
func (w *mentionWorkspace) WorkingDir() string                  { return "." }

func newMentionUI(w *mentionWorkspace) *UI {
	com := common.DefaultCommon(w)
	ta := textarea.New()
	ta.Focus()
	return &UI{
		com:      com,
		status:   NewStatus(com, nil),
		chat:     NewChat(com, config.ScrollbarDefault),
		textarea: ta,
		state:    uiChat,
		focus:    uiFocusEditor,
		width:    140,
		height:   45,
		session:  &session.Session{ID: "s1"},
		keyMap:   DefaultKeyMap(),
		dialog:   dialog.NewOverlay(),
		completions: completions.New(com.Styles.Completions.Normal,
			com.Styles.Completions.Focused, com.Styles.Completions.Match),
	}
}

// typeSigil types sigil into the composer as the editor does: the popup
// opens first, then the text goes in.
func typeSigil(t *testing.T, m *UI, sigil string) {
	t.Helper()
	require.True(t, completionSigil(sigil))
	m.openCompletions(sigil)
	m.textarea.InsertString(sigil)
	// The profiles are read off the UI loop; read them here.
	if cmd := m.loadProfiles(false); cmd != nil {
		m.applyProfiles(cmd().(profilesLoadedMsg))
	}
}

func popup(m *UI) string { return ansi.Strip(m.completions.Render()) }

func TestMentionSigils(t *testing.T) {
	t.Parallel()
	w := &mentionWorkspace{
		lines: []workspace.AgentLine{{ID: "claude", Kind: "claude", Status: "idle"}},
		profiles: []workspace.Profile{
			{Name: "charles", Kind: "claude", Role: "reviewer", Scope: "global"},
			{Name: "ada", Kind: "codex", Scope: "project"},
		},
	}

	// "@" and "#" open the files, as in Claude Code: no popup offers
	// agents; the session's lead is picked with ctrl+l.
	for _, sigil := range []string{"@", "#"} {
		m := newMentionUI(w)
		m.openCompletions(sigil)
		require.True(t, m.completionsOpen, sigil)
		require.True(t, m.completionsFiles, sigil)
		require.NotContains(t, popup(m), "charles", sigil)
	}
	// "$" opens nothing: "$name" is text.
	require.False(t, completionSigil("$"))
	require.False(t, completionSigil("!"))
	require.False(t, completionSigil("a"))
}

func TestPathLike(t *testing.T) {
	t.Parallel()
	for q, want := range map[string]bool{
		"":           false,
		"claude":     false,
		"charles-2":  false,
		"src/":       true,
		"main.go":    true,
		".github":    true,
		"~/notes.md": true,
	} {
		require.Equal(t, want, pathLike(q), q)
	}
}

func TestPeopleItems(t *testing.T) {
	t.Parallel()
	lines := []workspace.AgentLine{
		{ID: "charles", Kind: "claude", Status: "idle", Profile: "charles", Role: "reviewer"},
		{ID: "codex", Kind: "codex", Status: "working"},
		{ID: "ada", Kind: "codex", Status: "exited", Profile: "ada"},
	}
	profiles := []workspace.Profile{
		{Name: "Charles", Kind: "claude", Role: "reviewer"},
		{Name: "ada", Kind: "codex"},
	}
	var labels, inserts []string
	for _, it := range peopleItems("@", lines, profiles, []string{"alice"}) {
		labels = append(labels, ansi.Strip(it.Label))
		inserts = append(inserts, it.Value.Insert)
	}
	// charles is a member: listed once, as the member. ada exited: the
	// profile adds another. alice is a teammate.
	require.Equal(t, []string{"@charles", "@ada", "@alice"}, inserts)
	require.True(t, strings.HasSuffix(labels[0], "charles · reviewer · idle"), labels[0])
	require.True(t, strings.HasSuffix(labels[1], "ada · codex profile"), labels[1])
	require.Equal(t, "◆ alice · teammate", labels[2])

	// A member of a kind with several is listed by instanceItems instead.
	two := append(lines[:2:2], workspace.AgentLine{ID: "claude", Kind: "claude", Status: "idle"})
	require.Len(t, peopleItems("@", two, nil, nil), 0)
	var ins []string
	for _, it := range instanceItems("@", two) {
		ins = append(ins, it.Value.Insert)
		if it.Value.Insert == "@charles" {
			require.Contains(t, ansi.Strip(it.Label), "charles · reviewer · idle")
		}
	}
	require.Equal(t, []string{"@charles", "@claude"}, ins)
}

func TestProfilesCommand(t *testing.T) {
	t.Parallel()
	require.True(t, isProfilesSlash(" /profiles "))
	require.False(t, isProfilesSlash("/profiles please"))

	n := profilesNotice(nil)
	require.Equal(t, "No profiles", n.Title)
	require.Contains(t, n.Body, `"profiles"`)

	n = profilesNotice([]workspace.Profile{
		{Name: "charles", Kind: "claude", Role: "reviewer", Scope: "global"},
		{Name: "ada", Kind: "codex", Scope: "project"},
	})
	require.Equal(t, "Profiles (2)", n.Title)
	lines := strings.Split(n.Body, "\n")
	require.Len(t, lines, 2)
	require.True(t, strings.HasSuffix(lines[0], "charles · reviewer (claude, global)"), lines[0])
	require.True(t, strings.HasSuffix(lines[1], "ada (codex, project)"), lines[1])
	require.Equal(t, notice.ActionNotices, n.Action.ID)

	// /profiles reads them off the UI loop and lists them as a notice.
	m := newMentionUI(&mentionWorkspace{profiles: []workspace.Profile{{Name: "charles", Kind: "claude", Scope: "global"}}})
	cmd := m.sendMessage("/profiles")
	require.NotNil(t, cmd)
	msg, ok := cmd().(profilesLoadedMsg)
	require.True(t, ok)
	m.applyProfiles(msg)
	got := m.notices.store.List()
	require.NotEmpty(t, got)
	require.Equal(t, "Profiles (1)", got[0].Title)
}

func TestRosterShowsTheRole(t *testing.T) {
	t.Parallel()
	m := newBoardUI(&boardWorkspace{})
	out := ansi.Strip(m.modelInfoFor([]workspace.AgentLine{
		{ID: "charles", Kind: "claude", Status: "idle", Profile: "charles", Role: "reviewer"},
		{ID: "codex", Kind: "codex", Status: "idle"},
	}, 90))
	lines := strings.Split(out, "\n")
	require.Contains(t, lines[0], "charles · reviewer")
	require.NotContains(t, lines[2], "codex ·", "no role, no dot")
}
