import type { Decision } from "@web/protocol";
import { useCallback, useEffect, useRef, useState } from "react";
import type { DeckBridge, LocalDeck, SavedServer, StartError } from "../../shared/bridge";
import { useAppearance } from "../appearance";
import { attachProject, dispatchUi, dropProject, setEngine, showProject } from "../appState";
import type { InboxItem } from "../attention";
import type { DeckClient } from "../client";
import { connect, openLocal } from "../connection";
import { refreshTeam, teamOf } from "../crewState";
import { parseTag, useBadge, useInbox, useNotifications } from "../inbox";
import { forgetOpen, type OpenProject, rememberOpen, restoredOpen } from "../openProjects";
import { usePrefs } from "../prefs";
import { ConnectScreen } from "./ConnectScreen";
import { Shell } from "./Shell";
import type { ProjectEntry } from "./Sidebar";
import { closeTerminals } from "./Terminal";
import { base, type StartFailure, Welcome } from "./Welcome";

// A paired Deck as the window names it: where it runs, never its address.
function pairedName(s: SavedServer): string {
  return s.tls ? "Paired Deck (another computer)" : "Paired Deck (this computer)";
}

// Where to go once a project is current: a thread, the new-thread screen
// ("new"), or wherever the project was (null).
export type Landing = string | "new" | null;

function land(key: string, to: Landing): void {
  if (to !== null) dispatchUi(key, { type: "open", id: to === "new" ? null : to });
}

