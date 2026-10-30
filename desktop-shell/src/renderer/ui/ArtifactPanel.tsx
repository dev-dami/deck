import { useEffect, useRef, useState } from "react";
import type { ArtifactBuildInfo, ArtifactDiagnostic, ArtifactRevisionFiles, ArtifactRevisionInfo, ArtifactSummary } from "../../shared/artifacts";
import type { DeckBridge } from "../../shared/bridge";
import { sourceDownload } from "../artifactExport";
import { selectArtifact, useSelectedArtifact } from "../artifactSelection";
import { conflictText, entryText, fileName, isolated, MAX_RUNTIME_REPORTS, revisionLabel, runtimeDiagnostic } from "../artifacts";
import type { DeckClient } from "../client";
import { cn } from "../lib/cn";
import { useRoomLive } from "../live";
import { Copy, Package, RotateCcw } from "./icons";
import { Button } from "./kit/button";
import { EmptyState } from "./kit/empty";
import { Markdown } from "./Markdown";
import { covered } from "./PreviewPanel";

// The Artifacts tab: documents and small apps the agents saved in the room.
// Markdown is drawn here; HTML, SVG and React run in the isolated view that
// main lays over the preview box. Every edit is a saved revision, and the
// person's only edit is Restore.

type View = "preview" | "source" | "errors";

// The diagnostics of the newest build or runtime report of a revision.
function problems(builds: readonly ArtifactBuildInfo[]): ArtifactDiagnostic[] {
  return builds.filter((b) => !b.ok).flatMap((b) => b.diagnostics);
}

function Preview({
  bridge,
  files,
  summary,
  onResult,
}: {
  bridge: DeckBridge;
  files: ArtifactRevisionFiles;
  summary: ArtifactSummary;
  onResult: (phase: "build" | "runtime", ok: boolean, diagnostics: ArtifactDiagnostic[]) => void;
}) {
  // One show per mount, named so that main's events say which show they are
  // from: the page of an earlier artifact or revision never counts for this one.
  const request = useRef(crypto.randomUUID()).current;
  const box = useRef<HTMLDivElement>(null);
  const kind = summary.kind;
  const entry = files.info.entrypoint;
  const report = useRef(onResult);
  report.current = onResult;

  // Builds and shows this revision; a newer one overtakes it.
  useEffect(() => {
    if (!isolated(kind)) return;
    let live = true;
    void bridge.artifactShow({ request, kind, entrypoint: entry, files: files.files }).then((r) => {
      if (!live) return;
      // An empty list means a later show took over.
      if (!r.ok && r.diagnostics.length === 0) return;
      report.current("build", r.ok, r.ok ? [] : r.diagnostics);
    });
    return () => {
      live = false;
    };
  }, [bridge, request, kind, entry, files]);

  // Page errors are main's to see; the first few go to the engine.
  useEffect(() => {
    let sent = 0;
    return bridge.onArtifact((s) => {
      if (s.request !== request || s.kind === "loaded" || sent >= MAX_RUNTIME_REPORTS) return;
      sent += 1;
      report.current("runtime", false, [runtimeDiagnostic(s.message)]);
    });
  }, [bridge, request]);

  // Closing the view with the tab, not just hiding it, ends the page and any
  // build still running for it.
  useEffect(() => () => void bridge.artifactClose(), [bridge]);

  // Lays the view over the box, at most once a frame, and hides it when the
  // box is covered or gone.
  useEffect(() => {
    const el = box.current;
    if (el === null) return;
    let raf = 0;
    let last = "";
    const place = () => {
      raf = 0;
      const r = el.getBoundingClientRect();
      const hide = r.width < 1 || r.height < 1 || covered(r);
      const key = hide ? "hidden" : `${r.x},${r.y},${r.width},${r.height}`;
      if (key === last) return;
      last = key;
      if (hide) void bridge.artifactHide();
      else void bridge.artifactBounds({ x: r.x, y: r.y, w: r.width, h: r.height, zoom: 1 });
    };
    const later = () => {
      if (raf === 0) raf = requestAnimationFrame(place);
    };
    const ro = new ResizeObserver(later);
    ro.observe(el);
    const mo = new MutationObserver(later);
    mo.observe(document.body, { childList: true, subtree: true });
    window.addEventListener("resize", later);
    later();
    return () => {
      ro.disconnect();
      mo.disconnect();
      window.removeEventListener("resize", later);
      cancelAnimationFrame(raf);
    };
  }, [bridge]);

  return <div ref={box} className="min-h-0 flex-1" data-artifact-box="" />;
}

