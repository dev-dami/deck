import type { Store } from "@web/controller";
import { needsConfirm } from "@web/modes";
import type { ChangedFile, Image, Mode, QueuedItem, Session } from "@web/protocol";
import { sessionMode } from "@web/state";
import { type RefObject, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { setDraft as storeDraft, useDraft, useLocalNotices } from "../appState";
import { type DeckClient, serves } from "../client";
import { getPrefs } from "../prefs";
import { newQueued } from "../queue";
import { NO_RACE, type RaceDraft, toWire } from "../race";
import type { ShellSlice } from "../select";
import type { Slash } from "../slash";
import { titleFrom } from "../title";
import { yoloMove } from "../yolo";
import { useCrew } from "./AgentsTree";
import { useRoomVerbs } from "./RoomVerbs";
import { useShares } from "./Team";

// The draft of the composer on the new-thread screen, before a thread exists.
export const NEW = "";

// How long a steer waits for its message to show in the queue. A message
// to an agent that is not busy is delivered at once and never queues.
const STEER_WAIT_MS = 3000;

const YOLO_ASK = "Agents run everything without asking. Switch to Yolo?";

// The items a send adds to a room's queue, once the queue shows them.
function queuedAfter(store: Store, sid: string, before: ReadonlySet<string>): Promise<QueuedItem[]> {
  return new Promise((resolve) => {
    const look = () => newQueued(before, store.state.queue[sid] ?? []);
    let off = () => {};
    let timer: ReturnType<typeof setTimeout> | undefined;
    const done = (items: QueuedItem[]) => {
      off();
      clearTimeout(timer);
      resolve(items);
    };
    const now = look();
    if (now.length > 0) return done(now);
    off = store.subscribe(() => {
      const items = look();
      if (items.length > 0) done(items);
    });
    timer = setTimeout(() => done([]), STEER_WAIT_MS);
  });
}

// The open thread's changed files: for the card under the last reply, the
// header and the diff panel. Fetched on opening and each time the agents
// finish; refresh fetches them again.
function useChanges(c: DeckClient, sid: string | null, busy: boolean): { files: ChangedFile[] | null; refresh: () => void } {
  const [changes, setChanges] = useState<{ id: string; files: ChangedFile[] } | null>(null);
  const [tick, setTick] = useState(0);
  const refresh = useCallback(() => setTick((t) => t + 1), []);
  useEffect(() => {
    if (sid === null) return;
    let live = true;
    void c.changes(sid).then((f) => {
      if (live) setChanges({ id: sid, files: f ?? [] });
    });
    return () => {
      live = false;
    };
  }, [c, sid, busy, tick]);
  return { files: changes !== null && changes.id === sid ? changes.files : null, refresh };
}

// The send path of a room and its commands: the first message of a new
// thread, a steer into a running turn, the slash commands and Yolo.
function useRoomSend({
  c,
  projectKey,
  open,
  send,
  stop,
  openSession,
  newSession,
  allowYolo,
  confirmYolo,
  canShare,
  onShare,
  onRace,
  info,
  toast,
}: {
  c: DeckClient;
  projectKey: string;
  open: Session | undefined;
  // Opens the race builder with brief as its task (/race).
  onRace: (brief: string) => void;
  // The room's own send and stop (useRoomVerbs).
  send: (text: string, images: Image[]) => Promise<boolean>;
  stop: () => void;
  openSession: (id: string | null) => void;
  newSession: () => void;
  allowYolo: boolean;
  confirmYolo: boolean;
  canShare: boolean;
  // Opens the share popover (/share).
  onShare: () => void;
  info: (text: string) => void;
  toast: (text: string) => void;
}) {
  const sid = open?.id ?? null;

  // Sends from the new-thread screen: makes the thread (on a branch of its
  // own when the person keeps that on), opens it and sends.
  const start = useCallback(
    async (text: string, images: Image[]): Promise<boolean> => {
      const s = await c.createSession(titleFrom(text) || undefined, serves(c, "IsolateSession") && getPrefs().isolate);
      if (s === null) return false;
      storeDraft(projectKey, NEW, "");
      openSession(s.id);
      const ok = await c.send(s.id, text, images);
      // The thread's own composer keeps the text of a failed send.
      if (!ok) storeDraft(projectKey, s.id, text);
      return ok;
    },
    [c, projectKey, openSession],
  );

  // The mode each thread had before Ctrl+Shift+Y put it in yolo, to go back to.
  const beforeYolo = useRef<Record<string, Mode>>({});
  const toggleYolo = useCallback(() => {
    if (open === undefined) return;
    const current = sessionMode(open);
    const move = yoloMove(current, beforeYolo.current[open.id] ?? null, allowYolo);
    if ("refused" in move) {
      c.store.dispatch({ type: "toast.add", kind: "error", text: move.refused });
      return;
    }
    if (needsConfirm(move.to) && confirmYolo && !window.confirm(YOLO_ASK)) return;
    if (move.to === "yolo" && current !== null) beforeYolo.current[open.id] = current;
    void c.setMode(open.id, move.to);
  }, [open, c, allowYolo, confirmYolo]);

  // A steer sends the message, which queues for the busy agent, then takes
  // its queue items into the running turn.
  const steer = useCallback(
    async (text: string, images: Image[]): Promise<boolean> => {
      if (sid === null) return false;
      const before = new Set((c.store.state.queue[sid] ?? []).map((q) => q.id));
      if (!(await send(text, images))) return false;
      void queuedAfter(c.store, sid, before).then((items) => {
        for (const q of items) void c.steer(sid, q.id);
      });
      return true;
    },
    [c, sid, send],
  );

  const runSlash = useCallback(
    async (s: Slash): Promise<boolean> => {
      if (s.cmd === "clear") return true;
      if (s.cmd === "new") {
        if (s.title === "") {
          newSession();
          return true;
        }
        const made = await c.createSession(s.title);
        if (made !== null) openSession(made.id);
        return made !== null;
      }
      if (s.cmd === "race") {
        onRace(s.brief);
        return true;
      }
      if (open === undefined) {
        info("Open a thread first.");
        return false;
      }
      if (s.cmd === "stop") {
        if (open.busy) stop();
        else info("No agent is working.");
        return true;
      }
      if (s.cmd === "share") {
        if (!canShare) {
          info("Sharing needs a team link with teammates.");
          return false;
        }
        onShare();
        return true;
      }
      const current = sessionMode(open);
      if (current === s.mode) return true;
      if (s.mode === "yolo") {
        const move = yoloMove(current, null, allowYolo);
        if ("refused" in move) {
          toast(move.refused);
          return false;
        }
        if (confirmYolo && needsConfirm("yolo") && !window.confirm(YOLO_ASK)) return false;
        if (current !== null) beforeYolo.current[open.id] = current;
      }
      return c.setMode(open.id, s.mode);
    },
    [c, open, stop, newSession, openSession, canShare, onShare, onRace, info, toast, allowYolo, confirmYolo],
  );

  // Starts the race the builder made, with exactly its entrants: in the
  // open thread, or in a new one named after its task.
  const startRace = useCallback(
    async (d: RaceDraft): Promise<boolean> => {
      // A local Deck lists StartRace when it serves it; a paired one answers race.start.
      if (c.startRace === undefined || (c.capabilities !== undefined && !serves(c, "StartRace"))) {
        info(NO_RACE);
        return false;
      }
      let id = open?.id;
      if (id === undefined) {
        const made = await c.createSession(titleFrom(d.brief) || undefined, serves(c, "IsolateSession") && getPrefs().isolate);
        if (made === null) return false;
        openSession(made.id);
        id = made.id;
      }
      return (await c.startRace(id, d.brief, toWire(d), d.judge)) !== null;
    },
    [c, open, openSession, info],
  );

  return { start, steer, runSlash, startRace, toggleYolo };
}

// Everything about the open room (or the new-thread screen) that the room
// view, the side panel and the keys share: the draft, the changed files,
// the agents tree, who follows the room, the lane filter, the room's verbs
// and its send path.
export function useOpenRoom({
  c,
  projectKey,
  open,
  state,
  inputRef,
  openSession,
  newSession,
  canShare,
  confirmYolo,
  refreshGit,
  info,
  toast,
}: {
  c: DeckClient;
  projectKey: string;
  open: Session | undefined;
  state: Pick<ShellSlice, "roster" | "approvals" | "server">;
  inputRef: RefObject<HTMLTextAreaElement | null>;
  openSession: (id: string | null) => void;
  newSession: () => void;
  // The team link has teammates to share with.
  canShare: boolean;
  confirmYolo: boolean;
  // The agents' edits change the work tree: git looks again when they stop.
  refreshGit: () => Promise<unknown>;
  info: (text: string) => void;
  toast: (text: string) => void;
}) {
  const sid = open?.id ?? null;
  const busy = open?.busy === true;
  const key = sid ?? NEW;
  const draft = useDraft(projectKey, key);
  const setDraft = useCallback((text: string) => storeDraft(projectKey, key, text), [projectKey, key]);
  // The lane the timeline shows alone, kept for the room it was picked in.
  const [lanePick, setLanePick] = useState<{ sid: string | null; lane: string | null }>({ sid: null, lane: null });
  // /share opens the room header's share popover.
  const [shareAsk, setShareAsk] = useState(0);
  // /race opens the race builder over the room's composer, with the task
  // typed after it; n remounts it for each /race.
  const [racePick, setRacePick] = useState<{ key: string; brief: string; n: number } | null>(null);
  const openRace = useCallback((brief: string) => setRacePick((r) => ({ key, brief, n: (r?.n ?? 0) + 1 })), [key]);
  const changes = useChanges(c, sid, busy);
  // The shell's own notes in the room, for its timeline.
  const notes = useLocalNotices(projectKey, key);
  // The composer's "@" lists these.
  const paths = useMemo(() => changes.files?.map((f) => f.path) ?? [], [changes.files]);
  useEffect(() => {
    if (!busy) void refreshGit();
  }, [busy, refreshGit]);
  // The agents tree (the lane strip and the Agents tab) and who follows the room.
  const { crew, at: crewAt } = useCrew(c.project, sid);
  const { shares, set: setShare } = useShares(c.project, sid);
  const roster = sid === null ? [] : (state.roster[sid] ?? []);
  const asking = sid !== null && state.approvals.some((a) => a.session_id === sid);
  const room = useRoomVerbs({ c, projectKey, open, roster, draft, asking, inputRef });
  const send = useRoomSend({
    c,
    projectKey,
    open,
    send: room.send,
    stop: room.stop,
    openSession,
    newSession,
    allowYolo: state.server?.allow_yolo ?? false,
    confirmYolo,
    canShare,
    onShare: () => setShareAsk((n) => n + 1),
    onRace: openRace,
    info,
    toast,
  });
  return {
    draft,
    setDraft,
    files: changes.files,
    refreshChanges: changes.refresh,
    paths,
    notes,
    crew,
    crewAt,
    shares,
    setShare,
    shareAsk,
    lane: lanePick.sid === sid ? lanePick.lane : null,
    setLane: (lane: string | null) => setLanePick({ sid, lane }),
    room,
    send,
    race: racePick !== null && racePick.key === key ? racePick : null,
    closeRace: () => setRacePick(null),
  };
}

export type OpenRoom = ReturnType<typeof useOpenRoom>;
