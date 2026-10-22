import type { Part } from "../../../../pkg/remote/web/src/protocol";
import type { InboxItem } from "../../../../pkg/remote/web/src/protocol3";

export type TaskTab = "needs" | "running" | "done";
export type BadgeTone = "warn" | "info" | "muted";

export interface TaskRow {
  key: string;
  pj: string;
  sessionId: string;
  title: string;
  subtitle: string;
  badge: { text: string; tone: BadgeTone } | null;
  unread: boolean;
}

const STATE: Record<TaskTab, InboxItem["state"]> = {
  needs: "needs_you",
  running: "running",
  done: "done",
};

export function tabCounts(items: readonly InboxItem[]): Record<TaskTab, number> {
  return {
    needs: items.filter((i) => i.state === "needs_you").length,
    running: items.filter((i) => i.state === "running").length,
    done: items.filter((i) => i.state === "done").length,
  };
}

// The tab a computer opens on: whatever needs an answer, else what is moving.
export function defaultTab(items: readonly InboxItem[]): TaskTab {
  const counts = tabCounts(items);
  if (counts.needs > 0) return "needs";
  if (counts.running > 0) return "running";
  return "done";
}

export function relativeTime(iso: string, nowMs: number): string {
  const at = Date.parse(iso);
  if (Number.isNaN(at)) return "";
  const minutes = Math.floor((nowMs - at) / 60_000);
  if (minutes < 1) return "just now";
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h ago`;
  const days = Math.floor(hours / 24);
  if (days < 7) return `${days}d ago`;
  const date = new Date(at);
  return date.toLocaleDateString([], { day: "numeric", month: "short" });
}

export function taskRows(
  items: readonly InboxItem[],
  projects: ReadonlyArray<{ id: string; name: string }>,
  tab: TaskTab,
  nowMs: number,
): TaskRow[] {
  return items
    .filter((item) => item.state === STATE[tab])
    .sort((a, b) => b.last_activity.localeCompare(a.last_activity))
    .map((item) => {
      const project = projects.find((p) => p.id === item.pj)?.name ?? "Project";
      const when = relativeTime(item.last_activity, nowMs);
      const detail = item.summary.trim() || when;
      let badge: TaskRow["badge"] = null;
      if (item.state === "needs_you")
        badge = {
          text: item.approvals > 1 ? `${item.approvals} approvals` : "Approve",
          tone: "warn",
        };
      else if (item.state === "running") badge = { text: "Running", tone: "info" };
      return {
        key: `${item.pj}:${item.session_id}`,
        pj: item.pj,
        sessionId: item.session_id,
        title: item.title.trim() || "Untitled task",
        subtitle: detail ? `${project} · ${detail}` : project,
        badge,
        unread: item.unread,
      };
    });
}

export type DiffKind = "add" | "del" | "hunk" | "meta" | "ctx";

export interface DiffLine {
  kind: DiffKind;
  text: string;
}

export function diffLines(diff: string): DiffLine[] {
  const out: DiffLine[] = [];
  for (const raw of diff.replace(/\r\n/g, "\n").split("\n")) {
    const line = raw.replace(/\t/g, "    ");
    if (line.startsWith("@@")) out.push({ kind: "hunk", text: line });
    else if (line.startsWith("+++") || line.startsWith("---") || line.startsWith("diff "))
      out.push({ kind: "meta", text: line });
    else if (line.startsWith("+")) out.push({ kind: "add", text: line.slice(1) });
    else if (line.startsWith("-")) out.push({ kind: "del", text: line.slice(1) });
    else out.push({ kind: "ctx", text: line.startsWith(" ") ? line.slice(1) : line });
  }
  while (out.length > 0 && out[out.length - 1]?.text === "" && out[out.length - 1]?.kind === "ctx")
    out.pop();
  return out;
}

export function splitPath(path: string): { name: string; dir: string } {
  const at = path.lastIndexOf("/");
  return at < 0 ? { name: path, dir: "" } : { name: path.slice(at + 1), dir: path.slice(0, at) };
}

export interface Step {
  key: string;
  label: string;
  detail: string;
  error: boolean;
}

// An agent message is prose plus the work it did on the way. The prose is
// what people read; the work folds into one line they can open.
export function splitMessage(parts: readonly Part[]): { prose: string[]; steps: Step[] } {
  const prose: string[] = [];
  const steps: Step[] = [];
  parts.forEach((part, index) => {
    if (part.type === "text" && part.text !== undefined) {
      if (part.text.trim()) prose.push(part.text);
      return;
    }
    if (part.type === "reasoning" || part.type === "thinking") return;
    const detail = (part.summary ?? part.output ?? part.diff ?? part.text ?? "")
      .split("\n")
      .find((line) => line.trim())
      ?.trim();
    steps.push({
      key: `${part.type}:${part.id ?? part.tool_call_id ?? index}`,
      label: part.name ?? (part.image ? "Attachment" : part.type),
      detail: part.image ? part.image.name : (detail ?? ""),
      error: part.is_error === true,
    });
  });
  return { prose, steps };
}
