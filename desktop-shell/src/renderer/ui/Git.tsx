import { relativeTime } from "@web/time";
import {
  ArrowDown,
  ArrowUp,
  Check,
  ChevronDown,
  CircleDot,
  ExternalLink,
  GitBranch,
  GitCommitHorizontal,
  GitMerge,
  GitPullRequest,
  Loader2,
  Plus,
  RefreshCw,
  Upload,
} from "./icons";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { cn } from "../lib/cn";
import type { GitStatus, ProjectApi, PullRequest } from "../project";
import { Button } from "./kit/button";
import { Dialog, fieldClass } from "./kit/dialog";
import { MenuItem, MenuLabel, MenuSeparator, Popover } from "./kit/menu";

// The project's work tree, refreshed every so often, after each action and
// when refresh() is called (for instance when the agents stop).
export function useGit(project: ProjectApi | undefined) {
  const [status, setStatus] = useState<GitStatus | null>(null);
  const [working, setWorking] = useState<string | null>(null);
  const live = useRef(true);
  const refresh = useCallback(async () => {
    if (project === undefined) return;
    const r = await project.gitStatus();
    if (live.current && typeof r !== "string") setStatus(r);
  }, [project]);
  useEffect(() => {
    live.current = true;
    void refresh();
    const t = setInterval(() => void refresh(), 15_000);
    const focus = () => void refresh();
    window.addEventListener("focus", focus);
    return () => {
      live.current = false;
      clearInterval(t);
      window.removeEventListener("focus", focus);
    };
  }, [refresh]);
  // Runs one action with a label to show while it works; its error, or null.
  const run = useCallback(
    async (label: string, f: () => Promise<string | null>): Promise<string | null> => {
      setWorking(label);
      const err = await f();
      setWorking(null);
      await refresh();
      return err;
    },
    [refresh],
  );
  return { status, working, refresh, run };
}

export type Git = ReturnType<typeof useGit>;

// The branch control of the composer's context strip, where T3 Code shows
// the thread's branch: the branch, how it stands against its upstream, and
// a list to switch to or create another.
export function BranchPicker({ git, project, onError }: { git: Git; project: ProjectApi; onError: (t: string) => void }) {
  const [open, setOpen] = useState(false);
  const [q, setQ] = useState("");
  const st = git.status;
  const shown = useMemo(
    () => (st?.branches ?? []).filter((b) => b.name.toLowerCase().includes(q.trim().toLowerCase())),
    [st, q],
  );
  if (st === null || !st.repo) return null;
  const now = Date.now();
  const exact = shown.some((b) => b.name === q.trim());
  const go = async (branch: string, create: boolean) => {
    setOpen(false);
    setQ("");
    const err = await git.run(create ? "Creating branch" : "Switching branch", () =>
      project.git("GitCheckout", { branch, create }),
    );
    if (err !== null) onError(err);
  };
  return (
    <Popover
      open={open}
      onClose={() => setOpen(false)}
      side="top"
      align="end"
      label="Branches"
      role="dialog"
      className="w-80 p-0"
      trigger={
        <button
          type="button"
          className="branch-picker flex h-6 min-w-0 max-w-64 cursor-pointer items-center gap-1.5 rounded-full px-2 text-muted-foreground text-xs tabular-nums outline-none transition-colors hover:bg-hover hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring aria-expanded:bg-selected aria-expanded:text-foreground"
          aria-expanded={open}
          aria-label={`Branch ${st.branch || "detached"}`}
          title={st.upstream === "" ? "Not pushed yet" : `Tracks ${st.upstream}`}
          onClick={() => setOpen((o) => !o)}
        >
          {git.working !== null ? <Loader2 className="size-3.5 shrink-0 animate-spin" /> : <GitBranch className="size-3.5 shrink-0" />}
          <span className="truncate">{st.detached ? `detached ${st.head.slice(0, 7)}` : st.branch}</span>
          {st.dirty > 0 && <CircleDot className="size-3 shrink-0 text-warning-foreground" aria-label={`${st.dirty} changed files`} />}
          {st.ahead > 0 && (
            <span className="flex shrink-0 items-center text-2xs">
              <ArrowUp className="size-3" />
              {st.ahead}
            </span>
          )}
          {st.behind > 0 && (
            <span className="flex shrink-0 items-center text-2xs">
              <ArrowDown className="size-3" />
              {st.behind}
            </span>
          )}
          <ChevronDown className="size-3 shrink-0" />
        </button>
      }
    >
      <div className="border-hairline border-b p-1.5">
        <input
          autoFocus
          aria-label="Find or create a branch"
          placeholder="Find or create a branch…"
          value={q}
          className="h-8 w-full rounded-[9px] bg-transparent px-2 text-sm outline-none transition-shadow placeholder:text-faint focus-visible:ring-2 focus-visible:ring-ring"
          onChange={(e) => setQ(e.target.value)}
          onKeyDown={(e) => {
            if (e.key !== "Enter") return;
            const name = q.trim();
            if (name === "") return;
            void go(name, !st.branches.some((b) => b.name === name));
          }}
        />
      </div>
      <div className="max-h-72 overflow-y-auto p-1">
        {q.trim() !== "" && !exact && (
          <MenuItem onClick={() => void go(q.trim(), true)}>
            <Plus /> Create branch <span className="font-mono text-xs">{q.trim()}</span>
          </MenuItem>
        )}
        {shown.map((b) => (
          <MenuItem key={b.name} className="items-start" onClick={() => void (b.name === st.branch ? setOpen(false) : go(b.name, false))}>
            <GitBranch className="mt-0.5" />
            <span className="flex min-w-0 flex-1 flex-col">
              <span className="truncate font-mono text-xs">{b.name}</span>
              <span className="truncate text-2xs text-muted-foreground">
                {b.subject} · {relativeTime(new Date(b.committedAt).toISOString(), now)}
              </span>
            </span>
            {b.name === st.branch && <Check className="mt-0.5" />}
          </MenuItem>
        ))}
        {shown.length === 0 && q.trim() === "" && <p className="px-2 py-2 text-muted-foreground text-xs">No branches.</p>}
      </div>
    </Popover>
  );
}

