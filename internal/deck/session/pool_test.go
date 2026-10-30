package session

import (
	"slices"
	"strings"
	"testing"

	"github.com/dev-dami/deck/internal/deck/agent/fake"
)

func TestKindAddressesFreeMember(t *testing.T) {
	kinds := []string{"claude", "codex"}
	refs := []MemberRef{
		{ID: "claude", Name: "claude1", Kind: "claude", Busy: true},
		{ID: "claude2", Name: "claude2", Kind: "claude"},
		{ID: "codex", Name: "codex", Kind: "codex"},
	}
	if ad := ParseAddressing("$claude look", refs, kinds); !slices.Equal(ad.Members, []string{"claude2"}) || ad.Body != "look" {
		t.Fatalf("$claude = %+v", ad)
	}
	// A name still reaches exactly that member, busy or not.
	if ad := ParseAddressing("$claude1 look", refs, kinds); !slices.Equal(ad.Members, []string{"claude"}) {
		t.Fatalf("$claude1 = %+v", ad)
	}

	// All busy: the one with fewer messages waiting, then the last speaker.
	busy := []MemberRef{
		{ID: "a", Kind: "claude", Busy: true, Waiting: 2},
		{ID: "b", Kind: "claude", Busy: true, Waiting: 1},
		{ID: "c", Kind: "claude", Busy: true, Waiting: 1, Last: true},
		{ID: "d", Kind: "claude", Exited: true},
	}
	if m, ok := choose(busy, "claude"); !ok || m.ID != "c" {
		t.Fatalf("choose = %+v %v", m, ok)
	}
	if _, ok := choose(busy, "codex"); ok {
		t.Fatal("choose found a codex")
	}
}

// The user's messages have no kind suffixes or counts: "$claude+",
// "$claude*" and "$claude×3" are plain text, and a race starts from /race.
func TestKindSuffixesAreText(t *testing.T) {
	kinds := []string{"claude", "codex"}
	refs := []MemberRef{
		{ID: "claude", Name: "claude1", Kind: "claude"},
		{ID: "claude2", Name: "claude2", Kind: "claude"},
	}
	for _, s := range []string{
		"$claude* review this", "$claude+ review this", "@claude+ @claude+ go", "$codex* go",
		"$claude×2 $codex×1 fix the flaky test", "@claude*3 fix it", "$codex×3 fix it",
		"echo $HOME* now", "$PATH+ x", "$claude*x y",
	} {
		ad := ParseAddressing(s, refs, kinds)
		if ad.Mentioned() || ad.Body != s {
			t.Errorf("%q addressed %+v", s, ad)
		}
	}
	// A plain mention before one still counts; the rest is the body.
	if ad := ParseAddressing("$codex $claude+ go", refs, kinds); len(ad.NewKinds) != 1 || ad.Body != "$claude+ go" {
		t.Fatalf("$codex $claude+ = %+v", ad)
	}
}

func TestTypedRaceIsText(t *testing.T) {
	r, tid, _ := raceRig(t, nil, []fake.Script{turn(say("ok"), finish())})
	r.add(t, tid, "codex")
	if err := r.m.postAt(tid, "$codex×3 fix the flaky test"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "codex's turn", func() bool { return r.c.turnDones("codex") == 1 })
	if tasks := r.m.Tasks(tid); len(tasks) != 0 {
		t.Fatalf("a typed race made tasks: %+v", tasks)
	}
	if ti, _ := r.m.Thread(tid); len(ti.Members) != 1 {
		t.Fatalf("a typed race added members: %+v", ti.Members)
	}
	has(t, r.codex.prompts("codex")[0], "$codex×3 fix the flaky test")
}

func TestSecondInstanceRenamesFirst(t *testing.T) {
	r := newRig(t, nil, nil)
	tid := r.thread(t)
	r.add(t, tid, "claude")
	r.add(t, tid, "claude")
	r.add(t, tid, "claude")
	if err := r.m.postAt(tid, "$codex"); err != nil {
		t.Fatal(err)
	}
	th, _ := r.m.Thread(tid)
	var got []string
	for _, a := range th.Members {
		got = append(got, a.ID+"="+a.Name)
	}
	want := []string{"claude=claude1", "claude2=claude2", "claude3=claude3", "codex=codex"}
	if !slices.Equal(got, want) {
		t.Fatalf("members %v, want %v", got, want)
	}
	// While there are two, nobody may take the kind's own name.
	if err := r.m.Rename("claude2", "claude"); err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("rename to the kind: %v", err)
	}
	if err := r.m.Rename("claude2", "a*b"); err == nil {
		t.Fatal("rename with a suffix character worked")
	}
	if err := r.m.Rename("codex", "codex"); err != nil {
		t.Fatalf("the only codex may keep its kind's name: %v", err)
	}
}

