import { useCallback, useEffect, useRef, useState } from "react";
import type { DeckBridge, LocalDeck, SavedServer } from "../../shared/bridge";
import { useAppearance } from "../appearance";
import { connect, openLocal } from "../connection";
import { forgetOpen, type OpenProject, rememberOpen, restoredOpen } from "../openProjects";
import { usePrefs } from "../prefs";
import { ConnectScreen } from "./ConnectScreen";
import { Shell } from "./Shell";
import type { ProjectEntry } from "./Sidebar";

// The last part of a folder path.
function base(dir: string): string {
  return dir.replace(/[\\/]+$/, "").split(/[\\/]/).pop() || dir;
}

// A paired Deck as the window names it: where it runs, never its address.
function pairedName(s: SavedServer): string {
  return s.tls ? "Paired Deck (another computer)" : "Paired Deck (this computer)";
}

// Where to go once a project is current: a thread, the new-thread screen
// ("new"), or wherever the project was (null).
export type Landing = string | "new" | null;

// Like Claude Desktop, several projects stay open at once: each runs its
// own Deck, the sidebar lists the threads of all of them, and the one whose
// thread is open is current. The open set comes back on the next start.
export function App({ bridge }: { bridge: DeckBridge }) {
  const [servers, setServers] = useState<SavedServer[] | null>(null);
  const [recent, setRecent] = useState<string[]>([]);
  const [deck, setDeck] = useState<string | null>(null);
  const [open, setOpen] = useState<OpenProject[]>([]);
  const [currentKey, setCurrentKey] = useState<string | null>(null);
  const [landing, setLanding] = useState<Landing>(null);
  const [message, setMessage] = useState<string | null>(null);
  // The connect screen over open projects, to add or pair another.
  const [connecting, setConnecting] = useState(false);
  // The folder whose Deck is starting.
  const [opening, setOpening] = useState<string | null>(null);
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
    } else {
      live.current = [...live.current, p];
      setOpen(live.current);
      setCurrentKey(p.key);
      rememberOpen(p.source);
    }
    setLanding(to);
    setMessage(null);
    setConnecting(false);
  }, []);

  const pairedOpen = useCallback(
    (s: SavedServer, to: Landing = null) => {
      const source = `paired:${s.id}`;
      const had = live.current.find((x) => x.source === source);
      if (had !== undefined) {
        setCurrentKey(had.key);
        setLanding(to);
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
        setLanding(to);
        setConnecting(false);
        return;
      }
      add(
        {
          key: `rpc:${d.id}`,
          source,
          name: base(d.dir),
          where: d.dir,
          transport: "Project on this computer",
          c: openLocal(bridge, d),
        },
        to,
      );
    },
    [bridge, add],
  );

  // Starts (or finds) the Deck of a folder; the error text, or null.
  const openFolder = useCallback(
    async (dir: string, to: Landing = null): Promise<string | null> => {
      const had = live.current.find((x) => x.source === `folder:${dir}`);
      if (had !== undefined) {
        setCurrentKey(had.key);
        setLanding(to);
        setConnecting(false);
        return null;
      }
      setOpening(dir);
      const out = await bridge.openFolder(dir);
      setOpening(null);
      void refresh();
      if (!out.ok) return out.message;
      localOpen(out.deck, to);
      return null;
    },
    [bridge, localOpen, refresh],
  );

  // Closes one project: its Deck stops (a folder) or its link ends (paired).
  const close = useCallback((key: string, why: string | null = null) => {
    const p = live.current.find((x) => x.key === key);
    if (p === undefined) return;
    live.current = live.current.filter((x) => x.key !== key);
    setOpen(live.current);
    forgetOpen(p.source);
    setCurrentKey((cur) => (cur === key ? (live.current[0]?.key ?? null) : cur));
    setLanding(null);
    if (why !== null) setMessage(why);
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
      let sources = restoredOpen();
      if (sources.length === 0) {
        const first = d !== null && r[0] !== undefined ? `folder:${r[0]}` : s[0] !== undefined ? `paired:${s[0].id}` : null;
        sources = first === null ? [] : [first];
      }
      let err: string | null = null;
      for (const src of sources) {
        if (src.startsWith("folder:") && d !== null) {
          err = (await openFolder(src.slice("folder:".length))) ?? err;
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
  }, [bridge, openFolder, pairedOpen]);

  // A client resets to "unpaired" when its Deck ends the connection for good
  // (a refused token over v2, a stopped process over rpc) and to "protocol"
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

  return (
    <div className="app h-full" data-app="">
      {current !== null && !connecting ? (
        <Shell
          key={current.key}
          c={current.c}
          name={current.name}
          where={current.where}
          transport={current.transport}
          others={open.filter((p) => p !== current)}
          landing={landing}
          projects={projects}
          onProject={toProject}
          onAddProject={addProject}
          onCloseProject={(source) => {
            const p = open.find((x) => x.source === source);
            if (p !== undefined) close(p.key);
          }}
          onDisconnect={() => close(current.key)}
          onConnect={() => setConnecting(true)}
        />
      ) : servers === null ? (
        <div className="loading grid h-full place-items-center text-muted-foreground text-sm">Loading…</div>
      ) : (
        <ConnectScreen
          bridge={bridge}
          deck={deck}
          servers={servers}
          recent={recent}
          opening={opening}
          message={message}
          onFolder={(dir) => openFolder(dir)}
          onPaired={(s) => {
            void refresh();
            pairedOpen(s);
          }}
          onOpen={(s) => pairedOpen(s)}
          onForget={(s) => void bridge.forgetServer(s.id).then(refresh)}
          {...(current !== null ? { onBack: () => setConnecting(false) } : {})}
        />
      )}
    </div>
  );
}
