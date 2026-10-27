// The agent CLIs Deck drives, as the Welcome shows them: the mark of each
// CLI (internal/ui/tone, Mark), how to install it and where to read more.
export interface AgentHelp {
  kind: string;
  name: string;
  mark: string;
  install: string | null;
  url: string | null;
}

export const AGENT_HELP: AgentHelp[] = [
  {
    kind: "claude",
    name: "Claude Code",
    mark: "✻",
    install: "npm install -g @anthropic-ai/claude-code",
    url: "https://docs.anthropic.com/en/docs/claude-code",
  },
  { kind: "codex", name: "Codex", mark: ">_", install: "npm install -g @openai/codex", url: "https://github.com/openai/codex" },
  { kind: "opencode", name: "OpenCode", mark: "▯", install: "npm install -g opencode-ai", url: "https://opencode.ai" },
  { kind: "antigravity", name: "Antigravity", mark: "◎", install: null, url: null },
  {
    kind: "pi",
    name: "Pi",
    mark: "π",
    // Pi speaks ACP through the pi-acp adapter, which runs pi.
    install: "npm install -g @earendil-works/pi-coding-agent pi-acp",
    url: "https://github.com/svkozak/pi-acp",
  },
];

export interface StartCard {
  id: "one" | "split" | "race";
  title: string;
  line: string;
  // What the card puts at the start of the composer.
  prefix: string;
}

// The three starts of the new-thread screen, for the kinds the Deck found:
// claude when it is there, else the first kind. A Deck with one kind splits
// the work by asking that agent, which starts helpers of its own. The race
// is /race: its builder picks the agents, of any kinds.
export function startCards(kinds: string[]): StartCard[] {
  const one = kinds.length === 0 || kinds.includes("claude") ? "claude" : (kinds[0] as string);
  const other = kinds.includes("codex") && one !== "codex" ? "codex" : (kinds.find((k) => k !== one) ?? one);
  const split =
    other === one
      ? { line: `${one} splits it with helpers`, prefix: `$${one} ` }
      : { line: "Two agents take halves and compare", prefix: `$${one} $${other} ` };
  return [
    { id: "one", title: "One agent", line: `Ask ${one} to …`, prefix: `$${one} ` },
    { id: "split", title: "Split the work", ...split },
    { id: "race", title: "Race agents", line: "Pick agents of any kind; keep the best", prefix: "/race " },
  ];
}

// The draft with a start card's prefix in place of the agents (or the
// /race) it began with.
export function withStart(prefix: string, draft: string): string {
  return prefix + draft.replace(/^\s*(\/race\s+)?([$@]\S+\s+)*/, "");
}
