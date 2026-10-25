import type { ChangedFile } from "@web/protocol";

// The paths whose change differs between two lists of changed files: new
// ones, gone ones, and ones whose added or removed counts moved. Sorted.
export function changedSince(before: readonly ChangedFile[], after: readonly ChangedFile[]): string[] {
  const was = new Map(before.map((f) => [f.path, f]));
  const out = new Set<string>();
  for (const f of after) {
    const b = was.get(f.path);
    if (b === undefined || b.added !== f.added || b.removed !== f.removed) out.add(f.path);
    was.delete(f.path);
  }
  for (const path of was.keys()) out.add(path);
  return [...out].sort();
}

export function takeoverNotice(n: number): string {
  return `You changed ${n} file${n === 1 ? "" : "s"} since the stop`;
}

// What "Continue with my changes" sends to the agent that was stopped.
export function takeoverText(agent: string | null, paths: readonly string[]): string {
  const to = agent === null ? "" : `$${agent} `;
  return `${to}Continue. I changed: ${paths.join(", ")}`;
}
