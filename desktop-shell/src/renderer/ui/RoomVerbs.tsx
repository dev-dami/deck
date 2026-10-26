import { applyAgentChip } from "@web/composer";
import type { AgentLine, ChangedFile, Image, Message, Mode, Session } from "@web/protocol";
import { sessionMode } from "@web/state";
import { Pencil, X } from "./icons";
import { type ReactNode, type RefObject, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { draftOf, setDraft } from "../appState";
import type { DeckClient } from "../client";
import { appendComment, commentBlock, countComments, fileAuthor } from "../comments";
import { messageText, retryText } from "../messages";
import { APPROVE_TEXT, approveMode, type ModeMemory, nextModeMemory, planText } from "../plans";
import { changedSince, takeoverNotice, takeoverText } from "../takeover";
import type { LineCommentApi } from "./DiffView";
import { Button } from "./kit/button";
import type { RoomActions } from "./Timeline";

// Where a stop left the room: the agent that was stopped, the changed files
// once the agents stood still, and the paths that changed since.
interface Stopped {
  sid: string;
  agent: string | null;
  base: ChangedFile[] | null;
  paths: string[];
}

// How often the take-over looks at the tree again, besides on window focus.
const WATCH_MS = 8000;

function lastAgent(messages: readonly Message[]): string | null {
  for (let i = messages.length - 1; i >= 0; i--) {
    const a = messages[i]?.agent;
    if (a !== undefined) return a;
  }
  return null;
}

const sameList = (a: readonly string[], b: readonly string[]) => a.length === b.length && a.every((x, i) => x === b[i]);

function Notice({ children, onDismiss }: { children: ReactNode; onDismiss: () => void }) {
  return (
    <div className="room-notice mb-2 flex items-center gap-2 rounded-lg border border-hairline bg-surface-1 py-1 pr-1 pl-3 text-xs inset-shadow-hl" role="status">
      {children}
      <Button variant="ghost-muted" size="icon-xs" aria-label="Dismiss" title="Dismiss" onClick={onDismiss}>
        <X />
      </Button>
    </div>
  );
}

// The open room's verbs: plan cards, message actions, line comments,
// take-over after a stop, and who may answer its approvals. Shell passes
// what this returns to the timeline, the composer and the diff.
export function useRoomVerbs({
  c,
  projectKey,
  open,
  roster,
  draft,
  asking,
  inputRef,
}: {
  c: DeckClient;
  projectKey: string;
  open: Session | undefined;
  roster: AgentLine[];
  draft: string;
  // Whether the room has an approval waiting.
  asking: boolean;
  inputRef: RefObject<HTMLTextAreaElement | null>;
}) {
  const sid = open?.id ?? null;
  const mode = sessionMode(open);
  const busy = open?.busy === true;

  // The mode before plan, so Approve goes back to yolo when it was yolo.
  const modes = useRef<Record<string, ModeMemory>>({});
  useEffect(() => {
    if (sid !== null) modes.current[sid] = nextModeMemory(modes.current[sid], mode);
  }, [sid, mode]);

  const live = useRef({ sid, roster });
  live.current = { sid, roster };
  const write = useCallback((text: string) => {
    const id = live.current.sid;
    if (id !== null) setDraft(projectKey, id, text);
  }, [projectKey]);
  const focusEnd = useCallback(() => {
    requestAnimationFrame(() => {
      const el = inputRef.current;
      if (el === null) return;
      el.focus();
      el.setSelectionRange(el.value.length, el.value.length);
    });
  }, [inputRef]);

  const [editing, setEditing] = useState<{ sid: string; id: string } | null>(null);
  const [stopped, setStopped] = useState<Stopped | null>(null);

  const actions = useMemo<RoomActions>(
    () => ({
      approve: async (agent: string) => {
        const id = live.current.sid;
        if (id === null) return false;
        const to: Mode = approveMode(modes.current[id]?.before ?? null);
        if (!(await c.setMode(id, to))) return false;
        return c.send(id, `$${agent} ${APPROVE_TEXT}`, []);
      },
      edit: (agent: string, steps: readonly string[]) => {
        write(`$${agent} Use this plan:\n${planText(steps)}`);
        focusEnd();
      },
      ask: (agent: string) => {
        const id = live.current.sid;
        if (id === null) return;
        write(applyAgentChip(draftOf(projectKey, id), agent));
        focusEnd();
      },
      editMessage: (m: Message) => {
        setEditing({ sid: m.session_id, id: m.id });
        write(messageText(m));
        focusEnd();
      },
      retry: (replyId: string) => {
        const id = live.current.sid;
        const text = id === null ? null : retryText(c.store.state.messages[id] ?? [], replyId);
        if (id !== null && text !== null) void c.send(id, text, []);
      },
      continueRun: () => {
        const id = live.current.sid;
        if (id !== null) void c.continueRun(id);
      },
    }),
    [c, projectKey, write, focusEnd],
  );

  // The composer's send: an edit replaces the message it edits.
  const send = useCallback(
    async (text: string, images: Image[]): Promise<boolean> => {
      if (sid === null) return false;
      if (editing !== null && editing.sid === sid) {
        const ok = await c.editMessage(sid, editing.id, text);
        if (ok) setEditing(null);
        return ok;
      }
      const ok = await c.send(sid, text, images);
      // The person spoke after the stop: the take-over is done.
      if (ok) setStopped((s) => (s?.sid === sid ? null : s));
      return ok;
    },
    [c, sid, editing],
  );

  const stop = useCallback(() => {
    if (sid === null) return;
    const agent = roster.find((l) => l.busy)?.id ?? lastAgent(c.store.state.messages[sid] ?? []);
    setStopped({ sid, agent, base: null, paths: [] });
    void c.cancel(sid);
  }, [c, sid, roster]);

  const interrupt = useCallback(
    (agent: string) => {
      if (sid === null) return;
      setStopped({ sid, agent, base: null, paths: [] });
      void c.interrupt(sid, agent);
    },
    [c, sid],
  );

  // Once the agents stand still, the changed files are the base the
  // person's own edits are measured against.
  useEffect(() => {
    if (stopped === null || stopped.sid !== sid || stopped.base !== null || busy) return;
    let on = true;
    const id = stopped.sid;
    void c.changes(id).then((f) => {
      if (on) setStopped((s) => (s === null || s.sid !== id || s.base !== null ? s : f === null ? null : { ...s, base: f }));
    });
    return () => {
      on = false;
    };
  }, [c, sid, stopped, busy]);

  // An agent at work again ends the take-over.
  useEffect(() => {
    if (busy) setStopped((s) => (s !== null && s.sid === sid && s.base !== null ? null : s));
  }, [busy, sid]);

  const base = stopped?.sid === sid ? (stopped?.base ?? null) : null;
  useEffect(() => {
    if (base === null || sid === null) return;
    let on = true;
    const look = () => {
      void c.changes(sid).then((f) => {
        if (!on || f === null) return;
        const paths = changedSince(base, f);
        setStopped((s) => (s === null || s.base !== base || sameList(s.paths, paths) ? s : { ...s, paths }));
      });
    };
    const t = setInterval(look, WATCH_MS);
    window.addEventListener("focus", look);
    return () => {
      on = false;
      clearInterval(t);
      window.removeEventListener("focus", look);
    };
  }, [c, sid, base]);

  const comments = useMemo<LineCommentApi | null>(() => {
    if (sid === null) return null;
    return {
      count: countComments(draft),
      add: (path, line, text, comment) => {
        const agent = fileAuthor(c.store.state.messages[sid] ?? [], live.current.roster, path);
        setDraft(projectKey, sid, appendComment(draftOf(projectKey, sid), commentBlock(agent, { path, line, text }, comment)));
        focusEnd();
      },
      send: () => {
        const text = draftOf(projectKey, sid);
        if (text.trim() === "") return;
        void send(text, []).then((ok) => {
          if (ok) setDraft(projectKey, sid, "");
        });
      },
    };
  }, [c, sid, projectKey, draft, focusEnd, send]);

  // You, and the teammates the thread is shared with as editors.
  const [editors, setEditors] = useState<{ sid: string; names: string[] } | null>(null);
  useEffect(() => {
    const api = c.project;
    if (sid === null || !asking || api === undefined) return;
    let on = true;
    void api.threadShares(sid).then((r) => {
      if (!on || typeof r === "string") return;
      const names = Object.entries(r)
        .filter(([, role]) => role === "editor")
        .map(([peer]) => peer)
        .sort();
      setEditors({ sid, names });
    });
    return () => {
      on = false;
    };
  }, [c, sid, asking]);
  const answerers = useMemo(() => ["You", ...(editors !== null && editors.sid === sid ? editors.names : [])], [editors, sid]);

  let notice: ReactNode = null;
  if (editing !== null && editing.sid === sid) {
    notice = (
      <Notice
        onDismiss={() => {
          setEditing(null);
          write("");
        }}
      >
        <Pencil className="size-3.5 shrink-0 text-faint" />
        <span className="min-w-0 flex-1 truncate text-muted-foreground">Editing your message. Send replaces it and runs it again.</span>
      </Notice>
    );
  } else if (stopped !== null && stopped.sid === sid && stopped.paths.length > 0) {
    const s = stopped;
    notice = (
      <Notice onDismiss={() => setStopped(null)}>
        <span className="takeover min-w-0 flex-1 truncate text-muted-foreground" title={s.paths.join("\n")}>
          {takeoverNotice(s.paths.length)}
        </span>
        <Button
          size="xs"
          variant="outline"
          onClick={() => {
            void c.send(s.sid, takeoverText(s.agent, s.paths), []).then((ok) => {
              if (ok) setStopped(null);
            });
          }}
        >
          Continue with my changes
        </Button>
      </Notice>
    );
  }

  return { mode, actions, send, stop, interrupt, comments, answerers, notice };
}