function checksState(pr: PullRequest): { label: string; className: string } | null {
  if (pr.checks.length === 0) return null;
  if (pr.checks.some((c) => c.bucket === "fail" || c.bucket === "cancel")) return { label: "Checks failed", className: "text-destructive-foreground" };
  if (pr.checks.some((c) => c.bucket === "pending")) return { label: "Checks running", className: "text-warning-foreground" };
  return { label: "Checks passed", className: "text-success-foreground" };
}

function CommitDialog({ dirty, onCommit, onClose }: { dirty: number; onCommit: (msg: string, push: boolean) => void; onClose: () => void }) {
  const [msg, setMsg] = useState("");
  const ok = msg.trim() !== "";
  return (
    <Dialog
      title="Commit changes"
      description={`Stages all ${dirty} changed ${dirty === 1 ? "file" : "files"} and commits them.`}
      onClose={onClose}
      footer={
        <>
          <Button variant="outline" size="sm" disabled={!ok} onClick={() => onCommit(msg, true)}>
            Commit and push
          </Button>
          <Button size="sm" disabled={!ok} onClick={() => onCommit(msg, false)}>
            Commit
          </Button>
        </>
      }
    >
      <textarea
        autoFocus
        aria-label="Commit message"
        placeholder="Describe the change"
        rows={4}
        value={msg}
        className={cn(fieldClass, "resize-none")}
        onChange={(e) => setMsg(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter" && (e.metaKey || e.ctrlKey) && ok) onCommit(msg, false);
        }}
      />
    </Dialog>
  );
}

