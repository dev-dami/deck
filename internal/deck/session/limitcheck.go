package session

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent"
)

// Asking a CLI for its plan limits, and starting its rolling window, apart
// from any thread.
//
// Both start the kind's CLI on its own, in a scratch folder, with none of
// the user's config or instruction files and no tools: a throwaway session
// no room shows. A check sends nothing (the CLIs report their windows when
// they start: Claude Code answers get_usage, Codex account/rateLimits/read)
// and stops at the first report. Starting the window sends one tiny prompt
// on the cheapest model the CLI lists, which uses a little of the plan, and
// stops when its turn is done.

// checkTimeout bounds a check; startTimeout a window start (a model has to
// answer).
const (
	checkTimeout = 30 * time.Second
	startTimeout = 90 * time.Second
)

// windowPrompt is the window start's prompt: as short as an answer gets.
const windowPrompt = "Reply with the single word OK."

// ErrNoLimits says a kind's CLI has no way to report plan limits Deck may use.
var ErrNoLimits = errors.New("it does not report plan limits")

var throwawaySeq atomic.Int64

// CheckLimits asks kind's CLI for the plan's windows now. The answer is also
// what the manager knows of kind from then on (routing, waits).
func (m *Manager) CheckLimits(ctx context.Context, kind string) (agent.Limits, error) {
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	l, _, err := m.throwaway(ctx, kind, "", "", "")
	if err != nil {
		return agent.Limits{}, err
	}
	return l, nil
}

// StartedWindow is what StartWindow did: the model it asked and the limits
// the CLI reported after.
type StartedWindow struct {
	Model  string
	Limits agent.Limits
}

// StartWindow starts kind's rolling plan window now, with one minimal prompt
// on its cheapest model, so the window resets that much sooner. It uses a
// little of the plan.
func (m *Manager) StartWindow(ctx context.Context, kind string) (StartedWindow, error) {
	ctx, cancel := context.WithTimeout(ctx, startTimeout)
	defer cancel()
	model, effort := m.cheapest(ctx, kind)
	l, _, err := m.throwaway(ctx, kind, model, effort, windowPrompt)
	if err != nil {
		return StartedWindow{Model: model}, err
	}
	return StartedWindow{Model: model, Limits: l}, nil
}

// cheapest is the model (and its lowest effort) a window start uses: Claude
// Code's haiku alias, else the listed model that reads as the smallest
// ("mini", "nano", "flash", "haiku", "lite"), else the CLI's default.
func (m *Manager) cheapest(ctx context.Context, kind string) (model, effort string) {
	if kind == "claude" {
		return "haiku", ""
	}
	ml, ok := m.adapters[kind].(agent.ModelLister)
	if !ok {
		return "", ""
	}
	models, err := ml.Models(ctx)
	if err != nil || len(models) == 0 {
		return "", ""
	}
	pick := -1
	for _, w := range []string{"nano", "mini", "flash", "haiku", "lite", "small"} {
		pick = slices.IndexFunc(models, func(mi agent.ModelInfo) bool { return strings.Contains(strings.ToLower(mi.ID), w) })
		if pick >= 0 {
			break
		}
	}
	if pick < 0 {
		return "", ""
	}
	mi := models[pick]
	if len(mi.Efforts) > 0 {
		effort = mi.Efforts[0].Level
	}
	return mi.ID, effort
}

