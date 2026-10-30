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
