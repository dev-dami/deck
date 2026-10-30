package session

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/dev-dami/deck/internal/deck/profile"
)

func TestProfileMentionAddsANamedMember(t *testing.T) {
	r := newRig(t, nil, nil)
	charles := profile.Profile{
		Name: "charles", Kind: "claude", Model: "opus-x", Effort: "high", Role: "reviewer",
		Instructions: "Check the tests before the code.", Voice: "dry, terse", Paths: []string{"internal/auth/**"},
		Color: "#c6a0f6",
	}
	var dirs []string
	r.m.SetProfiles(func(dir string) []profile.Profile {
		dirs = append(dirs, dir)
		return []profile.Profile{charles, {Name: "ada", Kind: "codex", Role: "implements the plan"}}
	})
	tid := r.thread(t)
	lead := r.add(t, tid, "claude")

	if err := r.m.postAt(tid, "@charles review the store"); err != nil {
		t.Fatal(err)
	}
	ti, _ := r.m.Thread(tid)
	if len(ti.Members) != 2 {
		t.Fatalf("members = %+v", ti.Members)
	}
	c := ti.Members[1]
	if c.Name != "charles" || c.Kind != "claude" || c.Model != "opus-x" || c.Effort != "high" || c.Profile != "charles" || c.Role != "reviewer" || c.Color != "#c6a0f6" {
		t.Fatalf("the member from the profile: %+v", c)
	}
	if ti.Dir == "" || len(dirs) == 0 || dirs[0] != ti.Dir {
		t.Fatalf("profiles read for %q, the thread is in %q", dirs, ti.Dir)
	}
	waitFor(t, "charles's turn", func() bool { return r.c.turnDones(c.ID) == 1 })
	if p := r.claude.prompts(c.ID); len(p) != 1 || !strings.Contains(p[0], "review the store") {
		t.Fatalf("charles was told %q", p)
	}
	sp, _ := r.claude.spec(c.ID)
	has(t, sp.Instructions, `You are "charles" (claude, reviewer)`, "@name", "plain text",
		"Your role: reviewer.", "Check the tests before the code.", "Voice: dry, terse.", "internal/auth/**")

	// A second mention reaches the same member.
	if err := r.m.postAt(tid, "@charles and the lock order"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "charles's second turn", func() bool { return r.c.turnDones(c.ID) == 2 })
	if ti, _ := r.m.Thread(tid); len(ti.Members) != 2 {
		t.Fatalf("a repeat mention added a member: %+v", ti.Members)
	}

	// A second member from the profile is named after it.
	if _, err := r.m.Spawn(tid, "", SpawnSpec{Kind: "charles", Brief: "a second opinion"}); err != nil {
		t.Fatal(err)
	}
	ti, _ = r.m.Thread(tid)
	if len(ti.Members) != 3 || ti.Members[2].Name != "charles-2" || ti.Members[2].Profile != "charles" {
		t.Fatalf("members after a second charles = %+v", ti.Members)
	}

	// The others are told who charles is, and the roster says it.
	ldr := r.m.get(lead)
	has(t, r.m.instructions(ldr.thread, ldr, true), "charles (claude, reviewer)", "@name")
	has(t, r.m.roster(ldr), "charles · claude · reviewer")

	// The thread keeps where the member came from.
	r.m.Flush()
	_, th, err := r.st.LoadEntries(tid)
	if err != nil {
		t.Fatal(err)
	}
	if m := th.Members[1]; m.Profile != "charles" || m.Role != "reviewer" || m.Color != "#c6a0f6" {
		t.Fatalf("stored member = %+v", m)
	}
}

func TestProfileMentionWithoutProfilesIsText(t *testing.T) {
	r := newRig(t, nil, nil)
	tid := r.thread(t)
	r.add(t, tid, "claude")
	if err := r.m.postAt(tid, "@charles hi"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "claude's turn", func() bool { return r.c.turnDones("claude") == 1 })
	if p := r.claude.prompts("claude"); len(p) != 1 || p[0] != "@charles hi" {
		t.Fatalf("claude was told %q", p)
	}
}

// namesTeam is a hub.Team that only names its teammates.
type namesTeam []string

func (n namesTeam) Names() []string { return n }
func (namesTeam) List() string      { return "" }
func (namesTeam) Status(context.Context, string) (string, error) {
	return "", nil
}
func (namesTeam) Read(int) string { return "" }
func (namesTeam) Post(context.Context, string, string, string) (string, error) {
	return "", nil
}
func (namesTeam) News(string, uint64, time.Time) (string, uint64) { return "", 0 }

// A teammate's name is never a profile, even when deck.json says so.
func TestProfileShadowedByTeammate(t *testing.T) {
	r := newRig(t, nil, nil)
	r.m.SetTeam(namesTeam{"charles"})
	r.m.SetProfiles(func(string) []profile.Profile {
		return []profile.Profile{{Name: "charles", Kind: "codex"}}
	})
	tid := r.thread(t)
	r.add(t, tid, "claude")
	if err := r.m.postAt(tid, "@charles hi"); err != nil {
		t.Fatal(err)
	}
	if ti, _ := r.m.Thread(tid); len(ti.Members) != 1 {
		t.Fatalf("a teammate's name added a member: %+v", ti.Members)
	}
}

func TestRenameRefusesTheContextSigil(t *testing.T) {
	r := newRig(t, nil, nil)
	tid := r.thread(t)
	id := r.add(t, tid, "claude")
	if err := r.m.Rename(id, "a#b"); err == nil {
		t.Fatal("a name with # was taken")
	}
	if err := r.m.Rename(id, "charles"); err != nil {
		t.Fatal(err)
	}
}
