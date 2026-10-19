import type { ChangedFile, Decision, Session } from "@web/protocol";
import type { AppState } from "@web/state";
import { useEffect, useState } from "react";
import { approvalSummary, DECISIONS, groupApprovals } from "../approvals";
import type { DeckClient } from "../client";
import { PANELS, type Panel } from "../ui";
import { DiffView } from "./DiffView";

function Changes({ c, sessionId }: { c: DeckClient; sessionId: string }) {
  const [files, setFiles] = useState<ChangedFile[] | null>(null);
  const [open, setOpen] = useState<string | null>(null);
  const [tick, setTick] = useState(0);
  useEffect(() => {
    let live = true;
    setFiles(null);
    void c.changes(sessionId).then((f) => {
      if (live) setFiles(f ?? []);
    });
    return () => {
      live = false;
    };
  }, [c, sessionId, tick]);
  return (
    <section aria-label="Changes">
      <div className="panel-tools">
        <span className="muted">{files === null ? "Loading…" : `${files.length} files changed`}</span>
        <button type="button" onClick={() => setTick((t) => t + 1)}>
          Refresh
        </button>
      </div>
      {files?.map((f) => (
        <div key={f.path} className="file">
          <button type="button" className="file-head" onClick={() => setOpen(open === f.path ? null : f.path)}>
            <span className="file-path">{f.path}</span>
            <span className="add">+{f.added}</span>
            <span className="del">−{f.removed}</span>
          </button>
          {open === f.path && <DiffView diff={f.diff ?? ""} truncated={f.diff_truncated} />}
        </div>
      ))}
    </section>
  );
}

function Approvals({
  c,
  state,
  openId,
  onOpen,
}: {
  c: DeckClient;
  state: AppState;
  openId: string | null;
  onOpen: (id: string) => void;
}) {
  const groups = groupApprovals(state.approvals, state.sessions, openId);
  const answer = (sid: string, id: string, d: Decision) => void c.answerApproval(sid, id, d);
  if (groups.length === 0) return <p className="empty">No approvals wait for you.</p>;
  return (
    <section aria-label="Approvals">
      {groups.map((g) => (
        <div key={g.sessionId} className="approval-group">
          <button type="button" className="group-head" onClick={() => onOpen(g.sessionId)}>
            {g.title}
          </button>
          {g.items.map((a) => (
            <div key={a.id} className="approval" data-approval={a.id}>
              <div className="approval-head">
                <span className="approval-agent">{a.agent}</span>
                <span className="muted">{a.description}</span>
              </div>
              <pre className="approval-what">{approvalSummary(a)}</pre>
              {a.diff !== undefined && a.diff !== "" && <DiffView diff={a.diff} truncated={a.diff_truncated} />}
              <div className="approval-actions">
                {DECISIONS.map((d) => (
                  <button
                    key={d.decision}
                    type="button"
                    className={`decide decide-${d.decision}`}
                    onClick={() => answer(a.session_id, a.id, d.decision)}
                  >
                    {d.label}
                  </button>
                ))}
              </div>
            </div>
          ))}
        </div>
      ))}
    </section>
  );
}

function Queue({ c, state, sessionId }: { c: DeckClient; state: AppState; sessionId: string }) {
  const items = state.queue[sessionId] ?? [];
  if (items.length === 0) return <p className="empty">Nothing waits in the queue.</p>;
  return (
    <section aria-label="Queue">
      {items.map((q) => (
        <div key={q.id} className="queued">
          <div className="approval-head">
            <span className="approval-agent">{q.agent}</span>
          </div>
          <p>{q.text}</p>
          <div className="approval-actions">
            <button type="button" onClick={() => void c.steer(sessionId, q.id)}>
              Send now
            </button>
            <button type="button" onClick={() => void c.cancelQueued(sessionId, q.id)}>
              Cancel
            </button>
          </div>
        </div>
      ))}
    </section>
  );
}

export function RightPanel({
  c,
  state,
  panel,
  open,
  onClose,
  onOpen,
}: {
  c: DeckClient;
  state: AppState;
  panel: Panel;
  open: Session | undefined;
  onClose: () => void;
  onOpen: (id: string) => void;
}) {
  return (
    <aside className="panel" aria-label="Side panel">
      <header className="panel-head">
        <h2>{PANELS.find((p) => p.panel === panel)?.label}</h2>
        <button type="button" className="link-button" aria-label="Close the panel" onClick={onClose}>
          ×
        </button>
      </header>
      <div className="panel-body">
        {panel === "approvals" && <Approvals c={c} state={state} openId={open?.id ?? null} onOpen={onOpen} />}
        {panel === "changes" &&
          (open === undefined ? <p className="empty">Open a session to see its changes.</p> : <Changes c={c} sessionId={open.id} />)}
        {panel === "queue" &&
          (open === undefined ? <p className="empty">Open a session to see its queue.</p> : <Queue c={c} state={state} sessionId={open.id} />)}
      </div>
    </aside>
  );
}