// throwaway runs kind's CLI apart from any thread: until its first limits
// report when prompt is empty, else until its turn ends. It returns the
// limits it reported (folded into what the manager knows of kind) and the
// turn's error text.
func (m *Manager) throwaway(ctx context.Context, kind, model, effort, prompt string) (agent.Limits, string, error) {
	ad := m.adapters[kind]
	if ad == nil {
		return agent.Limits{}, "", fmt.Errorf("no agent %q is installed", kind)
	}
	if m.isClosed() {
		return agent.Limits{}, "", errClosed
	}
	dir := filepath.Join(os.TempDir(), "deck-limits")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return agent.Limits{}, "", err
	}
	cfg := m.config()
	spec := agent.Spec{
		ID:  fmt.Sprintf("deck-%s-%d", kind, throwawaySeq.Add(1)),
		Dir: dir, Model: model, Effort: effort, FirstPrompt: prompt, Mode: agent.ModePlan,
		Context: agent.ContextPolicy{IsolateGlobalConfig: true, InstructionFiles: agent.InstrFilesNone, MCP: agent.MCPDeckOnly},
	}
	if o, ok := ad.(agent.OpsSafe); ok && o.OpsSafe() {
		spec.NoTools = true
	}
	if b := cfg.Binary(kind); b != kind {
		spec.Binary = b
	}
	run, err := ad.Start(ctx, spec)
	if err != nil {
		return agent.Limits{}, "", err
	}
	defer func() { go run.Close() }()

	var (
		lim     agent.Limits
		got     bool
		errText string
	)
	evs, aps := run.Events(), run.Approvals()
	for {
		select {
		case <-ctx.Done():
			if got && prompt == "" {
				return lim, "", nil
			}
			return lim, errText, fmt.Errorf("%s did not answer in time", kind)
		case req, ok := <-aps:
			if !ok {
				aps = nil
				continue
			}
			if req.Respond != nil {
				req.Respond(agent.Decision{Allow: false, Reason: "this session only checks the plan"})
			}
		case ev, ok := <-evs:
			if !ok {
				return m.endThrowaway(kind, lim, got, errText, prompt)
			}
			switch ev.Kind {
			case agent.KindLimits:
				if ev.Limits == nil {
					continue
				}
				lim, got = mergeLimits(lim, *ev.Limits), true
				if prompt == "" && (len(lim.Windows) > 0 || lim.Unavailable != "") {
					return m.endThrowaway(kind, lim, got, "", prompt)
				}
			case agent.KindError:
				errText = strings.TrimSpace(ev.Text)
			case agent.KindTurnDone, agent.KindExited:
				if prompt != "" || ev.Kind == agent.KindExited {
					return m.endThrowaway(kind, lim, got, errText, prompt)
				}
			}
		}
	}
}

// endThrowaway folds what a throwaway run reported into the manager and says
// how it went.
func (m *Manager) endThrowaway(kind string, lim agent.Limits, got bool, errText, prompt string) (agent.Limits, string, error) {
	if got && len(lim.Windows) > 0 {
		m.mu.Lock()
		if m.limits == nil {
			m.limits = map[string]agent.Limits{}
		}
		m.limits[kind] = mergeLimits(m.limits[kind], lim)
		m.mu.Unlock()
		m.rearmWaits(kind)
	}
	switch {
	case prompt != "" && errText != "":
		return lim, errText, errors.New(errText)
	case prompt != "":
		return lim, "", nil
	case lim.Unavailable == agent.LimitsUnsupported:
		return lim, "", ErrNoLimits
	case !got || len(lim.Windows) == 0 && lim.Unavailable != "":
		if errText != "" {
			return lim, errText, errors.New(errText)
		}
		return lim, "", fmt.Errorf("%s did not report its limits", kind)
	}
	return lim, "", nil
}

// mergeLimits folds a new report into what is known: windows by id, the
// plan when named. A report with no windows keeps the windows known.
func mergeLimits(old, nu agent.Limits) agent.Limits {
	out := agent.Limits{Plan: old.Plan, Unavailable: nu.Unavailable}
	if nu.Plan != "" {
		out.Plan = nu.Plan
	}
	out.Windows = append(out.Windows, old.Windows...)
	for _, w := range nu.Windows {
		if i := slices.IndexFunc(out.Windows, func(o agent.LimitWindow) bool { return o.ID == w.ID }); i >= 0 {
			out.Windows[i] = w
		} else {
			out.Windows = append(out.Windows, w)
		}
	}
	if len(out.Windows) > 0 {
		out.Unavailable = ""
	}
	return out
}
