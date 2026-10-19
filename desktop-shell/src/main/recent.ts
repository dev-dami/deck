import { mkdirSync, readFileSync, renameSync, writeFileSync } from "node:fs";
import { dirname } from "node:path";

const MAX = 8;

// The folders opened last, newest first. Paths are not secret; the file
// lives beside servers.json. file is null for a list in memory only.
export class RecentFolders {
  private dirs: string[] = [];

  constructor(private readonly file: string | null) {
    if (file === null) return;
    try {
      const v: unknown = JSON.parse(readFileSync(file, "utf8"));
      if (Array.isArray(v)) this.dirs = v.filter((d): d is string => typeof d === "string").slice(0, MAX);
    } catch {
      // No list yet.
    }
  }

  list(): string[] {
    return [...this.dirs];
  }

  add(dir: string): void {
    this.dirs = [dir, ...this.dirs.filter((d) => d !== dir)].slice(0, MAX);
    if (this.file === null) return;
    mkdirSync(dirname(this.file), { recursive: true, mode: 0o700 });
    const tmp = `${this.file}.tmp`;
    writeFileSync(tmp, `${JSON.stringify(this.dirs, null, 2)}\n`, { mode: 0o600 });
    renameSync(tmp, this.file);
  }
}
