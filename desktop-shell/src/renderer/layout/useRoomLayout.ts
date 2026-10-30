import { type RefObject, useCallback, useEffect, useRef, useState } from "react";
import type { ProjectApi } from "../project";
import { PANELS, type Panel } from "../ui";
import { closeTerm, focusTerm, pruneTerms } from "../ui/Terminal";
import type { LayoutMenuProps } from "./LayoutMenu";
import { useLayout, type LayoutControl } from "./store";
import {
  activate,
  applyPreset,
  CONVERSATION,
  dock,
  type Edge,
  isPanel,
  isTerminal,
  type Layout,
  type PaneId,
  presetShows,
  removePane,
  setHidden,
  splitPane,
  stackOf,
  terminalId,
  terminalsOf,
  toggleZoom,
} from "./tree";
import { shownPanes, type PaneOps } from "./Workspace";

// The room's layout wired to the window: which panes show (the terminal's
// toggle and the side panel's, as before, now over the layout's panes),
// the verbs of the panes and the Layout menu, and the keys:
//   Ctrl/⌘+Shift+\   split the focused pane right with a new terminal
//   Ctrl/⌘+Shift+-   split it down
//   Ctrl/⌘+Shift+M   zoom it, or back
//   Ctrl/⌘+Shift+[ ] the pane before or after it
// None of them is taken: the shell's own keys use Shift with Y and G only,
// the composer's with Enter, and the menu's zoom is Ctrl/⌘ with - alone.

export interface RoomLayout {
  ctl: LayoutControl;
  visible: (p: PaneId) => boolean;
  ops: PaneOps;
  focused: PaneId | null;
  ring: boolean;
  // A pane took the focus; a click there ends the keys' mark.
  onFocused: (p: PaneId, pointer: boolean) => void;
  // The side panel and the terminal, as the header's buttons and the
  // commands drive them.
  openPanel: (p: Panel) => void;
  panelAction: (p: Panel | null) => void;
  togglePanel: () => void;
  toggleTerminal: () => void;
  // What the header shows as on.
  panelShown: boolean;
  diffShown: boolean;
  menu: Omit<LayoutMenuProps, "mac">;
}

