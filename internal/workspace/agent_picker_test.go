package workspace

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	decks "github.com/dev-dami/deck/internal/deck/session"
)

// pickerSession is newPickerWorkspace with session s1 on a thread of its own.
func pickerSession(t *testing.T) (*DeckWorkspace, string) {
	t.Helper()
	w := newPickerWorkspace(t, pickerCfg())
	tid, err := w.mgr().NewThread(t.TempDir())
	require.NoError(t, err)
	w.threads["s1"], w.back[tid] = tid, "s1"
	_, err = w.AgentModelChoices(context.Background(), "s1") // the catalog, for effort checks
	require.NoError(t, err)
	return w, tid
}

func ptr(s string) *string { return &s }

func TestThreadPicksDoNotWriteTheDefaults(t *testing.T) {
	saved := stubSaveDeckConfig(t)
	w, _ := pickerSession(t)
	before := w.DeckSettings()

	lead, err := w.SetLead("s1", "claude")
	require.NoError(t, err)
	require.True(t, lead.Changed)
	_, err = w.SetMemberSpec("s1", lead.Agent, ptr("opus"), ptr("max"))
	require.NoError(t, err)
	require.NoError(t, w.SetAgentModel("s1", "codex", "gpt-5-codex", "high"))

	require.Empty(t, *saved, "a pick for a session wrote the settings")
	require.Equal(t, before, w.DeckSettings())

	// A new session still starts on the defaults.
	require.Equal(t, "high", w.CachedAgentModelChoices("other")[0].CurrentEffort)
}

func TestCurrentSpecIsTheLeadsNotTheFirstOfItsKind(t *testing.T) {
	th := decks.ThreadInfo{Lead: "claude2", Members: []decks.AgentInfo{
		{ID: "claude", Kind: "claude", Model: "opus", Effort: "max"},
		{ID: "claude2", Kind: "claude", Model: "sonnet", Effort: "low"},
		{ID: "codex", Kind: "codex", Model: "gpt-5-codex", Effort: "high", Status: decks.StatusExited},
	}}
	cfg := pickerCfg()
	model, effort := currentSpec(th, true, cfg, "claude")
	require.Equal(t, "sonnet", model)
	require.Equal(t, "low", effort)
	// No live codex to hand over to: a new one would run the defaults.
	model, effort = currentSpec(th, true, cfg, "codex")
	require.Equal(t, "gpt-5", model)
	require.Equal(t, "low", effort)
}

func TestSetMemberSpecTargetsTheMemberNotTheKind(t *testing.T) {
	stubSaveDeckConfig(t)
	w, tid := pickerSession(t)
	first, err := w.mgr().AddMember(tid, "claude", "")
	require.NoError(t, err)
	second, err := w.mgr().AddMember(tid, "claude", "")
	require.NoError(t, err)
	name := func(id string) string { a, _ := w.mgr().Agent(id); return a.Name }

	_, err = w.SetMemberSpec("s1", name(second), ptr("sonnet"), ptr("low"))
	require.NoError(t, err)
	a, _ := w.mgr().Agent(first)
	require.Equal(t, "", a.Model, "the other claude was changed")
	require.Equal(t, "high", a.Effort)
	th, _ := w.mgr().Thread(tid)
	require.Equal(t, first, th.Lead, "the lead moved")
}

func TestSetMemberSpecRefusesAnEffortTheModelLacks(t *testing.T) {
	stubSaveDeckConfig(t)
	w, _ := pickerSession(t)
	lead, err := w.SetLead("s1", "claude")
	require.NoError(t, err)
	_, err = w.SetMemberSpec("s1", lead.Agent, ptr("opus"), ptr("turbo"))
	require.ErrorContains(t, err, "turbo isn't an effort")
}

func TestSetDefaultsWritesTheSettings(t *testing.T) {
	saved := stubSaveDeckConfig(t)
	w, _ := pickerSession(t)

	require.Error(t, w.SetDefaults("claude", ptr("opus"), ptr("turbo")))
	require.Empty(t, *saved, "an invalid effort is not saved")

	require.NoError(t, w.SetDefaults("claude", ptr("opus"), ptr("max")))
	require.Len(t, *saved, 1)
	got := w.DeckSettings()
	require.Equal(t, "opus", got.Models.For("claude"))
	require.Equal(t, "max", got.Efforts.For("claude"))

	// Default is a value like any other; a nil effort keeps the kind's.
	require.NoError(t, w.SetDefaults("claude", ptr(""), nil))
	require.Equal(t, "", w.DeckSettings().Models.For("claude"))
	require.Equal(t, "max", w.DeckSettings().Efforts.For("claude"))
	require.Error(t, w.SetDefaults("gemini", nil, nil))
}

func TestSetAgentModelNeedsASession(t *testing.T) {
	saved := stubSaveDeckConfig(t)
	w, _ := pickerSession(t)
	require.Error(t, w.SetAgentModel("", "claude", "opus", ""))
	require.Empty(t, *saved)
}

func TestAPickOfAnotherAgentAloneKeepsItsOwnSpec(t *testing.T) {
	stubSaveDeckConfig(t)
	w, tid := pickerSession(t)
	require.NoError(t, w.SetAgentModel("s1", "claude", "opus", "max"))
	// Handing over to codex with no model or effort: codex runs its defaults.
	require.NoError(t, w.SetAgentModel("s1", "codex", "", ""))
	th, _ := w.mgr().Thread(tid)
	var lead decks.AgentInfo
	for _, a := range th.Members {
		if a.ID == th.Lead {
			lead = a
		}
	}
	require.Equal(t, "codex", lead.Kind)
	require.Equal(t, "gpt-5", lead.Model)
	require.Equal(t, "low", lead.Effort)
}
