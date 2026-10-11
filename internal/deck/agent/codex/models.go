package codex

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent"
)

// modelListTimeout bounds the whole short-lived app-server round trip.
var modelListTimeout = 10 * time.Second

// Models lists the models the installed codex offers (app-server `model/list`,
// hidden ones excluded). It spawns a short-lived `codex app-server`
// (initialize -> model/list -> close) and caches a successful result for the
// adapter's lifetime. An error is remembered for modelsRetryAfter only;
// callers fall back to free text. Concurrent callers share one listing, and
// one that gives up does not stop it.
func (a *adapter) Models(ctx context.Context) ([]agent.ModelInfo, error) {
	return a.models.Get(ctx, func(ctx context.Context) ([]agent.ModelInfo, error) {
		ctx, cancel := context.WithTimeout(ctx, modelListTimeout)
		defer cancel()
		return a.listModels(ctx)
	})
}

// modelsRetryAfter is how long a failed listing is answered from memory
// rather than by spawning codex again.
var modelsRetryAfter = 10 * time.Second

func (a *adapter) listModels(ctx context.Context) ([]agent.ModelInfo, error) {
	bin := a.binary
	if bin == "" {
		bin = "codex"
	}
	path, err := exec.LookPath(bin)
	if err != nil {
		return nil, fmt.Errorf("codex: cannot find %q: %w", bin, err)
	}
	cmd := exec.Command(path, "app-server")
	cmd.Env = os.Environ()
	configureProc(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr := &tailBuffer{max: 2048}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("codex: start: %w", err)
	}
	client := NewClient(stdout, stdin, a.opts.Tap)
	defer func() {
		_ = stdin.Close()
		killTree(cmd)
		go func() {
			for range client.Incoming() {
			}
		}()
		_ = cmd.Wait()
	}()

	withTail := func(err error) error {
		if ctx.Err() != nil {
			err = fmt.Errorf("%w: %v", err, ctx.Err())
		}
		if tail := stderr.String(); tail != "" {
			err = fmt.Errorf("%w (codex stderr: %s)", err, tail)
		}
		return err
	}

	init := initializeParams{ClientInfo: clientInfo{Name: "deck", Title: "Deck", Version: a.opts.ClientVersion}}
	if err := client.Call(ctx, methodInitialize, init, nil); err != nil {
		return nil, withTail(fmt.Errorf("codex: initialize: %w", err))
	}
	if err := client.Notify(notifyInitialized, nil); err != nil {
		return nil, withTail(fmt.Errorf("codex: initialized: %w", err))
	}

	out := []agent.ModelInfo{}
	cursor := ""
	for page := 0; page < 20; page++ {
		var resp modelListResponse
		if err := client.Call(ctx, methodModelList, modelListParams{Cursor: cursor}, &resp); err != nil {
			return nil, withTail(fmt.Errorf("codex: model/list: %w", err))
		}
		for _, m := range resp.Data {
			if m.Hidden {
				continue
			}
			id := m.Model // the slug the `model` field takes
			if id == "" {
				id = m.ID
			}
			if id == "" {
				continue
			}
			name := m.DisplayName
			if name == "" {
				name = id
			}
			mi := agent.ModelInfo{ID: id, Name: name, Description: m.Description, Default: m.IsDefault, DefaultEffort: m.DefaultEffort}
			for _, e := range m.Efforts {
				if e.ReasoningEffort != "" {
					mi.Efforts = append(mi.Efforts, agent.EffortInfo{Level: e.ReasoningEffort, Description: e.Description})
				}
			}
			out = append(out, mi)
		}
		if resp.NextCursor == "" {
			return out, nil
		}
		cursor = resp.NextCursor
	}
	return nil, errors.New("codex: model/list: too many pages")
}
