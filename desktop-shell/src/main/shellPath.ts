import { execFile } from "node:child_process";
import { homedir } from "node:os";
import { delimiter, join } from "node:path";

// An app started from the Dock, Finder or a desktop launcher gets a bare
// PATH (/usr/bin:/bin), without the folders the agent CLIs live in. Like
// Claude Desktop, the app asks the user's login shell for its PATH and
// adds the usual install folders, so the engine finds claude, codex and
// opencode as a terminal would.

const MARK = "__DECK_PATH__";
const TIMEOUT_MS = 4000;

// The usual places the agent CLIs install to.
export function wellKnownDirs(home: string = homedir()): string[] {
  return [
    join(home, ".local", "bin"),
    join(home, ".claude", "local"),
    join(home, ".npm-global", "bin"),
    join(home, ".bun", "bin"),
    join(home, ".opencode", "bin"),
    join(home, ".cargo", "bin"),
    join(home, "go", "bin"),
    "/opt/homebrew/bin",
    "/usr/local/bin",
    "/usr/bin",
    "/bin",
  ];
}

// Joins PATH lists, keeping the first place each folder appears.
export function mergePath(...lists: (string | undefined)[]): string {
  const seen = new Set<string>();
  const out: string[] = [];
  for (const l of lists) {
    for (const dir of (l ?? "").split(delimiter)) {
      if (dir === "" || seen.has(dir)) continue;
      seen.add(dir);
      out.push(dir);
    }
  }
  return out.join(delimiter);
}

// The PATH between the markers in a shell's output (rc files may print
// around it), or null.
export function pathFromOutput(out: string): string | null {
  const a = out.indexOf(MARK);
  const b = out.indexOf(MARK, a + MARK.length);
  if (a < 0 || b < 0) return null;
  const p = out.slice(a + MARK.length, b).trim();
  return p === "" ? null : p;
}

// The login shell's PATH, or null when there is none to ask (Windows) or
// it does not answer in time.
export function loginShellPath(env: NodeJS.ProcessEnv = process.env): Promise<string | null> {
  if (process.platform === "win32") return Promise.resolve(null);
  const shell = env["SHELL"] || "/bin/sh";
  return new Promise((resolve) => {
    execFile(
      shell,
      ["-ilc", `printf '${MARK}%s${MARK}' "$PATH"`],
      { timeout: TIMEOUT_MS, env: { ...env, DISABLE_AUTO_UPDATE: "true" } },
      (err, stdout) => resolve(err === null || stdout !== "" ? pathFromOutput(String(stdout)) : null),
    );
  });
}

// The PATH the engine runs with: the app's own, the login shell's, then
// the well-known folders.
export async function enginePath(env: NodeJS.ProcessEnv = process.env): Promise<string> {
  return mergePath(env["PATH"], (await loginShellPath(env)) ?? undefined, wellKnownDirs().join(delimiter));
}
