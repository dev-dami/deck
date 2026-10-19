package engine

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent/fake"
	"github.com/dev-dami/deck/internal/deck/config"
	"github.com/dev-dami/deck/internal/deck/peer"
	"github.com/dev-dami/deck/internal/deck/session"
	"github.com/dev-dami/deck/internal/deck/vsh"
)

// The presets a max_scope may name are the sandbox's presets.
func TestOpsPresetsAreVshPresets(t *testing.T) {
	if got := slices.Sorted(slices.Values(config.OpsPresets)); !slices.Equal(got, vsh.PresetNames()) {
		t.Fatalf("config.OpsPresets = %v, vsh presets = %v", got, vsh.PresetNames())
	}
}

func TestOpsSandboxRunsInScratch(t *testing.T) {
	box, err := opsSandbox(nil)(nil, nil, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	out, _, exit, _, err := box.Run(context.Background(), "echo hi > a.txt && cat a.txt")
	if err != nil || exit != 0 || out != "hi\n" {
		t.Fatalf("run = %q, exit %d, %v", out, exit, err)
	}
	_, errOut, exit, _, _ := box.Run(context.Background(), "cat /etc/passwd")
	if exit == 0 || !strings.Contains(errOut, "outside the scratch dir") {
		t.Fatalf("read outside scratch: exit %d %q", exit, errOut)
	}
	if _, err := opsSandbox(nil)([]string{"no-such-preset"}, nil, t.TempDir()); err == nil {
		t.Fatal("an unknown preset")
	}
}

// noBox is a sandbox that runs nothing.
type noBox struct{}

func (noBox) Run(context.Context, string) (string, string, int, bool, error) {
	return "", "", 126, false, nil
}

// An ops contract whose agent ends its turn without a result, or that
// does not start, fails at once: its requester does not wait for its time
// to run out.
func TestOpsContractFailsAtOnce(t *testing.T) {
	a := newDeck(t, "a", []fake.Script{turn(finish())}, nil)
	b := newDeck(t, "b", nil, nil)
	link(t, a, b)
	if _, err := config.UpdatePeers(filepath.Join(a.dir, "config.json"), func(p *config.Peers) bool {
		p.Remote = append(p.Remote, config.PeerRemote{Name: "b", Role: config.PeerRoleContractor, MaxScope: []string{"podman-read"}})
		return true
	}); err != nil {
		t.Fatal(err)
	}
	if err := a.svc.Reload(); err != nil {
		t.Fatal(err)
	}
	a.mgr.SetSandbox(func([]string, []string, string) (session.Sandbox, error) { return noBox{}, nil })
	fails := func(want string) {
		t.Helper()
		_, c, err := peer.ParseOps("ask a to report podman containers: name")
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		sent, err := b.svc.ProposeContract(ctx, "a", c)
		if err != nil {
			t.Fatal(err)
		}
		// a fails it and queues the result to b (the peer tests follow it
		// to b); its time (2 hours) has not run out.
		var note string
		waitFor(t, "the failed contract", func() bool {
			for _, k := range a.svc.Contracts() {
				if k.ID == sent.ID && k.State == peer.ContractFailed {
					note = k.Note
					return true
				}
			}
			return false
		})
		if !strings.Contains(note, want) {
			t.Fatalf("note %q, want %q", note, want)
		}
	}
	fails("its agent ended its turn without a result")
	a.ran = func(tid string) { _ = a.mgr.Delete(tid) }
	fails("it did not start")
}
