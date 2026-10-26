package session

import (
	"errors"
	"fmt"
	"strings"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/config"
)

// contextPolicy is the agent.ContextPolicy of kind under cfg.
func contextPolicy(cfg config.Config, kind string) agent.ContextPolicy {
	p := cfg.Context.For(kind)
	return agent.ContextPolicy{
		IsolateGlobalConfig: p.IsolateGlobalConfig, InstructionFiles: p.InstructionFiles,
		MCP: p.MCP, CompactAtPct: p.CompactAtPct,
	}
}

// policyNotes are what the user should know about running a CLI under
// policy p with an adapter of caps c: each knob that was set and that the CLI
// cannot honour (it is ignored), and what isolation leaves out. Nothing for
// the defaults.
func policyNotes(p agent.ContextPolicy, c agent.ContextCaps) []string {
	var notes []string
	if p.IsolateGlobalConfig {
		switch c.IsolateConfig {
		case agent.SupportNone:
			notes = append(notes, "isolate_global_config is not supported; its global config still loads")
		default:
			notes = append(notes, "its global settings are left out, with your own hooks and permissions in them")
		}
	}
	if f := p.InstructionFiles; f != "" && f != agent.InstrFilesInherit && c.InstructionFiles == agent.SupportNone {
		notes = append(notes, "instruction_files is not supported; its instruction files still load")
	}
	if p.MCP == agent.MCPDeckOnly && c.IsolateMCP != agent.SupportNative {
		notes = append(notes, "mcp deck-only is not supported; its own MCP servers still load")
	}
	if p.CompactAtPct > 0 && !c.Compaction.Threshold {
		notes = append(notes, "compact_at_pct is not supported; it compacts at its own default")
	}
	return notes
}

// notePolicy tells the user, once per kind for the life of the Manager, what
// the context policy does and does not do for a's CLI.
func (m *Manager) notePolicy(t *thread, a *agentState, p agent.ContextPolicy) {
	ad := m.adapters[a.kind]
	if ad == nil {
		return
	}
	notes := policyNotes(p, agent.CapsOf(ad))
	if len(notes) == 0 {
		return
	}
	m.mu.Lock()
	if m.policyNoted == nil {
		m.policyNoted = map[string]bool{}
	}
	seen := m.policyNoted[a.kind]
	m.policyNoted[a.kind] = true
	m.mu.Unlock()
	if !seen {
		m.emit(NoticeMsg{ThreadID: t.id, Text: fmt.Sprintf("%s: %s (context in config.json)", a.kind, strings.Join(notes, "; "))})
	}
}

// errNoCompactor is the answer when no member's CLI compacts on request.
var errNoCompactor = errors.New("each agent manages its own context; ask it to /compact")

// Compact asks every member of the thread whose CLI can compact on request
// (agent.Compactor) to compact its conversation. It fails when none can.
func (m *Manager) Compact(threadID string) error {
	t := m.thread(threadID)
	if t == nil {
		return fmt.Errorf("session: no thread %q", threadID)
	}
	t.mu.Lock()
	members := append([]*agentState(nil), t.members...)
	t.mu.Unlock()
	asked := false
	var errs []error
	for _, a := range members {
		c, ok := a.getRun().(agent.Compactor)
		if !ok {
			continue
		}
		asked = true
		if err := c.Compact(); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", a.getName(), err))
		}
	}
	if !asked {
		return errNoCompactor
	}
	return errors.Join(errs...)
}
