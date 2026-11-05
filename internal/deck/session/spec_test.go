package session

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/config"
)

// listingSpy is a spy adapter that also lists models.
type listingSpy struct {
	*spyAdapter
	models []agent.ModelInfo
}

func (l *listingSpy) Models(context.Context) ([]agent.ModelInfo, error) { return l.models, nil }

// specModels are two models: big takes low and high, small takes low only.
func specModels() []agent.ModelInfo {
	return []agent.ModelInfo{
		{ID: "big", Default: true, Efforts: []agent.EffortInfo{{Level: "low"}, {Level: "high"}}},
		{ID: "small", Efforts: []agent.EffortInfo{{Level: "low"}}},
	}
}

// specRig is a rig whose runs switch model and effort in place, with claude
// listing specModels and the catalog loaded.
func specRig(t *testing.T, cfg config.Config) *rig {
	t.Helper()
	r := newRigWith(t, t.TempDir(), true, cfg, nil, nil)
	r.m.adapters["claude"] = &listingSpy{spyAdapter: r.claude, models: specModels()}
	r.m.WaitCatalog(context.Background(), "claude")
	return r
}

func TestSetMemberSpecChangesOneMemberByIDAndNotTheLead(t *testing.T) {
	r := specRig(t, testCfg())
	tid := r.thread(t)
	lead := r.add(t, tid, "claude")
	other := r.add(t, tid, "claude")
	if _, err := r.m.SetLead(tid, lead); err != nil {
		t.Fatal(err)
	}

	big, high := "big", "high"
	res, err := r.m.SetMemberSpec(tid, other, &big, &high)
	if err != nil {
		t.Fatal(err)
	}
	if res.AgentID != other || res.Model != "big" || res.Effort != "high" || res.ModelFrom != SourceThread || res.EffortFrom != SourceThread {
		t.Fatalf("result = %+v", res)
	}
	info, _ := r.m.Thread(tid)
	if info.Lead != lead {
		t.Fatalf("lead = %q, want %q: a member's spec does not change the lead", info.Lead, lead)
	}
	if a, _ := r.m.Agent(lead); a.Model != "" || a.Effort != "" {
		t.Fatalf("the lead changed too: %+v", a)
	}
	if len(info.Members) != 2 {
		t.Fatalf("members = %+v", info.Members)
	}

	// The roster's name addresses it too; nil leaves a field alone.
	low := "low"
	a, _ := r.m.Agent(other)
	if _, err := r.m.SetMemberSpec(tid, a.Name, nil, &low); err != nil {
		t.Fatal(err)
	}
	if a, _ = r.m.Agent(other); a.Model != "big" || a.Effort != "low" {
		t.Fatalf("by name: %+v", a)
	}
	if _, err := r.m.SetMemberSpec(tid, "ghost", nil, &low); err == nil {
		t.Fatal("an unknown member accepted")
	}
}

