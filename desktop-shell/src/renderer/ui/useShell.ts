import { sessionTitle } from "@web/state";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { DeckBridge } from "../../shared/bridge";
import type { DeckClient } from "../client";
import { crewOf } from "../crewState";
import { messageText } from "../messages";
import type { OpenProject } from "../openProjects";
import { type Command, type CommandDeps, MENU_PALETTE, type PaletteRoom, paletteCommands, previewCommands } from "../palette";
import type { FoundPort } from "../ports";
import { setPrefs } from "../prefs";
import { isSide } from "../sideChat";
import { type InboxItem, needsYouRooms, nextNeedsYou } from "../attention";
import type { TeamThread } from "../crew";
import type { Panel, UiAction } from "../ui";
import type { ForeignThread, RowRef } from "./Sidebar";

// Where the person goes from the shell: a room of this project or of
// another, a row of the sidebar by its number, the next room that needs
// them, and a room a teammate shares.
export function useShellNav({
  projectKey,
  source,
  dispatch,
  ui: { openId, inbox },
  attention: { items, onOpenRoom },
  onProject,
  focusInput,
  info,
  toast,
}: {
  projectKey: string;
  source: string;
  dispatch: (a: UiAction) => void;
  // The open room, and whether the inbox page shows.
  ui: { openId: string | null; inbox: boolean };
  // The inbox across projects, and how App opens a room of another project.
  attention: { items: InboxItem[]; onOpenRoom: (project: string, room: string) => void };
  onProject: (source: string, to?: string) => Promise<string | null>;
  focusInput: () => void;
  info: (text: string) => void;
  toast: (text: string) => void;
}) {
  // A room a teammate shares with you, shown in place of this project's room.
  const [shared, setShared] = useState<{ peer: string; thread: TeamThread } | null>(null);
  const orToast = (p: Promise<string | null>) => void p.then((e) => e !== null && toast(e));
  const openSession = useCallback(
    (id: string | null) => {
      setShared(null);
      dispatch({ type: "open", id });
      focusInput();
    },
    [dispatch, focusInput],
  );
  // A new thread is the empty screen: the first message makes it.
  const newSession = useCallback(() => openSession(null), [openSession]);
  // A room of this project opens here; one of another project through App.
  const openRoom = (project: string, room: string) => (project === projectKey ? openSession(room) : onOpenRoom(project, room));
  // The sidebar's rows in order, for Ctrl/Cmd+1 to 9.
  const order = useRef<RowRef[]>([]);
  const toRow = (n: number) => {
    const row = order.current[n - 1];
    if (row === undefined) return;
    if (row.source === source) openSession(row.id);
    else orToast(onProject(row.source, row.id));
  };
  const nextAttention = () => {
    const to = nextNeedsYou(needsYouRooms(items), { project: projectKey, room: inbox ? null : openId });
    if (to === null) info("No room needs you.");
    else openRoom(to.project, to.room);
  };
  const openShared = (peer: string, thread: TeamThread) => {
    // Opening a room closes Settings, Usage and Team, as a thread does.
    dispatch({ type: "open", id: openId });
    setShared({ peer, thread });
  };
  return { shared, openSession, newSession, openRoom, order, toRow, nextAttention, openShared, orToast };
}

// The threads of the other open projects, kept current from their stores.
export function useForeignThreads(others: OpenProject[]): ForeignThread[] {
  const [rows, setRows] = useState<ForeignThread[]>([]);
  useEffect(() => {
    let lists: unknown[] = [];
    const read = () => {
      const next = others.map((o) => o.c.store.state.sessions);
      // Only a changed session list makes new rows.
      if (next.length === lists.length && next.every((l, i) => l === lists[i])) return;
      lists = next;
      setRows(
        others.flatMap((o) =>
          o.c.store.state.sessions.filter((s) => !isSide(s)).map((s) => ({ source: o.source, project: o.name, s, c: o.c })),
        ),
      );
    };
    read();
    const offs = others.map((o) => o.c.store.subscribe(read));
    return () => {
      for (const off of offs) off();
    };
  }, [others]);
  return rows;
}

