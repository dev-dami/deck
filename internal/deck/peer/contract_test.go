package peer

import (
	"encoding/json"
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
	} else if !strings.Contains(err.Error(), "only a contractor may ask it for work") {
		t.Fatalf("the refusal does not say what to do: %v", err)
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

// A proposal to a teammate that is away waits in the outbox; once it
// arrives the requester can still call it off, and an accepted contract's
// work is stopped.
func TestContractWaitsForTheLinkAndCanBeCalledOff(t *testing.T) {
	s := openSub(t)
	ctx := ctxT(t)
	tree := t.TempDir()
	if err := os.WriteFile(filepath.Join(tree, "a.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	base, _ := s.Snap(ctx, tree)
	stopped := make(chan Contract, 1)
	run := func(c Contract) (string, string, func(), error) { return "th", "t1", nil, nil }
	a, b := linkedWith(t,
		ServiceOptions{Contracts: ContractHooks{Run: run, Cancel: func(c Contract) { stopped <- c }}},
		ServiceOptions{SubStore: func() *sub.Store { return s }},
		config.PeerRoleContractor)

	if err := a.SetEnabled(false); err != nil {
		t.Fatal(err)
	}
	c, err := b.ProposeContract(ctx, "a", Contract{Title: "one", Base: base, Thread: "T", Task: "t1"})
	if !errors.As(err, new(QueuedError)) || c.ID == "" || b.out.len() != 1 {
		t.Fatalf("to an absent teammate: %+v, %v (outbox %d)", c, err, b.out.len())
	}
	// Called off before it left: it never goes.
	if !b.CancelContract("T", "t1", "not needed") || b.out.len() != 0 {
		t.Fatalf("cancel of a queued proposal left %d calls", b.out.len())
	}
	c2, err := b.ProposeContract(ctx, "a", Contract{Title: "two", Base: base, Thread: "T", Task: "t2"})
	if !errors.As(err, new(QueuedError)) {
		t.Fatal(err)
	}
	if err := a.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 20*time.Second, "the queued proposal", func() bool { return len(a.Contracts()) == 1 })
	if got := a.Contracts()[0]; got.ID != c2.ID || got.Title != "two" || got.State != ContractProposed {
		t.Fatalf("a's contract = %+v", got)
	}
	if err := a.AnswerContract("b", c2.ID, true, "claude"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 20*time.Second, "b hears it was accepted", func() bool {
		return b.Contracts()[1].State == ContractAccepted
	})
	if !b.CancelContract("T", "t2", "changed my mind") {
		t.Fatal("no open contract for t2")
	}
	select {
	case k := <-stopped:
		if k.ID != c2.ID || k.Thread != "th" || k.State != ContractCancelled {
			t.Fatalf("stopped %+v", k)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the work was not stopped")
	}
	if b.CancelContract("T", "t2", "again") {
		t.Fatal("cancelled twice")
	}
}

// A contract called off while its base is being fetched does not start,
// and the thread made for it is stopped.
func TestContractCalledOffWhileAccepting(t *testing.T) {
	s := openSub(t)
	ctx := ctxT(t)
	tree := t.TempDir()
	if err := os.WriteFile(filepath.Join(tree, "a.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	base, _ := s.Snap(ctx, tree)
	var a *Service
	started := false
	stopped := make(chan Contract, 1)
	run := func(c Contract) (string, string, func(), error) {
		// The requester's call-off lands while the base is fetched.
		if _, err := a.cancelled(Caller{Name: "b"}, json.RawMessage(`{"id":"`+c.ID+`","note":"never mind"}`)); err != nil {
			return "", "", nil, err
		}
		return "th", "t1", func() { started = true }, nil
	}
	a, b := linkedWith(t,
		ServiceOptions{Contracts: ContractHooks{Run: run, Cancel: func(c Contract) { stopped <- c }}},
		ServiceOptions{SubStore: func() *sub.Store { return s }},
		config.PeerRoleContractor)
	c, err := b.ProposeContract(ctx, "a", Contract{Title: "one", Base: base, Thread: "T", Task: "t1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.AnswerContract("b", c.ID, true, "claude"); err == nil {
		t.Fatal("accepted a contract its requester called off")
	}
	if started {
		t.Fatal("the work started")
	}
	if got := a.Contracts()[0]; got.State != ContractCancelled {
		t.Fatalf("a's record = %+v", got)
	}
	select {
	case k := <-stopped:
		if k.Thread != "th" || k.Task != "t1" {
			t.Fatalf("stopped %+v", k)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the thread made for it was not stopped")
	}
}

// A proposal the teammate refuses once it arrives from the outbox ends on
// the requester, which hears of it.
func TestQueuedContractRefused(t *testing.T) {
	s := openSub(t)
	ctx := ctxT(t)
	tree := t.TempDir()
	if err := os.WriteFile(filepath.Join(tree, "a.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	base, _ := s.Snap(ctx, tree)
	news := make(chan Contract, 2)
	a, b := linkedWith(t, ServiceOptions{},
		ServiceOptions{SubStore: func() *sub.Store { return s }, Contracts: ContractHooks{Updated: func(c Contract) { news <- c }}},
		"") // b is only an observer on a
	if err := a.SetEnabled(false); err != nil {
		t.Fatal(err)
	}
	if _, err := b.ProposeContract(ctx, "a", Contract{Title: "x", Base: base}); !errors.As(err, new(QueuedError)) {
		t.Fatal(err)
	}
	if err := a.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	got := waitNews(t, news)
	if got.State != ContractFailed || !strings.Contains(got.Note, "a refused it") {
		t.Fatalf("news = %+v", got)
	}
}

// A proposal no one answers ends on both sides after proposalTTL.
func TestUnansweredProposalExpires(t *testing.T) {
	s := openSub(t)
	ctx := ctxT(t)
	tree := t.TempDir()
	if err := os.WriteFile(filepath.Join(tree, "a.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	base, _ := s.Snap(ctx, tree)
	news := make(chan Contract, 2)
	a, b := linkedWith(t, ServiceOptions{},
		ServiceOptions{SubStore: func() *sub.Store { return s }, Contracts: ContractHooks{Updated: func(c Contract) { news <- c }}},
		config.PeerRoleContractor)
	c, err := b.ProposeContract(ctx, "a", Contract{Title: "x", Base: base})
	if err != nil {
		t.Fatal(err)
	}
	if !b.granted("a", base) {
		t.Fatal("the base is not shared while the proposal is open")
	}
	later := time.Now().Add(proposalTTL + time.Minute)
	b.sweepContracts(later)
	if got := waitNews(t, news); got.ID != c.ID || got.State != ContractFailed || !strings.Contains(got.Note, "did not answer within 24 hours") {
		t.Fatalf("news = %+v", got)
	}
	if b.granted("a", base) {
		t.Fatal("the base is still shared")
	}
	// a ends it too: by its own sweep, or because b's call-off came first.
	a.sweepContracts(later)
	if got := a.Contracts()[0]; !got.Final() || !strings.Contains(got.Note, "answer") {
		t.Fatalf("a's record = %+v", got)
	}
	if err := a.AnswerContract("b", c.ID, true, "claude"); err == nil {
		t.Fatal("accepted an expired proposal")
	}
}
