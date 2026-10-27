// A title for a thread started from its first message: the first line,
// without the agent mentions (and the forms Deck no longer reads, such as
// "$codex×3", which a message from before may hold).
export function titleFrom(text: string): string {
  const line = text.replace(/(^|\s)\$[\w-]+(?:[+*]|×\d+)?/g, " ").trim().split("\n")[0] ?? "";
  const t = line.replace(/\s+/g, " ").trim();
  return t.length > 60 ? `${t.slice(0, 57).trimEnd()}…` : t;
}