func TestAgentsReachPools(t *testing.T) {
	r := newRig(t, nil, []fake.Script{
		turn(sendTo("claude*", "both of you: review"), sendTo("claude", "one of you: fix"),
			fake.Step{Exec: &fake.Exec{Op: "thread.roster"}}, say("asked"), finish()),
	})
	tid := r.thread(t)
	c1 := r.add(t, tid, "claude")
	c2 := r.add(t, tid, "claude")
	r.add(t, tid, "codex")
	if err := r.m.postAt(tid, "$codex go"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "codex", func() bool { return r.c.turnDones("codex") == 1 })

	res := r.c.toolResults("codex", "message")
	if len(res) != 2 {
		t.Fatalf("results = %q", res)
	}
	has(t, res[0], "delivered to claude1")
	has(t, res[0], "delivered to claude2")
	// One claude gets it, delivered or queued behind the broadcast.
	if !strings.Contains(res[1], " claude") || strings.Contains(res[1], ";") {
		t.Fatalf("pool send = %q", res[1])
	}
	rel := r.c.relays()
	if len(rel) < 2 || !slices.Equal(rel[0].To, []string{c1, c2}) || len(rel[1].To) != 1 {
		t.Fatalf("relays = %+v", rel)
	}

	roster := r.c.toolResults("codex", "deck")
	if len(roster) != 1 {
		t.Fatalf("roster results = %q", roster)
	}
	has(t, roster[0], "claude1 · claude")
	has(t, roster[0], "codex (you) · codex")
	has(t, roster[0], `2 claude are running`)

	// codex was told, when the second claude joined, how to reach them.
	prompts := r.codex.prompts("codex")
	if len(prompts) == 0 {
		t.Fatal("codex got no prompt")
	}
	has(t, prompts[0], `claude2 (claude) joined the thread; with 2 claude here, "claude" reaches whichever is free and "claude*" all of them`)
	if strings.Count(prompts[0], "reaches whichever is free") != 1 {
		t.Fatalf("the pool is explained more than once:\n%s", prompts[0])
	}
}

func TestInstructionsMentionRosterUnclipped(t *testing.T) {
	cfg := testCfg()
	cfg.DeckAttribution = true // the longest instructions
	r := newRigWith(t, t.TempDir(), false, cfg, nil, nil)
	tid := r.thread(t)
	r.add(t, tid, "codex")
	r.add(t, tid, "codex")
	id := r.add(t, tid, "claude")
	sp, ok := r.claude.spec(id)
	if !ok {
		t.Fatal("no spec")
	}
	has(t, sp.Instructions, "thread.roster")
	has(t, sp.Instructions, "Write plan files")
	if strings.Contains(sp.Instructions, "[cut]") {
		t.Fatalf("instructions were clipped (%d chars):\n%s", len(sp.Instructions), sp.Instructions)
	}
	t.Logf("instructions: %d chars", len(sp.Instructions))
}

func TestResumeFreesKindNameForPool(t *testing.T) {
	r := newRig(t, nil, nil)
	tid := r.thread(t)
	r.add(t, tid, "claude")
	r.add(t, tid, "claude")
	// A thread saved before pools: its first claude is still called "claude".
	a := r.m.get("claude")
	a.setName("claude")
	r.m.persistMeta(a.thread)
	r.m.Close()

	r2 := newRigIn(t, r.dir, nil, nil)
	ti, _, err := r2.m.Resume(tid)
	if err != nil {
		t.Fatal(err)
	}
	if len(ti.Members) != 2 || ti.Members[0].Name != "claude1" || ti.Members[1].Name != "claude2" {
		t.Fatalf("members = %+v", ti.Members)
	}
}