function PRDialog({
  st,
  initialTitle,
  onCreate,
  onClose,
}: {
  st: GitStatus;
  initialTitle: string;
  onCreate: (title: string, body: string, draft: boolean) => void;
  onClose: () => void;
}) {
  const [title, setTitle] = useState(initialTitle);
  const [body, setBody] = useState("");
  const ok = title.trim() !== "";
  return (
    <Dialog
      title="Create pull request"
      description={
        <>
          From <span className="font-mono">{st.branch}</span> into <span className="font-mono">{st.defaultBranch || "main"}</span>
          {st.ahead > 0 || st.upstream === "" ? ". The branch is pushed first." : "."}
        </>
      }
      onClose={onClose}
      footer={
        <>
          <Button variant="outline" size="sm" disabled={!ok} onClick={() => onCreate(title, body, true)}>
            Create draft
          </Button>
          <Button size="sm" disabled={!ok} onClick={() => onCreate(title, body, false)}>
            Create pull request
          </Button>
        </>
      }
    >
      <input autoFocus aria-label="Title" placeholder="Title" value={title} className={fieldClass} onChange={(e) => setTitle(e.target.value)} />
      <textarea
        aria-label="Description"
        placeholder="What changed and why"
        rows={6}
        value={body}
        className={cn(fieldClass, "resize-none")}
        onChange={(e) => setBody(e.target.value)}
      />
    </Dialog>
  );
}