// Whether Ctrl or Cmd is held: the sidebar's rows then show their digit,
// for Ctrl/Cmd+1 to 9.
export function useModDigits(): boolean {
  const [digits, setDigits] = useState(false);
  useEffect(() => {
    let timer: ReturnType<typeof setTimeout> | null = null;
    const hide = () => {
      if (timer !== null) clearTimeout(timer);
      timer = null;
      setDigits(false);
    };
    const isMod = (e: KeyboardEvent) => e.key === "Control" || e.key === "Meta";
    const down = (e: KeyboardEvent) => {
      if (!isMod(e)) hide();
      else if (!e.repeat && timer === null) timer = setTimeout(() => setDigits(true), 300);
    };
    const up = (e: KeyboardEvent) => {
      if (isMod(e)) hide();
    };
    window.addEventListener("keydown", down);
    window.addEventListener("keyup", up);
    window.addEventListener("blur", hide);
    return () => {
      hide();
      window.removeEventListener("keydown", down);
      window.removeEventListener("keyup", up);
      window.removeEventListener("blur", hide);
    };
  }, []);
  return digits;
}

interface ShellKeys {
  nextAttention: () => void;
  room: (n: number) => void;
  toggleYolo: () => void;
  togglePalette: () => void;
  newThread: () => void;
  toggleSidebar: () => void;
  settings: () => void;
  // Null without a terminal (a paired Deck).
  toggleTerminal: (() => void) | null;
}

// The window's own keys. The handlers are read when a key comes, so the
// listener stays the same one.
function useShellKeys(keys: ShellKeys): void {
  const latest = useRef(keys);
  latest.current = keys;
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const k = latest.current;
      // Ctrl/Cmd+Alt+L: the next room that needs the person. The code, as
      // Alt changes the key a Mac reports.
      if ((e.ctrlKey || e.metaKey) && e.altKey && !e.shiftKey && e.code === "KeyL") {
        e.preventDefault();
        k.nextAttention();
        return;
      }
      if (!(e.ctrlKey || e.metaKey) || e.altKey) return;
      const key = e.key.toLowerCase();
      const digit = /^Digit([1-9])$/.exec(e.code);
      if (digit !== null && !e.shiftKey) {
        e.preventDefault();
        k.room(Number(digit[1]));
        return;
      }
      if (e.shiftKey) {
        // With Shift, so Ctrl+Y stays Redo in a text field.
        if (key === "y") {
          e.preventDefault();
          k.toggleYolo();
        }
        return;
      }
      const run =
        key === "k"
          ? k.togglePalette
          : key === "n"
            ? k.newThread
            : key === "b"
              ? k.toggleSidebar
              : key === ","
                ? k.settings
                : key === "j" || key === "`"
                  ? k.toggleTerminal
                  : null;
      if (run === null) return;
      e.preventDefault();
      run();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);
}

type CommandFns = Omit<CommandDeps, "mac" | "terminal" | "theme">;
// The commands that open a page or a panel go through the project's UI state.
type UiCommands = "togglePanel" | "toggleSidePanel" | "settings" | "usage" | "team" | "inbox";

