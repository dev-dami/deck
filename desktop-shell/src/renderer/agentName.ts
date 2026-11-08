// How an agent's name reads on screen. Deck addresses an agent by its id
// ("claude", "claude2", or "claude-1" from an older Deck and the fixtures),
// and the id stays what @mentions, lanes and the engine use. The window
// shows the agent as people say it: "Claude", and "Claude 2" for the
// second of its kind. A named profile ("charles") keeps its own name, and
// a subagent its own part after the slash.

const KINDS: Record<string, string> = {
  claude: "Claude",
  codex: "Codex",
  opencode: "OpenCode",
  antigravity: "Antigravity",
  pi: "Pi",
  gemini: "Gemini",
};

const NUMBERED = /^([a-z]+)-?(\d+)$/i;

export function agentName(id: string): string {
  if (id === "") return id;
  const slash = id.indexOf("/");
  if (slash > 0) return `${agentName(id.slice(0, slash))}/${id.slice(slash + 1)}`;
  const plain = KINDS[id.toLowerCase()];
  if (plain !== undefined) return plain;
  const m = NUMBERED.exec(id);
  const label = m === null ? undefined : KINDS[(m[1] as string).toLowerCase()];
  if (m === null || label === undefined) return id;
  const n = Number(m[2]);
  return n <= 1 ? label : `${label} ${n}`;
}

// The same, with an @ for where the window says who was addressed.
export function atName(id: string): string {
  return `@${agentName(id)}`;
}