export function useRoomLayout({
  project,
  side,
  api,
  terminal,
  setTerminal,
  panel,
  setPanel,
  panelsOn,
  available,
  inputRef,
  keys,
}: {
  // The project's key: a layout of its own is kept under it.
  project: string;
  side: "left" | "right";
  api: ProjectApi | undefined;
  terminal: boolean;
  setTerminal: (v: boolean) => void;
  // The side panel's open view (the project's UI state), and how to set it.
  panel: Panel | null;
  setPanel: (p: Panel | null) => void;
  // The side panel may show (not in a teammate's room).
  panelsOn: boolean;
  available: (p: Panel) => boolean;
  inputRef: RefObject<HTMLTextAreaElement | null>;
  // The keys listen while the room shows.
  keys: boolean;
}): RoomLayout {
  const ctl = useLayout(project, side);
  const { layout, edit } = ctl;
  const [focused, setFocused] = useState<PaneId | null>(null);
  const [ring, setRing] = useState(false);

  const visible = useCallback(
    (p: PaneId) =>
      p === CONVERSATION || (isTerminal(p) ? api !== undefined && terminal : isPanel(p) && panelsOn && panel !== null && available(p) && !layout.hidden.includes(p)),
    [api, terminal, panelsOn, panel, available, layout.hidden],
  );
  const shown = shownPanes(layout, visible);
  const terms = terminalsOf(layout.root);

  // The shells of panes the layout no longer has end.
  const termKey = terms.join(" ");
  useEffect(() => pruneTerms(api, new Set(termKey.split(" "))), [api, termKey]);

  // A pane about to show is not left behind a zoom on another stack.
  const unzoomFor = (l: Layout, p: PaneId): Layout =>
    l.zoom !== null && stackOf(l.root, l.zoom)?.panes.includes(p) !== true ? { ...l, zoom: null } : l;

  const focusPane = (p: PaneId, keys = false) => {
    setFocused(p);
    setRing(keys);
    edit((l) => activate(l, p));
    requestAnimationFrame(() => {
      if (p === CONVERSATION) {
        inputRef.current?.focus();
        return;
      }
      if (isTerminal(p) && focusTerm(api, p)) return;
      const host = document.querySelector<HTMLElement>(`[data-pane-host="${CSS.escape(p)}"]`);
      host?.querySelector<HTMLElement>("button, [href], input, textarea, [tabindex]:not([tabindex='-1'])")?.focus();
    });
  };

  const openPanel = (p: Panel) => {
    edit((l) => activate(unzoomFor(setHidden(l, p, false), p), p));
    if (panel !== p) setPanel(p);
  };
  const closePanels = () => {
    if (panel !== null) setPanel(null);
  };
  // A view's toggle (the palette, Diff): open, or close the side panel when
  // it is the view on screen.
  const panelAction = (p: Panel | null) => {
    if (p === null) closePanels();
    else if (shown.includes(p)) closePanels();
    else openPanel(p);
  };
  const panelShown = shown.some(isPanel);
  const togglePanel = () => (panelShown ? closePanels() : openPanel(panel ?? "changes"));

  // Showing the terminal starts one where it was, or under the conversation
  // when the layout has none.
  const showTerminal = () => {
    const first = terms[0];
    if (first === undefined) {
      const id = terminalId();
      edit((l) => unzoomFor(splitPane(l, CONVERSATION, "bottom", id), id));
      setFocused(id);
    } else {
      edit((l) => unzoomFor(l, first));
      setFocused(first);
    }
    setTerminal(true);
  };
  const toggleTerminal = () => (terminal ? setTerminal(false) : showTerminal());

  const ops: PaneOps = {
    activate: (p) => {
      setFocused(p);
      if (isPanel(p) && panel !== p) setPanel(p);
    },
    close: (p) => {
      if (isTerminal(p)) {
        closeTerm(api, p);
        // The last terminal keeps its place; a new shell starts there when
        // the terminal shows again.
        if (terms.length > 1) edit((l) => removePane(l, p));
        else setTerminal(false);
        return;
      }
      if (!isPanel(p)) return;
      edit((l) => setHidden(l, p, true));
      if (!shown.some((q) => q !== p && isPanel(q))) closePanels();
    },
    hide: (g) => (g === "panel" ? closePanels() : setTerminal(false)),
    newTerminal: (stackId) => {
      const id = terminalId();
      edit((l) => dock(l, id, stackId, "center"));
      setFocused(id);
    },
    split: (p, edge: Edge) => {
      const id = terminalId();
      edit((l) => unzoomFor(splitPane(l, p, edge, id), id));
      setTerminal(true);
      focusPane(id, true);
    },
    zoom: (p) => edit((l) => toggleZoom(l, p)),
    focus: (p) => focusPane(p),
  };

  // The keys, in the capture phase so a terminal does not take them first.
  const latest = useRef({ ops, focused, shown, focusPane });
  latest.current = { ops, focused, shown, focusPane };
  useEffect(() => {
    if (!keys) return;
    const onKey = (e: KeyboardEvent) => {
      if (!(e.ctrlKey || e.metaKey) || !e.shiftKey || e.altKey) return;
      const { ops: o, focused: f, shown: s, focusPane: go } = latest.current;
      const at = f !== null && s.includes(f) ? f : CONVERSATION;
      const run =
        e.code === "Backslash"
          ? () => o.split(at, "right")
          : e.code === "Minus"
            ? () => o.split(at, "bottom")
            : e.code === "KeyM"
              ? () => o.zoom(at)
              : e.code === "BracketLeft" || e.code === "BracketRight"
                ? () => {
                    const i = s.indexOf(at);
                    const n = s.length;
                    const next = s[(i + (e.code === "BracketRight" ? 1 : n - 1)) % n];
                    if (next !== undefined) go(next, true);
                  }
                : null;
      if (run === null) return;
      e.preventDefault();
      e.stopPropagation();
      run();
    };
    window.addEventListener("keydown", onKey, true);
    return () => window.removeEventListener("keydown", onKey, true);
  }, [keys]);

  const menu: Omit<LayoutMenuProps, "mac"> = {
    panes: [
      ...(api === undefined ? [] : [{ id: terms[0] ?? "term:1", label: "Terminal", shown: terminal }]),
      ...PANELS.filter((x) => available(x.panel)).map((x) => ({ id: x.panel, label: x.label, shown: visible(x.panel) })),
    ],
    onPane: (id, show) => {
      if (isTerminal(id)) return show ? showTerminal() : setTerminal(false);
      if (!isPanel(id)) return;
      if (show) openPanel(id);
      else ops.close(id);
    },
    onPreset: (id) => {
      edit((l) => applyPreset(l, id, side));
      const s = presetShows(id);
      if (s.terminal === true && api !== undefined) setTerminal(true);
      if (s.terminal === false) setTerminal(false);
      if (s.panel === null) closePanels();
      else if (s.panel !== undefined && panel !== s.panel) setPanel(s.panel);
    },
    onSplit: (edge) => ops.split(focused !== null && shown.includes(focused) ? focused : CONVERSATION, edge),
    zoomed: layout.zoom !== null,
    onZoom: () => ops.zoom(layout.zoom ?? (focused !== null && shown.includes(focused) ? focused : CONVERSATION)),
    own: ctl.own,
    onOwn: ctl.setOwn,
    onReset: ctl.reset,
  };

  return {
    ctl,
    visible,
    ops,
    focused,
    ring,
    onFocused: (p, pointer) => {
      setFocused(p);
      if (pointer) setRing(false);
    },
    openPanel,
    panelAction,
    togglePanel,
    toggleTerminal,
    panelShown,
    diffShown: shown.includes("changes"),
    menu,
  };
}