// Like Claude Desktop, several projects stay open at once: each runs its
// own Deck, the sidebar lists the threads of all of them, and the one whose
// thread is open is current. The open set comes back on the next start.
export function App({ bridge }: { bridge: DeckBridge }) {
  const [servers, setServers] = useState<SavedServer[] | null>(null);
  const [recent, setRecent] = useState<string[]>([]);
  const [deck, setDeck] = useState<string | null>(null);
  const [open, setOpen] = useState<OpenProject[]>([]);
  const [currentKey, setCurrentKey] = useState<string | null>(null);
  const [message, setMessage] = useState<string | null>(null);
  // The connect screen over open projects, to add or pair another.
  const [connecting, setConnecting] = useState(false);
  // The folder whose Deck is starting.
  const [opening, setOpening] = useState<string | null>(null);
  // The Welcome: on the first launch, from the palette, and when the engine
  // finds no agent CLI.
  const [welcome, setWelcome] = useState(false);
  // The engine's last start error, until a project opens.
  const [failure, setFailure] = useState<StartFailure | null>(null);
  const live = useRef<OpenProject[]>([]);
  live.current = open;

  const refresh = useCallback(async () => {
    const [s, r] = await Promise.all([bridge.listServers(), bridge.recentFolders()]);
    setServers(s);
    setRecent(r);
  }, [bridge]);

  // Adds a project and makes it current; one already open just becomes
  // current (main hands back the same Deck for a folder it runs).
  const add = useCallback((p: OpenProject, to: Landing) => {
    const had = live.current.find((x) => x.source === p.source);
    if (had !== undefined) {
      if (had.c !== p.c) p.c.forget();
      setCurrentKey(had.key);
      land(had.key, to);
    } else {
      live.current = [...live.current, p];
      attachProject(p.key, p.source, p.c.store, p.c);
      setOpen(live.current);
      setCurrentKey(p.key);
      rememberOpen(p.source);
      land(p.key, to);
    }
    setMessage(null);
    setConnecting(false);
    setWelcome(false);
    setFailure(null);
  }, []);

  const pairedOpen = useCallback(
    (s: SavedServer, to: Landing = null) => {
      const source = `paired:${s.id}`;
      const had = live.current.find((x) => x.source === source);
      if (had !== undefined) {
        setCurrentKey(had.key);
        land(had.key, to);
        setConnecting(false);
        return;
      }
      add(
        {
          key: `v2:${s.id}`,
          source,
          name: "Paired Deck",
          where: pairedName(s),
          transport: "Paired Deck",
          c: connect(bridge, s.id),
        },
        to,
      );
    },
    [bridge, add],
  );

  const localOpen = useCallback(
    (d: LocalDeck, to: Landing) => {
      const source = `folder:${d.dir}`;
      const had = live.current.find((x) => x.source === source);
      if (had !== undefined) {
        setCurrentKey(had.key);
        land(had.key, to);
        setConnecting(false);
        return;
      }
      const key = `rpc:${d.id}`;
      // The project stays open when its engine stops; its shells died with it.
      const c: DeckClient = openLocal(bridge, d, (stop) => {
        if (stop !== null) closeTerminals(c.project);
        setEngine(key, stop);
      });
      add({ key, source, name: base(d.dir), where: d.dir, transport: "Project on this computer", c }, to);
    },
    [bridge, add],
  );

  // An engine that did not start shows on the landing screens: no agent CLI
  // on the Welcome, any other error with its last lines. The error text for
  // the caller, or null when a screen shows it.
  const failStart = useCallback((dir: string, out: { message: string; start?: StartError }): string | null => {
    if (out.start === undefined) return out.message;
    setFailure({ dir, message: out.message, ...out.start });
    if (out.start.noAgents) setWelcome(true);
    else setConnecting(true);
    return null;
  }, []);

  // Starts (or finds) the Deck of a folder; the error text, or null.
  const openFolder = useCallback(
    async (dir: string, to: Landing = null): Promise<string | null> => {
      const had = live.current.find((x) => x.source === `folder:${dir}`);
      if (had !== undefined) {
        setCurrentKey(had.key);
        land(had.key, to);
        setConnecting(false);
        return null;
      }
      setOpening(dir);
      const out = await bridge.openFolder(dir);
      setOpening(null);
      void refresh();
      if (!out.ok) return failStart(dir, out);
      localOpen(out.deck, to);
      return null;
    },
    [bridge, localOpen, refresh, failStart],
  );

  // Closes one project: its Deck stops (a folder) or its link ends (paired).
  const close = useCallback((key: string, why: string | null = null) => {
    const p = live.current.find((x) => x.key === key);
    if (p === undefined) return;
    live.current = live.current.filter((x) => x.key !== key);
    setOpen(live.current);
    forgetOpen(p.source);
    setCurrentKey((cur) => (cur === key ? (live.current[0]?.key ?? null) : cur));
    if (why !== null) setMessage(why);
    closeTerminals(p.c.project);
    dropProject(key);
    p.c.forget();
  }, []);

  // On start: the projects that were open, else the last folder, else the
  // newest pairing.
  useEffect(() => {
    void (async () => {
      const [s, r, d] = await Promise.all([bridge.listServers(), bridge.recentFolders(), bridge.findDeck()]);
      setServers(s);
      setRecent(r);
      setDeck(d);
      if (r.length === 0 && s.length === 0) setWelcome(true);
      let sources = restoredOpen();
      if (sources.length === 0) {
        const first = d !== null && r[0] !== undefined ? `folder:${r[0]}` : s[0] !== undefined ? `paired:${s[0].id}` : null;
        sources = first === null ? [] : [first];
      }
      // The folders' Decks start together; the projects then open in the
      // order they were listed.
      const folders = sources.filter((src) => src.startsWith("folder:") && d !== null);
      setOpening(folders[0]?.slice("folder:".length) ?? null);
      const started = await Promise.allSettled(folders.map((src) => bridge.openFolder(src.slice("folder:".length))));
      setOpening(null);
      if (folders.length > 0) void refresh();
      let err: string | null = null;
      for (const src of sources) {
        const got = started[folders.indexOf(src)];
        if (got !== undefined) {
          if (got.status === "rejected") err = String(got.reason);
          else if (got.value.ok) localOpen(got.value.deck, null);
          else err = failStart(src.slice("folder:".length), got.value);
        } else if (src.startsWith("paired:")) {
          const srv = s.find((x) => `paired:${x.id}` === src);
          if (srv !== undefined) pairedOpen(srv);
          else forgetOpen(src);
        }
      }
      // The first one restored is current, as it was listed first.
      if (live.current[0] !== undefined) setCurrentKey(live.current[0].key);
      if (err !== null) setMessage(err);
    })();
  }, [bridge, localOpen, pairedOpen, refresh, failStart]);

  // A client resets to "unpaired" when its Deck ends the connection for good
  // (a refused token over v2, a closed Deck over rpc) and to "protocol"
  // on a version mismatch: that project closes, the others stay.
  useEffect(() => {
    const check = () => {
      for (const p of live.current) {
        const a = p.c.state.auth;
        if (a === "unpaired") close(p.key, p.c.state.authMessage ?? `${p.name}: Deck ended this connection.`);
        else if (a === "protocol") close(p.key, "This Deck speaks another protocol version. Update Deck or this app.");
      }
    };
    const offs = open.map((p) => p.c.store.subscribe(check));
    return () => {
      for (const off of offs) off();
    };
  }, [open, close]);

  const prefs = usePrefs();
  useAppearance(prefs.theme, prefs.palette, prefs.accent);

  // The project on screen sees its open room; the others gather news.
  const shownKey = connecting || welcome ? null : currentKey;
  useEffect(() => showProject(shownKey), [shownKey, open]);

  // The inbox, the badge and the notifications, across the open projects.
  const inbox = useInbox(open);
  useBadge(bridge, inbox);
  useNotifications(bridge, open);

  // The inbox is a page of the project it opened in; a switch leaves it.
  const left = useRef(currentKey);
  useEffect(() => {
    if (left.current !== null && left.current !== currentKey) dispatchUi(left.current, { type: "inbox", open: false });
    left.current = currentKey;
  }, [currentKey]);

  // Opens a room of any open project.
  const openRoom = useCallback((key: string, room: string) => {
    if (!live.current.some((p) => p.key === key)) return;
    setCurrentKey(key);
    land(key, room);
    setConnecting(false);
  }, []);
  useEffect(
    () =>
      bridge.onNotificationClick(({ tag }) => {
        const t = parseTag(tag);
        if (t !== null) openRoom(t.project, t.room);
      }),
    [bridge, openRoom],
  );
  const attention = {
    items: inbox,
    onOpenRoom: openRoom,
    onAnswer: (item: InboxItem, d: Decision) => {
      const p = live.current.find((x) => x.key === item.project);
      if (p !== undefined && item.approval !== undefined) void p.c.answerApproval(item.approval.session_id, item.approval.id, d);
    },
    onContract: async (item: InboxItem, accept: boolean): Promise<string | null> => {
      const api = live.current.find((x) => x.key === item.project)?.c.project;
      const c = item.contract;
      if (api === undefined || c === undefined) return null;
      const err = await api.answerContract(c.peer, c.id, accept, teamOf(api)?.contractKinds[0] ?? "");
      await refreshTeam(api);
      return err;
    },
  };

  const current = open.find((p) => p.key === currentKey) ?? null;
  const isOpen = (source: string) => open.some((p) => p.source === source);
  const projects: ProjectEntry[] = [
    ...recent.map((dir) => ({
      key: `folder:${dir}`,
      name: base(dir),
      detail: dir,
      kind: "folder" as const,
      active: current?.source === `folder:${dir}`,
      open: isOpen(`folder:${dir}`),
    })),
    ...(servers ?? []).map((s) => ({
      key: `paired:${s.id}`,
      name: "Paired Deck",
      detail: s.tls ? "Another computer" : "This computer",
      kind: "paired" as const,
      active: current?.source === `paired:${s.id}`,
      open: isOpen(`paired:${s.id}`),
    })),
  ];

  const toProject = async (source: string, to: Landing = null): Promise<string | null> => {
    if (source.startsWith("folder:")) return openFolder(source.slice("folder:".length), to);
    const s = servers?.find((x) => `paired:${x.id}` === source);
    if (s !== undefined) pairedOpen(s, to);
    return null;
  };

  const addProject = async (): Promise<string | null> => {
    if (deck === null) return "This install of Deck is missing its engine. Reinstall Deck.";
    const dir = await bridge.pickFolder();
    return dir === null ? null : openFolder(dir, "new");
  };
  const retry = async (): Promise<string | null> => (failure === null ? null : openFolder(failure.dir, "new"));
  const back =
    current === null
      ? {}
      : {
          onBack: () => {
            setInvite(null);
            setConnecting(false);
            setWelcome(false);
          },
        };

  // A deck:// link, a folder argument or a dropped folder: a folder opens,
  // an invite fills the pairing form. An error shows on the connect screen.
  const [invite, setInvite] = useState<string | null>(null);
  const openFrom = useCallback(
    async (dir: string) => {
      const e = await openFolder(dir, "new");
      if (e === null) return;
      setMessage(e);
      setWelcome(false);
      setConnecting(true);
    },
    [openFolder],
  );
  useEffect(
    () =>
      bridge.onLink((l) => {
        if (l.type === "open") void openFrom(l.path);
        else {
          setInvite(l.url);
          setWelcome(false);
          setConnecting(true);
        }
      }),
    [bridge, openFrom],
  );
  const onDrop = (e: React.DragEvent) => {
    const dirs = [...e.dataTransfer.items]
      .filter((i) => i.kind === "file" && i.webkitGetAsEntry()?.isDirectory === true)
      .map((i) => i.getAsFile())
      .filter((f) => f !== null);
    if (dirs.length === 0) return;
    e.preventDefault();
    for (const f of dirs) {
      const dir = bridge.folderPath(f);
      if (dir !== "") void openFrom(dir);
    }
  };
  // The project view runs the menu's Open folder; without one, it is here.
  const menuOpen = useRef<(() => void) | null>(null);
  menuOpen.current =
    current !== null && !connecting && !welcome ? null : () => void addProject().then((e) => e !== null && setMessage(e));
  useEffect(() => bridge.onMenu((m) => m === "open-folder" && menuOpen.current?.()), [bridge]);

  return (
    <div
      className="app h-full"
      data-app=""
      onDragOver={(e) => {
        if (e.dataTransfer.types.includes("Files")) e.preventDefault();
      }}
      onDrop={onDrop}
    >
      {current !== null && !connecting && !welcome ? (
        <Shell
          key={current.key}
          c={current.c}
          projectKey={current.key}
          bridge={bridge}
          folder={current.source.startsWith("folder:") ? current.source.slice("folder:".length) : null}
          name={current.name}
          where={current.where}
          transport={current.transport}
          others={open.filter((p) => p !== current)}
          attention={attention}
          projects={projects}
          onProject={toProject}
          onAddProject={addProject}
          onCloseProject={(source) => {
            const p = open.find((x) => x.source === source);
            if (p !== undefined) close(p.key);
          }}
          onDisconnect={() => close(current.key)}
          onConnect={() => setConnecting(true)}
          onWelcome={() => setWelcome(true)}
        />
      ) : servers === null ? (
        <div className="loading grid h-full place-items-center text-muted-foreground text-sm">Loading…</div>
      ) : welcome ? (
        <Welcome
          bridge={bridge}
          opening={opening}
          failure={failure}
          onAdd={addProject}
          onPair={() => {
            setWelcome(false);
            setInvite("");
            setConnecting(true);
          }}
          onRetry={retry}
          {...back}
        />
      ) : (
        <ConnectScreen
          key={invite ?? ""}
          invite={invite}
          bridge={bridge}
          deck={deck}
          servers={servers}
          recent={recent}
          opening={opening}
          message={message}
          failure={failure}
          onAdd={addProject}
          onFolder={(dir) => openFolder(dir)}
          onRetry={retry}
          onPaired={(s) => {
            setInvite(null);
            void refresh();
            pairedOpen(s);
          }}
          onOpen={(s) => pairedOpen(s)}
          onForget={(s) => void bridge.forgetServer(s.id).then(refresh)}
          {...back}
        />
      )}
    </div>
  );
}
