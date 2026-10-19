package peer

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dev-dami/deck/internal/deck/config"
	"github.com/dev-dami/deck/internal/deck/sub"
)

func openSub(t *testing.T) *sub.Store {
	t.Helper()
	s, err := sub.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// b asks a (which made b a contractor) to do a task: a's person accepts it,
// a fetches b's tree, does the work and sends a capsule, which b fetches.
// Each side shares only what the contract needs, with the other alone.
func TestContractRoundTrip(t *testing.T) {
	aStore, bStore := openSub(t), openSub(t)
	ctx := ctxT(t)
	tree := t.TempDir()
	if err := os.WriteFile(filepath.Join(tree, "a.txt"), []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	base, err := bStore.Snap(ctx, tree)
	if err != nil {
		t.Fatal(err)
	}

	var a *Service
	work := t.TempDir()
	started := make(chan struct{}, 1)
	run := func(c Contract) (string, string, func(), error) {
		if c.Agent != "claude" || c.Base != base || c.Title != "fix a.txt" {
			return "", "", nil, errors.New("wrong contract")
		}
		if _, err := a.FetchCapsule(ctx, c.Peer, c.Base, aStore); err != nil {
			return "", "", nil, err
		}
		if err := aStore.Checkout(ctx, work, c.Base); err != nil {
			return "", "", nil, err
		}
		return "th1", "t1", func() { started <- struct{}{} }, nil
	}
	news := make(chan Contract, 4)
	a, b := linkedWith(t,
		ServiceOptions{SubStore: func() *sub.Store { return aStore }, Contracts: ContractHooks{Run: run}},
		ServiceOptions{SubStore: func() *sub.Store { return bStore }, Contracts: ContractHooks{Updated: func(c Contract) { news <- c }}},
		config.PeerRoleContractor)

	c, err := b.ProposeContract(ctx, "a", Contract{Title: "fix a.txt", Brief: "make it new", Base: base, Files: 1, Thread: "T", Task: "t7"})
	if err != nil {
		t.Fatal(err)
	}
	got := a.Snapshot().Contracts
	if len(got) != 1 || !got[0].Inbound || got[0].State != ContractProposed || got[0].Peer != "b" || got[0].Brief != "make it new" {
		t.Fatalf("a's contracts = %+v", got)
	}
	// Nothing of b's tree is fetchable but by a, and only for the contract.
	if _, err := b.FetchCapsule(ctx, "a", base, openSub(t)); err == nil {
		t.Fatal("a served b's own base back to b")
	}

	if err := a.AnswerContract("b", c.ID, true, "claude"); err != nil {
		t.Fatal(err)
	}
	<-started
	if err := a.AnswerContract("b", c.ID, true, "claude"); err == nil {
		t.Fatal("a contract started twice")
	}
	accepted := waitNews(t, news)
	if accepted.State != ContractAccepted || accepted.Agent != "claude" || accepted.Thread != "T" || accepted.Task != "t7" {
		t.Fatalf("accepted news = %+v", accepted)
	}

	if err := os.WriteFile(filepath.Join(work, "a.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	capsule, err := aStore.Capture(ctx, work, base, "", sub.Meta{Source: "claude"})
	if err != nil || capsule == "" {
		t.Fatalf("capture: %q %v", capsule, err)
	}
	if a.FinishContract("th1", "t2", true, capsule, "x") {
		t.Fatal("finished a task that is no contract's")
	}
	if !a.FinishContract("th1", "t1", true, capsule, "made a.txt new") {
		t.Fatal("the contract's task was not found")
	}
	done := waitNews(t, news)
	if done.State != ContractDone || done.Capsule != capsule || done.Summary != "made a.txt new" {
		t.Fatalf("done news = %+v", done)
	}
	if _, err := b.FetchCapsule(ctx, "a", capsule, bStore); err != nil {
		t.Fatalf("b fetches the result: %v", err)
	}
	if err := bStore.Apply(ctx, tree, capsule); err != nil {
		t.Fatal(err)
	}
	// The contract is over: b's base is no longer shared with a.
	if _, err := a.FetchCapsule(ctx, "b", base, openSub(t)); err == nil {
		t.Fatal("b still serves its base after the contract ended")
	}
}

func waitNews(t *testing.T, news chan Contract) Contract {
	t.Helper()
	select {
	case c := <-news:
		return c
	case <-time.After(20 * time.Second):
		t.Fatal("no news of the contract")
	}
	return Contract{}
}

func TestContractRefusals(t *testing.T) {
	s := openSub(t)
	ctx := ctxT(t)
	tree := t.TempDir()
	if err := os.WriteFile(filepath.Join(tree, "a.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	base, _ := s.Snap(ctx, tree)
	opts := ServiceOptions{SubStore: func() *sub.Store { return s }}
	a, b := linkedWith(t, ServiceOptions{}, opts, "") // b is only an observer on a
	if _, err := b.ProposeContract(ctx, "a", Contract{Title: "x", Base: base}); err == nil {
		t.Fatal("an observer proposed a contract")
	}
	if len(b.Contracts())+len(a.Contracts()) != 0 {
		t.Fatalf("a refused proposal left records: %+v %+v", b.Contracts(), a.Contracts())
	}
	if _, err := b.ProposeContract(ctx, "nobody", Contract{Title: "x", Base: base}); err == nil {
		t.Fatal("proposed to a Deck that is not paired")
	}

	// A teammate can only report on contracts this Deck proposed to it.
	h := &ContractHandler{Base: &StatusHandler{Name: "me"}, S: b}
	_, err := h.Execute(ctx, Caller{Name: "a"}, OpContractResult, []byte(`{"id":"abc","state":"done"}`))
	if !errors.Is(err, ErrUnknownOp) {
		t.Fatalf("a result for no contract: %v", err)
	}
	// Proposals are capped per teammate, and the text is cleaned.
	for i := range maxProposed + 1 {
		_, err := b.proposed(Caller{Name: "c"}, []byte(`{"id":"k`+string(rune('a'+i))+`","title":"do \u001b[2Jit\nnow","base":"`+string(base)+`"}`))
		if i < maxProposed && err != nil || i == maxProposed && err == nil {
			t.Fatalf("proposal %d: %v", i, err)
		}
	}
	if c := b.Contracts()[0]; c.Title != "do [2Jit now" || strings.ContainsRune(c.Title, 0x1b) {
		t.Fatalf("title = %q", c.Title)
	}
	if !(Caller{Role: config.PeerRoleObserver}).Allowed(OpContractStatus) || (Caller{}).Allowed(OpContractPropose) {
		t.Fatal("the contract ops are not allowed as documented")
	}
}