// The header's git control, after T3 Code's GitActionsControl: the next
// step as the main button (commit, push, open a pull request or view it)
// and every action in its menu.
export function GitActions({
  git,
  project,
  threadTitle,
  onError,
  onInfo,
}: {
  git: Git;
  project: ProjectApi;
  threadTitle: string;
  onError: (t: string) => void;
  onInfo: (t: string) => void;
}) {
  const [menu, setMenu] = useState(false);
  const [dialog, setDialog] = useState<"commit" | "pr" | null>(null);
  const st = git.status;
  if (st === null || !st.repo) return null;
  const pr = st.pullRequest;
  const prOpen = pr !== null && pr.state === "OPEN";
  const busy = git.working !== null;

  const act = async (label: string, f: () => Promise<string | null>, done?: string) => {
    const err = await git.run(label, f);
    if (err !== null) onError(err);
    else if (done !== undefined) onInfo(done);
  };
  const push = () => act("Pushing", () => project.git("GitPush"), "Pushed.");
  const pull = () => act("Pulling", () => project.git("GitPull"), "Up to date.");
  const commit = async (msg: string, andPush: boolean) => {
    setDialog(null);
    const err = await git.run("Committing", () => project.git("GitCommit", { message: msg }));
    if (err !== null) return onError(err);
    if (andPush) await push();
    else onInfo("Committed.");
  };
  const createPR = async (title: string, body: string, draft: boolean) => {
    setDialog(null);
    await git.run("Opening pull request", async () => {
      if (st.ahead > 0 || st.upstream === "") {
        const err = await project.git("GitPush");
        if (err !== null) {
          onError(err);
          return null;
        }
      }
      const r = await project.createPR(title, body, st.defaultBranch, draft);
      if (typeof r === "string") onError(r);
      else onInfo(`Opened pull request #${r.number}.`);
      return null;
    });
  };

  const next: { label: string; icon: React.ReactNode; run: () => void } | null =
    st.dirty > 0
      ? { label: "Commit", icon: <GitCommitHorizontal />, run: () => setDialog("commit") }
      : st.ahead > 0 || (st.upstream === "" && st.branch !== "")
        ? { label: "Push", icon: <Upload />, run: () => void push() }
        : prOpen
          ? null
          : st.gh && st.branch !== "" && st.branch !== st.defaultBranch
            ? { label: "Create PR", icon: <GitPullRequest />, run: () => setDialog("pr") }
            : null;
  const checks = pr === null ? null : checksState(pr);

  return (
    <>
      {/* It sits in a capsule of the toolbar: the pull request, then the next
          step and the menu as one split button of borderless halves. */}
      <div className="git-actions relative flex items-center gap-0.5">
        {pr !== null && (
          <a
            href={pr.url}
            target="_blank"
            rel="noopener noreferrer"
            className="flex h-7 items-center gap-1 rounded-full px-2 text-xs tabular-nums outline-none transition-colors hover:bg-hover focus-visible:ring-2 focus-visible:ring-ring"
            title={`${pr.title}${checks === null ? "" : ` · ${checks.label}`}`}
          >
            {pr.state === "MERGED" ? <GitMerge className="size-3.5 text-[#a371f7]" /> : <GitPullRequest className={cn("size-3.5", checks?.className ?? "text-success-foreground")} />}
            #{pr.number}
          </a>
        )}
        {pr !== null && <span aria-hidden className="h-4 w-px bg-hairline-strong" />}
        <Popover
          open={menu}
          onClose={() => setMenu(false)}
          align="end"
          label="Git"
          className="w-60"
          trigger={
            <div className="flex items-center">
              {next !== null && (
                <Button variant="ghost" size="sm" className="gap-1.5 rounded-r-[6px] px-2.5 font-normal" disabled={busy} onClick={next.run}>
                  {busy ? <Loader2 className="animate-spin" /> : next.icon}
                  <span className="sr-only @3xl/top:not-sr-only">{busy ? git.working : next.label}</span>
                </Button>
              )}
              {next !== null && <span aria-hidden className="mx-px h-4 w-px bg-hairline-strong" />}
              <Button
                variant="ghost-muted"
                size="sm"
                className={cn("gap-1.5 font-normal", next !== null ? "rounded-l-[6px] px-1.5" : "px-2.5")}
                aria-label="Git actions"
                aria-expanded={menu}
                aria-pressed={menu}
                onClick={() => setMenu((m) => !m)}
              >
                {next === null && (busy ? <Loader2 className="animate-spin" /> : <GitBranch />)}
                {next === null && <span className="sr-only @3xl/top:not-sr-only">{busy ? git.working : "Git"}</span>}
                <ChevronDown className="size-3" />
              </Button>
            </div>
          }
        >
          <MenuLabel>
            {st.branch || "detached"}
            {st.upstream !== "" ? ` → ${st.upstream}` : " · not pushed"}
          </MenuLabel>
          <MenuItem disabled={st.dirty === 0 || busy} onClick={() => (setMenu(false), setDialog("commit"))}>
            <GitCommitHorizontal /> Commit {st.dirty > 0 ? `${st.dirty} ${st.dirty === 1 ? "file" : "files"}…` : ""}
          </MenuItem>
          <MenuItem disabled={busy || st.branch === ""} onClick={() => (setMenu(false), void push())}>
            <Upload /> Push{st.ahead > 0 ? ` ${st.ahead} ${st.ahead === 1 ? "commit" : "commits"}` : ""}
          </MenuItem>
          <MenuItem disabled={busy || st.upstream === ""} onClick={() => (setMenu(false), void pull())}>
            <ArrowDown /> Pull{st.behind > 0 ? ` ${st.behind} ${st.behind === 1 ? "commit" : "commits"}` : ""}
          </MenuItem>
          <MenuItem onClick={() => (setMenu(false), void git.refresh())}>
            <RefreshCw /> Refresh
          </MenuItem>
          <MenuSeparator />
          {pr === null ? (
            <MenuItem disabled={!st.gh || busy || st.branch === "" || st.branch === st.defaultBranch} onClick={() => (setMenu(false), setDialog("pr"))}>
              <GitPullRequest /> Create pull request…
            </MenuItem>
          ) : (
            <>
              <MenuItem onClick={() => (setMenu(false), window.open(pr.url, "_blank", "noopener"))}>
                <ExternalLink /> Open #{pr.number} on GitHub
              </MenuItem>
              {prOpen && (
                <MenuItem
                  disabled={busy}
                  onClick={() => {
                    setMenu(false);
                    if (window.confirm(`Squash and merge #${pr.number}?`)) {
                      void act("Merging", () => project.git("GitMergePR", { method: "squash" }), `Merged #${pr.number}.`);
                    }
                  }}
                >
                  <GitMerge /> Squash and merge
                </MenuItem>
              )}
            </>
          )}
          {!st.gh && <p className="px-2 pt-1 pb-1.5 text-2xs text-muted-foreground">Install the GitHub CLI and run gh auth login for pull requests.</p>}
        </Popover>
      </div>
      {dialog === "commit" && <CommitDialog dirty={st.dirty} onCommit={(m, p) => void commit(m, p)} onClose={() => setDialog(null)} />}
      {dialog === "pr" && (
        <PRDialog st={st} initialTitle={threadTitle} onCreate={(t, b, d) => void createPR(t, b, d)} onClose={() => setDialog(null)} />
      )}
    </>
  );
}
