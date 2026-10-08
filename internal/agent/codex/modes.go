package codex

import (
	"fmt"
	"strings"

	"github.com/dev-dami/deck/internal/agent"
)

// planNote is prepended (as its own text input item) to every turn started
// while the thread is in plan mode. The stable app-server schema has no
// per-turn developer instructions and `collaborationMode` on turn/start is
// experimental-only (needs initialize capabilities.experimentalApi and a
// model in its settings), so the read-only sandbox is backed by this note.
const planNote = "[Deck plan mode] Plan only: investigate and propose; do not modify files or run commands that change state.\n\n"

// modeSettings is the approval policy and sandbox mode (SandboxMode strings)
// a Deck permission mode maps to.
type modeSettings struct {
	approval string // AskForApproval
	sandbox  string // SandboxMode
}

// settingsFor maps a Deck mode to codex settings.
//
//	ask (or empty): the adapter's configured policy (default on-request + workspace-write)
//	plan:           on-request + read-only
//	yolo:           never + danger-full-access (codex's --dangerously-bypass-approvals-and-sandbox)
func (o Options) settingsFor(m agent.Mode) (modeSettings, error) {
	switch m {
	case "", agent.ModeAsk:
		return modeSettings{o.ApprovalPolicy, o.Sandbox}, nil
	case agent.ModePlan:
		return modeSettings{"on-request", "read-only"}, nil
	case agent.ModeYolo:
		return modeSettings{"never", "danger-full-access"}, nil
	}
	return modeSettings{}, fmt.Errorf("codex: unknown mode %q", m)
}

// sandboxPolicyFor converts a SandboxMode into the turn-level SandboxPolicy.
func sandboxPolicyFor(sandbox string) *sandboxPolicy {
	switch sandbox {
	case "read-only":
		return &sandboxPolicy{Type: "readOnly"}
	case "danger-full-access":
		return &sandboxPolicy{Type: "dangerFullAccess"}
	default:
		return &sandboxPolicy{Type: "workspaceWrite"}
	}
}

// stripPlanNote removes planNote from the echoed user message.
func stripPlanNote(s string) string { return strings.TrimPrefix(s, planNote) }
