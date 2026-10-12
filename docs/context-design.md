# Context handling: current state and a model-agnostic design

Status: proposal, not implemented. Facts marked **[code]** were verified in the repo, **[help]** come from the installed CLIs' `--help`, **[assume]** are unverified assumptions about external CLIs (`agy` was not installed, so Antigravity facts are code-only).

## 1. Shared pieces

- Contract (`internal/deck/agent/agent.go`): `Usage.ContextTokens` / `ContextWindow` are the only context fields (0 = not reported). `Spec.Instructions` is one string. There is no compaction event, no capability flag, and no field saying where a number came from. [code]
- Instructions are built by `Manager.instructions` (`session/briefing.go:273-309`), clipped to `maxInstrChars = 1400` (about 350 tokens).
- Per-turn briefing (`buildPrompt`, `briefing.go:197-239`): each entry clipped to 6000 runes, whole briefing keeps its last 40,000 runes (~10k tokens). The same caps apply to every agent regardless of window size.
- Hub output cap is `MaxOutput = 20 KiB` (`hub/hub.go:37`). **`thread.read` is not capped by it**: it returns up to 40,000 runes (`approve/broker.go:326-334` -> `readThread`, `briefing.go:243-268`).
- `agentState.apply` keeps `ctxTokens`/`ctxWindow` only when > 0 (`session/manager.go:1564-1577`); `SetModel` does not reset them, so a stale window survives a model switch.
- UI: session % is the fullest agent's (`ui/model/context_fill.go:35-45`); sidebar, marks and usage pane show it per agent. "Summarize" is refused: "each agent manages its own context" (`deck_workspace.go:373-375`).

## 2. Per-agent behavior

| | Claude | Codex | OpenCode | Antigravity |
|---|---|---|---|---|
| Launch | `claude -p` stream-json, `--settings` temp file | `codex app-server`, Deck MCP via per-thread `config` overrides | `opencode acp`, MCP via ACP `mcpServers` | `agy` stream-json |
| Global config isolation | MCP only (`--strict-mcp-config`, `ENABLE_CLAUDEAI_MCP_SERVERS=false`). CLAUDE.md, memory, skills, plugins, hooks still load | none | none | none; `agy mcp add` **writes the user's global config** and removes it on Close |
| Instructions | `--append-system-prompt` | `DeveloperInstructions` (start + resume) | prefixed to first prompt **per process** | prefixed to first send **per run** |
| Repeats on Deck Resume? | no | unknown | **yes** (`briefed` re-armed, `adapter.go:196`) | **yes** (`firstSend`, `antigravity.go:149`) |
| Context source | derived from last assistant usage; window from `result.modelUsage`, else family fallback (200k) | `thread/tokenUsage/updated`; authoritative incl. window | ACP `usage_update{used,size}` | **none** (always 0) |
| Compaction | `compact_boundary` ignored; `--autocompact` not exposed | `thread/compacted` ignored | none | none |
| Steer | interrupt (kill + `--resume`) then send | native `turn/steer` (cheapest) | interrupt + new prompt | interrupt + resend |
| Setting changes | respawn with `--resume` | per-turn override, no restart | `set_config_option`, no restart | respawn |

Installed Claude CLI offers `--setting-sources`, `--bare`, `--disable-slash-commands`, `--exclude-dynamic-system-prompt-sections`, `--autocompact <auto|100k-1M>`. [help] Codex `thread/start` returns `instructionSources` (loaded instruction files), which Deck ignores. [code]

Known inaccuracy: Claude's fallback assumes 200k for opus/sonnet/haiku/fable unless the id contains `1m`; `testdata/limits.jsonl:7` shows `claude-opus-5-5` reporting a 1,000,000 window, so on a fresh run the first turn's meter can read up to 5x too full. [code]

## 3. Gaps

1. Config isolation is uneven; the reason Claude isolates MCP applies equally to the others.
2. Instruction delivery differs and two adapters duplicate it on every resume.
3. Context source quality differs (Codex authoritative, Claude derived, OpenCode delegated, Antigravity absent) but the UI shows all with equal confidence.
4. Compaction is invisible and untriggerable.
5. Budgets (briefing 40k, entry 6k, thread.read 40k, hub 20 KiB) ignore the window.
6. Window goes stale on model switch.
7. No breakdown of baseline vs conversation vs Deck-injected tokens.
8. `ModelInfo` carries no context window.

## 4. Proposed design

### 4.1 Contract additions (all optional, zero-value safe)