// The palette's commands, which the application menu and the window's keys
// run too. They call the latest handlers, so the list changes only with the
// platform and the terminal.
export function useShellCommands(
  {
    mac,
    terminal,
    dispatch,
    panel,
    palette,
    toggleYolo,
    ports,
    preview,
    ...rest
  }: Omit<CommandFns, UiCommands> & {
    mac: boolean;
    terminal: boolean;
    dispatch: (a: UiAction) => void;
    // The open tab of the side panel, and whether the palette shows.
    panel: Panel | null;
    palette: boolean;
    toggleYolo: () => void;
    // The project's open ports, and how one opens in the Preview tab.
    ports: readonly FoundPort[];
    preview: (url: string) => void;
  },
  bridge: DeckBridge,
): Command[] {
  const fns: CommandFns = {
    ...rest,
    togglePanel: (p) => dispatch({ type: "panel", panel: p }),
    toggleSidePanel: () => dispatch({ type: "panel", panel: panel ?? "changes" }),
    settings: (section) => dispatch({ type: "settings", section }),
    usage: () => dispatch({ type: "usage", open: true }),
    team: () => dispatch({ type: "team", open: true }),
    inbox: () => dispatch({ type: "inbox", open: true }),
  };
  const openPalette = () => dispatch({ type: "palette", open: true });
  const togglePalette = () => dispatch({ type: "palette", open: !palette });
  const latest = useRef({ fns, openPalette, preview });
  latest.current = { fns, openPalette, preview };
  useShellKeys({
    nextAttention: fns.nextAttention,
    room: fns.room,
    toggleYolo,
    togglePalette,
    newThread: fns.newThread,
    toggleSidebar: fns.toggleSidebar,
    settings: () => fns.settings("general"),
    toggleTerminal: terminal ? fns.toggleTerminal : null,
  });
  const commands = useMemo(() => {
    const live =
      <K extends keyof CommandFns>(k: K) =>
      (...a: Parameters<CommandFns[K]>) =>
        (latest.current.fns[k] as (...x: Parameters<CommandFns[K]>) => void)(...a);
    const list = paletteCommands({
      mac,
      terminal,
      newThread: live("newThread"),
      togglePanel: live("togglePanel"),
      toggleSidePanel: live("toggleSidePanel"),
      toggleSidebar: live("toggleSidebar"),
      settings: live("settings"),
      usage: live("usage"),
      team: live("team"),
      inbox: live("inbox"),
      nextAttention: live("nextAttention"),
      room: live("room"),
      toggleTerminal: live("toggleTerminal"),
      theme: (theme) => setPrefs({ theme }),
      addProject: live("addProject"),
      switchDeck: live("switchDeck"),
      welcome: live("welcome"),
    });
    return [...list, ...previewCommands(ports, (url) => latest.current.preview(url))];
    // A new probe makes a new ports array; the commands change with its URLs.
  }, [mac, terminal, ports.map((p) => p.url).join(" ")]);
  useEffect(
    () =>
      bridge.onMenu((m) => {
        if (m === "palette") latest.current.openPalette();
        else commands.find((x) => x.id === MENU_PALETTE[m])?.run();
      }),
    [bridge, commands],
  );
  return commands;
}

// A downloaded update that waits for a restart; "Check for updates"
// answers in a toast.
export function useUpdate(bridge: DeckBridge, c: DeckClient): string | null {
  const [update, setUpdate] = useState<string | null>(null);
  useEffect(
    () =>
      bridge.onUpdate((ev) => {
        if (ev.type === "ready") setUpdate(ev.version);
        else c.store.dispatch({ type: "toast.add", kind: "info", text: ev.message });
      }),
    [bridge, c],
  );
  return update;
}

// Every open project's rooms for the palette, read as it opens.
export function usePaletteRooms(c: DeckClient, projectKey: string, project: string, others: OpenProject[]): () => PaletteRoom[] {
  return useCallback((): PaletteRoom[] => {
    const of = (key: string, name: string, client: DeckClient): PaletteRoom[] =>
      client.store.state.sessions
        .filter((s) => !isSide(s))
        .map((s) => {
          const crewNow = crewOf(client.project, s.id);
          const msgs = client.store.state.messages[s.id];
          return {
            project: key,
            name,
            id: s.id,
            title: sessionTitle(s),
            updated: s.updated_at,
            branches: [...(crewNow?.crew?.agents ?? []), ...(crewNow?.board?.tasks ?? [])].map((x) => x.branch).filter((b) => b !== ""),
            texts: msgs === undefined ? null : msgs.map(messageText).filter((t) => t !== "").reverse(),
          };
        });
    return [...of(projectKey, project, c), ...others.flatMap((o) => of(o.key, o.name, o.c))];
  }, [c, projectKey, project, others]);
}
