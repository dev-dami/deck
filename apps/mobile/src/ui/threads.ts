import type { InboxItem, ProjectRef } from "../../../../pkg/remote/web/src/protocol3";
import { compareNewest, toMillis } from "../../../../pkg/remote/web/src/time";

// The desktop sidebar's view of a thread (desktop-shell/src/renderer/threads.ts):
// a status a person may need to act on, else just the time.
export type ThreadStatus = "approval" | "working" | "done" | null;

export function threadStatus(item: InboxItem): ThreadStatus {
  if (item.approvals > 0 || item.state === "needs_you") return "approval";
  if (item.state === "running") return "working";
  if (item.unread) return "done";
  return null;
}

// A thread settles when nothing has happened in it for this long.
export const SETTLE_AFTER_MS = 3 * 24 * 60 * 60 * 1000;

export interface ProjectGroup {
  id: string;
  name: string;
  online: boolean;
  live: InboxItem[];
  settled: InboxItem[];
  needsYou: number;
}

const newest = (a: InboxItem, b: InboxItem): number =>
  compareNewest(a.last_activity, a.session_id, b.last_activity, b.session_id);

// Threads under their project folders. A project's live threads come first and
// the settled ones sit behind a fold; a thread that works, waits or has news
// never settles. Projects that need the person come first, then the most
// recently active, and projects with no threads last so a new one can start.
export function groupByProject(
  inbox: InboxItem[],
  projects: ProjectRef[],
  now: number,
): ProjectGroup[] {
  const groups = new Map<string, ProjectGroup>();
  for (const project of projects) {
    groups.set(project.id, {
      id: project.id,
      name: project.name,
      online: project.online,
      live: [],
      settled: [],
      needsYou: 0,
    });
  }
  for (const item of inbox) {
    let group = groups.get(item.pj);
    if (group === undefined) {
      group = {
        id: item.pj,
        name: "Project",
        online: false,
        live: [],
        settled: [],
        needsYou: 0,
      };
      groups.set(item.pj, group);
    }
    const status = threadStatus(item);
    if (status === "approval") group.needsYou++;
    const quiet = now - toMillis(item.last_activity) > SETTLE_AFTER_MS;
    (quiet && status === null ? group.settled : group.live).push(item);
  }
  const list = [...groups.values()];
  for (const group of list) {
    group.live.sort(newest);
    group.settled.sort(newest);
  }
  const latest = (g: ProjectGroup): number =>
    Math.max(0, ...[...g.live, ...g.settled].map((i) => toMillis(i.last_activity)));
  return list.sort(
    (a, b) =>
      Number(b.needsYou > 0) - Number(a.needsYou > 0) ||
      latest(b) - latest(a) ||
      a.name.localeCompare(b.name),
  );
}