```go
type ContextCaps struct {
    InstructionSlot       InstrSlot   // System | Developer | FirstPrompt
    InstrPersistsOnResume bool
    IsolateConfig, IsolateMCP, InstructionFiles Support // Native | Partial | None
    ReportsContext, ReportsWindow Source // CLI | Derived | Estimated | None
    NativeSteer bool
    Compaction  CompactCaps // Manual, Threshold, Notifies
}
type ContextCapser interface{ ContextCaps() ContextCaps }

type ContextPolicy struct {
    IsolateGlobalConfig bool
    InstructionFiles    string // inherit | project-only | none
    MCP                 string // deck-only | inherit
    CompactAtPct        int    // 0 = CLI default
}
// Spec gains: Context ContextPolicy
// Usage gains: ContextSource string; BaselineTokens int
// New: KindCompacted event, optional Compactor{ Compact() error }, ModelInfo.ContextWindow
```

### 4.2 Adapter mapping

| Knob | Claude | Codex | OpenCode | Antigravity |
|---|---|---|---|---|
| Isolate config / files | `--setting-sources project` or `--bare` | `config` overrides [assume] | `OPENCODE_CONFIG_CONTENT` [assume] | unsupported -> report None, warn |
| MCP deck-only | already | try disabling user servers [assume] | partial | none |
| Instructions | system prompt | developer | first prompt, **once per session** (persist `briefed` in member meta) | same as OpenCode |
| Context source | derived, mark `fallback` until result | cli | cli | estimate from last result, window from table |
| Compaction | `--autocompact`, translate `compact_boundary` | translate `thread/compacted` | none | none |

### 4.3 Budgets derived from the window (`briefing.go`)

`budgetFor(agent)`: window from `ctxWindow` -> `ModelInfo.ContextWindow` -> 128k.
Briefing tail = clamp(4% of window x 4 chars, 16k, 160k runes); entry clip = briefing/6; `thread.read` = briefing budget, also capped by the hub cap; hub `MaxOutput` = clamp(1.5% of window x 4 chars, 8 KiB, 64 KiB), passed per agent via `Env`. When free space is low (> 80% full) halve the briefing and point to `thread.read`.

### 4.4 Config (`internal/deck/config/config.go`)

```json
"context": {
  "isolate_global_config": false,
  "instruction_files": "inherit",
  "mcp": "deck-only",
  "compact_at_pct": 0,
  "briefing_pct": 4,
  "tool_output_pct": 1.5,
  "per_agent": { "codex": { "mcp": "inherit" } }
}
```
Defaults reproduce today's behavior. Validate in `Validate()` (config.go:234).

### 4.5 Manager and UI

- `agentState` gains `ctxSource`, `ctxBaseline`, `deckTokens` (estimate of briefing/steer/instruction text sent, ~4 chars per token). Reset `ctxWindow` on `SetModel`/`SetEffort`.
- `KindCompacted` -> notice + store entry so briefings can mention it.
- `AgentInfo` / `workspace.AgentLine` gain source, baseline, deckTokens.
- Sidebar: `62% ctx`, `~` for estimate/fallback, dim when unknown. `pane_usage.go`: stacked meter (baseline / conversation / Deck-injected / free). `context_fill.go`: do not let `estimate` sources set the session maximum silently.
- `AgentSummarize` calls `Compactor` when available, else keeps today's error.
- Unsupported knobs degrade silently in the adapter, are reported in `ContextCaps`, and the manager emits one start-up notice (e.g. "antigravity: global agy config and MCP servers stay loaded").

### 4.6 Risks

Isolation flags depend on CLI versions; `--setting-sources` also drops user hooks/permissions, so isolation must be opt-in with a clear warning. Codex/OpenCode suppression keys are unverified. Char-based token estimates are about +/-30%. Window-relative budgets could enlarge briefings on 1M models (cost, cache churn), hence the clamps. Antigravity's global-config mutation cannot be fixed from Deck.

## 5. Phased plan

1. **Correctness (no new config):** clear window on model switch (`session/modes.go:252`, `manager.go:1564`); cap `thread.read` with `hub.Cap`; stop repeated instructions on OpenCode/Antigravity resume by persisting `briefed` (`opencode/adapter.go:196`, `antigravity/antigravity.go:149`); translate Claude `compact_boundary` and Codex `thread/compacted` into `KindCompacted`.
2. **Reporting:** `Usage.ContextSource`/`BaselineTokens`; Claude `fallback` marker (`translate.go:92-97`); Antigravity estimate (`antigravity/translate.go:304-313`); `ModelInfo.ContextWindow`; per-adapter `ContextCaps`; decode Codex `instructionSources`; UI source markers (`sidebar.go:57`, `marks.go:51`, `context_fill.go:39`).
3. **Budgets:** window-relative constants and `context` config block.
4. **Policy knobs:** `Spec.Context` wired into Claude `args()` (`claude.go:232`), Codex `mcpThreadConfig` (`codex/mcp.go:47`), OpenCode `childEnv` (`opencode/adapter.go:72`); `Compactor` -> `AgentSummarize`; settings UI.
5. **Breakdown meter:** track Deck-injected tokens in `buildPrompt` and `steerNow`; stacked meter in `pane_usage.go`.
