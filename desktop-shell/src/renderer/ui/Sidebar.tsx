import type { Session } from "@web/protocol";
import type { Link } from "@web/state";
import { sessionTitle } from "@web/state";
import { relativeTime } from "@web/time";
import { useState } from "react";

function Row({
  s,
  open,
  unread,
  now,
  onOpen,
  onRename,
  onDelete,
}: {
  s: Session;
  open: boolean;
  unread: boolean;
  now: number;
  onOpen: () => void;
  onRename: (title: string) => void;
  onDelete: () => void;
}) {
  const [editing, setEditing] = useState(false);
  const [confirm, setConfirm] = useState(false);
  if (editing) {
    return (
      <li className="session editing">
        <input
          aria-label="Session title"
          defaultValue={s.title}
          autoFocus
          onKeyDown={(e) => {
            if (e.key === "Enter") {
              const t = e.currentTarget.value.trim();
              if (t !== "") onRename(t);
              setEditing(false);
            } else if (e.key === "Escape") setEditing(false);
          }}
          onBlur={() => setEditing(false)}
        />
      </li>
    );
  }
  return (
    <li className={`session${open ? " open" : ""}`} data-session={s.id}>
      <button type="button" className="session-main" onClick={onOpen} aria-current={open ? "page" : undefined}>
        <span className="session-title">
          {s.busy && <span className="dot busy" title="Agents are working" />}
          {unread && <span className="dot unread" title="New messages" />}
          {sessionTitle(s)}
        </span>
        <span className="session-meta">
          {s.pending > 0 && (
            <span className="badge" title="Approvals waiting">
              {s.pending}
            </span>
          )}
          <span className="muted">{relativeTime(s.updated_at, now)}</span>
        </span>
      </button>
      {confirm ? (
        <span className="session-confirm">
          Delete?
          <button type="button" onClick={onDelete}>
            Yes
          </button>
          <button type="button" onClick={() => setConfirm(false)}>
            No
          </button>
        </span>
      ) : (
        <span className="session-tools">
          <button type="button" aria-label={`Rename ${sessionTitle(s)}`} onClick={() => setEditing(true)}>
            ✎
          </button>
          <button type="button" aria-label={`Delete ${sessionTitle(s)}`} onClick={() => setConfirm(true)}>
            ×
          </button>
        </span>
      )}
    </li>
  );
}

export function Sidebar({
  project,
  where,
  link,
  sessions,
  openId,
  unread,
  now,
  onOpen,
  onNew,
  onRename,
  onDelete,
  onDisconnect,
}: {
  project: string;
  where: string;
  link: Link;
  sessions: Session[];
  openId: string | null;
  unread: Record<string, true>;
  now: number;
  onOpen: (id: string) => void;
  onNew: () => void;
  onRename: (id: string, title: string) => void;
  onDelete: (id: string) => void;
  onDisconnect: () => void;
}) {
  return (
    <nav className="sidebar" aria-label="Sessions">
      <header className="side-head">
        <div>
          <div className="project">{project || "Deck"}</div>
          <div className="where muted">
            <span className={`dot link-${link}`} /> {link === "online" ? where : link}
          </div>
        </div>
      </header>
      <button type="button" className="new-session" onClick={onNew}>
        + New session <kbd>Ctrl N</kbd>
      </button>
      <ul className="sessions">
        {sessions.map((s) => (
          <Row
            key={s.id}
            s={s}
            open={s.id === openId}
            unread={unread[s.id] === true}
            now={now}
            onOpen={() => onOpen(s.id)}
            onRename={(t) => onRename(s.id, t)}
            onDelete={() => onDelete(s.id)}
          />
        ))}
      </ul>
      <footer className="side-foot">
        <span className="muted">
          <kbd>Ctrl K</kbd> commands
        </span>
        <button type="button" className="link-button" onClick={onDisconnect}>
          Switch Deck
        </button>
      </footer>
    </nav>
  );
}