func TestSetMemberSpecValidatesEffort(t *testing.T) {
	r := specRig(t, testCfg())
	tid := r.thread(t)
	id := r.add(t, tid, "claude")

	big, turbo := "big", "turbo"
	if _, err := r.m.SetMemberSpec(tid, id, &big, &turbo); err == nil || !strings.Contains(err.Error(), "takes low, high") {
		t.Fatalf("an unknown effort: %v", err)
	}
	if a, _ := r.m.Agent(id); a.Model != "" || a.Effort != "" {
		t.Fatalf("a refused pick was stored: %+v", a)
	}

	high := "high"
	if _, err := r.m.SetMemberSpec(tid, id, &big, &high); err != nil {
		t.Fatal(err)
	}
	// small does not take high: the effort goes back to the model's default.
	small := "small"
	res, err := r.m.SetMemberSpec(tid, id, &small, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Model != "small" || res.Effort != "" || len(res.Warnings) != 1 {
		t.Fatalf("result = %+v", res)
	}
	// A model the list does not name takes any effort: the CLI decides.
	custom := "custom-model"
	if _, err := r.m.SetMemberSpec(tid, id, &custom, &turbo); err != nil {
		t.Fatalf("an unlisted model: %v", err)
	}
}

func TestDefaultCanBeChosenAgainAndSurvivesAResume(t *testing.T) {
	cfg := testCfg()
	cfg.Models.Set("claude", "big")
	cfg.Efforts.Set("claude", "high")
	r := specRig(t, cfg)
	tid := r.thread(t)
	id := r.add(t, tid, "claude")
	if a, _ := r.m.Agent(id); a.Model != "big" || a.Effort != "high" || a.ModelFrom != SourceDefaults {
		t.Fatalf("start = %+v", a)
	}
	def := ""
	if _, err := r.m.SetMemberSpec(tid, id, &def, &def); err != nil {
		t.Fatal(err)
	}
	if a, _ := r.m.Agent(id); a.Model != "" || a.Effort != "" || a.ModelFrom != SourceThread {
		t.Fatalf("Default = %+v", a)
	}
	r.m.Flush()
	if err := r.m.Archive(tid); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.m.Resume(tid); err != nil {
		t.Fatal(err)
	}
	info, _ := r.m.Thread(tid)
	if len(info.Members) != 1 || info.Members[0].Model != "" || info.Members[0].Effort != "" {
		t.Fatalf("after a resume: %+v; the CLI's default stays, the defaults do not come back", info.Members)
	}
	if r.m.config().Models.For("claude") != "big" {
		t.Fatal("a thread pick changed the defaults")
	}
}

func TestSetLeadChangesNoModelOrEffort(t *testing.T) {
	r := specRig(t, testCfg())
	tid := r.thread(t)
	id := r.add(t, tid, "claude")
	big, high := "big", "high"
	if _, err := r.m.SetMemberSpec(tid, id, &big, &high); err != nil {
		t.Fatal(err)
	}
	res, err := r.m.SetLead(tid, "claude")
	if err != nil || res.AgentID != id {
		t.Fatalf("SetLead = %+v, %v", res, err)
	}
	if a, _ := r.m.Agent(id); a.Model != "big" || a.Effort != "high" {
		t.Fatalf("SetLead changed the spec: %+v", a)
	}
}

func TestSubagentsInheritTheirParentsSpec(t *testing.T) {
	r := specRig(t, testCfg())
	tid := r.thread(t)
	parent := r.add(t, tid, "claude")
	if err := r.m.SetLevel(tid, "full"); err != nil {
		t.Fatal(err)
	}
	big, high := "big", "high"
	if _, err := r.m.SetMemberSpec(tid, parent, &big, &high); err != nil {
		t.Fatal(err)
	}

	// No kind, model or effort: all three are the parent's.
	res, err := r.m.Spawn(tid, parent, SpawnSpec{Brief: "check the logs"})
	if err != nil {
		t.Fatal(err)
	}
	a, _ := r.m.Agent(res.AgentID)
	if a.Kind != "claude" || a.Model != "big" || a.Effort != "high" || a.ModelFrom != SourceParent || a.EffortFrom != SourceParent {
		t.Fatalf("subagent = %+v", a)
	}

	// An effort on the spawn wins; the model is still inherited.
	res, err = r.m.Spawn(tid, parent, SpawnSpec{Brief: "quick look", Effort: "low"})
	if err != nil {
		t.Fatal(err)
	}
	if a, _ = r.m.Agent(res.AgentID); a.Model != "big" || a.Effort != "low" || a.EffortFrom != SourceDelegation || a.ModelFrom != SourceParent {
		t.Fatalf("subagent with an effort = %+v", a)
	}

	// Another kind cannot take a claude model; its effort is not known to
	// apply there, so it runs codex's own.
	res, err = r.m.Spawn(tid, parent, SpawnSpec{Brief: "review", Kind: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if a, _ = r.m.Agent(res.AgentID); a.Kind != "codex" || a.Model != "" || a.Effort != "" || a.ModelFrom == SourceParent {
		t.Fatalf("subagent of another kind = %+v", a)
	}
}

func TestSubThreadLeadInheritsTheOpenersSpec(t *testing.T) {
	r := specRig(t, testCfg())
	tid := r.thread(t)
	lead := r.add(t, tid, "claude")
	big, high := "big", "high"
	if _, err := r.m.SetMemberSpec(tid, lead, &big, &high); err != nil {
		t.Fatal(err)
	}
	sub, err := r.m.OpenSubThread(tid, "side", "")
	if err != nil {
		t.Fatal(err)
	}
	info, _ := r.m.Thread(sub)
	if len(info.Members) != 1 {
		t.Fatalf("members = %+v", info.Members)
	}
	if a := info.Members[0]; a.Kind != "claude" || a.Model != "big" || a.Effort != "high" || a.ModelFrom != SourceParent {
		t.Fatalf("sub-thread lead = %+v", a)
	}
}

// slowLister answers only when its context ends; failLister fails at once;
// pairLister answers once its partner has started too.
type slowLister struct{ stubAdapter }

func (*slowLister) Models(ctx context.Context) ([]agent.ModelInfo, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

type failLister struct {
	stubAdapter
	calls atomic.Int32
}

func (f *failLister) Models(context.Context) ([]agent.ModelInfo, error) {
	f.calls.Add(1)
	return nil, errors.New("not logged in")
}

type pairLister struct {
	stubAdapter
	mine, theirs chan struct{}
}

func (p *pairLister) Models(ctx context.Context) ([]agent.ModelInfo, error) {
	close(p.mine)
	select {
	case <-p.theirs:
		return []agent.ModelInfo{{ID: "m", Default: true}}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestCatalogNeverBlocksOnASlowOrFailedCLI(t *testing.T) {
	fail := &failLister{}
	m := NewManager(map[string]agent.Adapter{
		"claude": &slowLister{},
		"codex":  fail,
	}, nil, testCfg())
	t.Cleanup(m.Close)
	m.cat.timeout = 100 * time.Millisecond
	m.cat.backoff = time.Hour

	start := time.Now()
	m.LoadCatalog()
	cat := m.Catalog()
	if took := time.Since(start); took > 50*time.Millisecond {
		t.Fatalf("Catalog took %v: it must answer from what it holds", took)
	}
	if len(cat) != 2 || cat[0].State != CatalogLoading || cat[1].State != CatalogLoading {
		t.Fatalf("catalog = %+v", cat)
	}

	// Waiting is bounded by the per-CLI timeout.
	start = time.Now()
	cat = m.WaitCatalog(context.Background())
	if took := time.Since(start); took > 2*time.Second {
		t.Fatalf("WaitCatalog took %v", took)
	}
	for _, kc := range cat {
		if kc.State != CatalogFailed || kc.Err == "" || kc.Models != nil {
			t.Fatalf("%s = %+v", kc.Kind, kc)
		}
	}

	// A failed CLI is not asked again on every open, nor waited for.
	start = time.Now()
	m.LoadCatalog()
	_ = m.WaitCatalog(context.Background())
	if _, err := m.Models(context.Background(), "codex"); err == nil {
		t.Fatal("the remembered failure was not reported")
	}
	if took := time.Since(start); took > 50*time.Millisecond {
		t.Fatalf("a CLI in back-off was waited for: %v", took)
	}
	if n := fail.calls.Load(); n != 1 {
		t.Fatalf("the failed CLI was asked %d times, want 1", n)
	}
	// A refresh asks it again.
	m.RefreshCatalog("codex")
	waitFor(t, "the refresh", func() bool { return fail.calls.Load() == 2 })
}

func TestCatalogAsksCLIsInParallel(t *testing.T) {
	a, b := make(chan struct{}), make(chan struct{})
	m := NewManager(map[string]agent.Adapter{
		"claude": &pairLister{mine: a, theirs: b},
		"codex":  &pairLister{mine: b, theirs: a},
	}, nil, testCfg())
	t.Cleanup(m.Close)
	m.cat.timeout = 5 * time.Second
	for _, kc := range m.WaitCatalog(context.Background()) {
		if kc.State != CatalogFresh || len(kc.Models) != 1 {
			t.Fatalf("%s = %+v: asked one after the other, each would wait for the other", kc.Kind, kc)
		}
	}
}
