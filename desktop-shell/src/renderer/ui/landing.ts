import { raceMessage } from "../crew";

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
];

export interface StartCard {
  id: "one" | "split" | "race";
  title: string;
  line: string;
  // What the card puts at the start of the composer.
  prefix: string;
}

// The three starts of the new-thread screen, for the kinds the Deck found:
// claude when it is there, else the first kind.
export function startCards(kinds: string[]): StartCard[] {
  const one = kinds.length === 0 || kinds.includes("claude") ? "claude" : (kinds[0] as string);
  const other = kinds.includes("codex") && one !== "codex" ? "codex" : (kinds.find((k) => k !== one) ?? one);
  return [
    { id: "one", title: "One agent", line: `Ask ${one} to …`, prefix: `$${one} ` },
    { id: "split", title: "Split the work", line: "Two agents take halves and compare", prefix: `$${one} $${other}+ ` },
    { id: "race", title: "Race three", line: "Three agents try; keep the best", prefix: raceMessage({ [one]: 3 }, "") },
  ];
}

// The draft with a start card's prefix in place of the agents it named.
export function withStart(prefix: string, draft: string): string {
  return prefix + draft.replace(/^\s*(\$\S+\s+)*/, "");
}
