package engine

import (
	"context"
	"fmt"
	"time"

	"github.com/dev-dami/deck/internal/deck/config"
	"github.com/dev-dami/deck/internal/deck/session"
	"github.com/dev-dami/deck/internal/deck/vsh"
)

// opsCommandTimeout bounds one sh.run of an ops contract: inside the 55
// seconds the hub gives one op.
const opsCommandTimeout = 45 * time.Second

// vshBox is one ops contract's vsh sandbox (session.Sandbox).
type vshBox struct{ sb vsh.Sandbox }

func (b vshBox) Run(ctx context.Context, script string) (string, string, int, bool, error) {
	r, err := vsh.Run(ctx, b.sb, script)
	return r.Stdout, r.Stderr, r.Exit, r.Truncated, err
}

// opsSandbox makes the vsh sandbox an ops contract runs in: the shims of
// its scope's presets, its targets, and the sizes peers.sandbox sets. A
// preset whose programs are not installed here refuses the contract.
func opsSandbox(p *config.Peers) session.NewSandbox {
	return func(scope, targets []string, dir string) (session.Sandbox, error) {
		_, mb, kb := p.SandboxLimits()
		presets := vsh.Presets()
		var shims []vsh.Shim
		for _, s := range scope {
			ps, ok := presets[s]
			if !ok {
				return nil, fmt.Errorf("preset %q cannot run here: its programs are not installed", s)
			}
			shims = append(shims, ps...)
		}
		return vshBox{vsh.Sandbox{Dir: dir, QuotaBytes: int64(mb) << 20, OutputCap: kb << 10, Timeout: opsCommandTimeout, Shims: shims, Targets: targets}}, nil
	}
}