export function ArtifactPanel({
  c,
  bridge,
  sid,
  onError,
  onInfo,
}: {
  c: DeckClient;
  bridge: DeckBridge;
  sid: string;
  onError: (t: string) => void;
  onInfo: (t: string) => void;
}) {
  const live = useRoomLive(c, sid);
  const list = live.artifacts;
  const pick = useSelectedArtifact(c, sid);
  const summary = list.find((a) => a.id === pick) ?? list[0] ?? null;
  const [pinned, setPinned] = useState<number | null>(null);
  const [view, setView] = useState<View>("preview");
  const [loaded, setLoaded] = useState<{ key: string; files: ArtifactRevisionFiles } | null>(null);
  const [revisions, setRevisions] = useState<ArtifactRevisionInfo[]>([]);
  const [builds, setBuilds] = useState<ArtifactBuildInfo[]>([]);
  const [shown, setShown] = useState<ArtifactDiagnostic[]>([]);
  const [source, setSource] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const reported = useRef("");

  const id = summary?.id ?? null;
  const current = summary?.current_revision ?? 0;
  const revision = pinned ?? current;
  const stateVersion = summary?.state_version ?? 0;
  // What is on show is named by room, artifact and revision. Files, build
  // reports and page errors count only for the key they were read for.
  const key = `${sid}/${id ?? ""}/${revision}`;
  const keyNow = useRef(key);
  keyNow.current = key;
  const files = loaded !== null && loaded.key === key ? loaded.files : null;

  // Another artifact starts at its latest.
  useEffect(() => {
    setPinned(null);
    setView("preview");
    setSource(null);
    setShown([]);
  }, [id]);

  // An agent's correction should compile immediately after an error.
  useEffect(() => { setView((v) => v === "errors" ? "preview" : v); }, [key]);

  // The files of the revision on show. A late answer for a revision the
  // person has left is dropped.
  useEffect(() => {
    setShown([]);
    setBuilds([]);
    if (id === null || revision === 0) {
      setLoaded(null);
      return;
    }
    let live = true;
    const mine = `${sid}/${id}/${revision}`;
    void c.artifactFiles?.(sid, id, revision).then((f) => {
      if (live) setLoaded(f === null ? null : { key: mine, files: f });
    });
    return () => {
      live = false;
    };
  }, [c, sid, id, revision]);

  useEffect(() => {
    if (id === null) return;
    let live = true;
    void c.artifactRevisions?.(sid, id).then((r) => {
      if (live && r !== null) setRevisions(r);
    });
    return () => {
      live = false;
    };
  }, [c, sid, id, current]);

  // What the engine holds of how this revision built, which agents read too.
  useEffect(() => {
    if (id === null || revision === 0) return;
    let live = true;
    const mine = `${sid}/${id}/${revision}`;
    void c.artifactBuilds?.(sid, id, revision).then((b) => {
      if (live && b !== null && keyNow.current === mine) setBuilds(b);
    });
    return () => {
      live = false;
    };
  }, [c, sid, id, revision, stateVersion]);

  if (summary === null) {
    return <EmptyState icon={Package} title="No artifacts yet." hint="Ask an agent to build a document, a diagram or a small app. It opens here." />;
  }

  const seenProblems = new Set<string>();
  const found = [...shown, ...problems(builds)].filter((d) => {
    const identity = JSON.stringify(d);
    if (seenProblems.has(identity)) return false;
    seenProblems.add(identity);
    return true;
  });
  const here = revisions.find((r) => r.revision === revision);
  const sourceFile = source ?? files?.info.entrypoint ?? "";
  const text = files === null ? "" : (files.files.find((f) => f.path === sourceFile)?.content ?? "");

  const restore = async () => {
    setBusy(true);
    const r = await c.artifactAction?.(sid, { action: "restore", id: summary.id, base_revision: current, restore_revision: revision });
    setBusy(false);
    if (r === undefined) return;
    if (r.ok) {
      setPinned(null);
      onInfo(`Restored r${revision} as a new revision.`);
    } else onError(r.conflict === null ? r.message : conflictText(r.conflict));
  };

  const copy = () => {
    void navigator.clipboard.writeText(text).then(
      () => onInfo(`Copied ${fileName(sourceFile)}.`),
      () => onError("Could not copy to the clipboard."),
    );
  };

  const download = () => {
    if (files === null) return;
    try {
      const item = sourceDownload(summary, files);
      const url = URL.createObjectURL(item.blob);
      const link = document.createElement("a");
      link.href = url; link.download = item.name;
      document.body.append(link); link.click(); link.remove();
      setTimeout(() => URL.revokeObjectURL(url), 1000);
    } catch (error) { onError(error instanceof Error ? error.message : "Could not export source."); }
  };

  const tabs: { v: View; label: string }[] = [
    { v: "preview", label: "Preview" },
    { v: "source", label: "Source" },
    ...(found.length > 0 ? [{ v: "errors" as const, label: `Errors (${found.length})` }] : []),
  ];

  return (
    <div className="flex min-h-0 flex-1 flex-col" data-artifact={summary.id}>
      <div className="flex shrink-0 flex-wrap items-center gap-2 px-3 py-2 text-xs">
        {list.length > 1 ? (
          <select
            aria-label="Artifact"
            className="max-w-[10rem] truncate rounded-full border bg-transparent px-2 py-1"
            value={summary.id}
            onChange={(e) => selectArtifact(c, sid, e.target.value)}
          >
            {list.map((a) => (
              <option key={a.id} value={a.id}>
                {a.title}
              </option>
            ))}
          </select>
        ) : (
          <span className="max-w-[10rem] truncate font-medium text-foreground">{here?.title || summary.title}</span>
        )}
        <select
          aria-label="Revision"
          className="rounded-full border bg-transparent px-2 py-1"
          value={revision}
          onChange={(e) => setPinned(Number(e.target.value) === current ? null : Number(e.target.value))}
        >
          {[...revisions].reverse().map((r) => (
            <option key={r.revision} value={r.revision}>
              {revisionLabel(r, current)}
            </option>
          ))}
          {revisions.length === 0 && <option value={revision}>{`r${revision}`}</option>}
        </select>
        {revision !== current && (
          <Button size="xs" variant="outline" disabled={busy} onClick={() => void restore()}>
            <RotateCcw aria-hidden /> Restore
          </Button>
        )}
        <div className="ml-auto flex items-center gap-1">
          <Button size="xs" variant="ghost-muted" disabled={files === null} onClick={download}>Download source</Button>
          {tabs.map((t) => (
            <Button key={t.v} size="xs" variant="ghost-muted" aria-pressed={view === t.v} onClick={() => setView(t.v)}>
              {t.label}
            </Button>
          ))}
        </div>
      </div>
      {files === null ? (
        <p className="px-3 py-6 text-center text-faint text-xs">Loading…</p>
      ) : view === "preview" ? (
        summary.kind === "markdown" ? (
          <div className="min-h-0 flex-1 overflow-y-auto px-4 pb-4">
            <Markdown text={entryText(files.files, files.info.entrypoint)} />
          </div>
        ) : (
          <Preview
            key={key}
            bridge={bridge}
            files={files}
            summary={summary}
            onResult={(phase, ok, diagnostics) => {
              // A late word from a page of another revision is not this one's.
              if (keyNow.current !== key) return;
              setShown(diagnostics);
              // A build that failed has no page to look at; a page that errors stays.
              if (!ok && phase === "build") setView((v) => (v === "preview" ? "errors" : v));
              // Showing the tab again must not report the same outcome twice.
              const outcome = `${key}:${phase}:${ok}`;
              if (reported.current === outcome && phase === "build") return;
              reported.current = outcome;
              void c.reportArtifactBuild?.(sid, summary.id, files.revision, phase, ok, diagnostics);
            }}
          />
        )
      ) : view === "source" ? (
        <div className="flex min-h-0 flex-1 flex-col">
          <div className="flex shrink-0 items-center gap-1 overflow-x-auto px-3 pb-1">
            {files.files.map((f) => (
              <Button key={f.path} size="xs" variant="ghost-muted" aria-pressed={f.path === sourceFile} onClick={() => setSource(f.path)}>
                {fileName(f.path)}
              </Button>
            ))}
            <Button size="xs" variant="ghost" className="ml-auto" onClick={copy}>
              <Copy aria-hidden /> Copy
            </Button>
          </div>
          <pre className={cn("min-h-0 flex-1 overflow-auto px-3 pb-3 font-mono text-xs leading-relaxed")}>{text}</pre>
        </div>
      ) : (
        <ul className="min-h-0 flex-1 space-y-2 overflow-y-auto px-3 pb-3 text-xs" data-artifact-errors="">
          {found.map((d, i) => (
            <li key={i} className="rounded-lg border px-3 py-2">
              {d.file !== "" && <p className="font-mono text-muted-foreground">{`${d.file}:${d.line}:${d.column}`}</p>}
              <p className="whitespace-pre-wrap text-destructive-foreground">{d.message}</p>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
