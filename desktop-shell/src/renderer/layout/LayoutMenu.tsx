import { useState } from "react";
import { Check, ColumnsPlusRight, CornersIn, CornersOut, PanesIcon, RotateCcw, RowsPlusBottom } from "../ui/icons";
import { Button } from "../ui/kit/button";
import { MenuItem, MenuLabel, MenuSeparator, Popover } from "../ui/kit/menu";
import { PRESETS, type PaneId, type Preset } from "./tree";

// The Layout menu, in the room's toolbar: the presets, which panes show,
// the focused pane's split and zoom, a layout of this project's own, and
// Reset. A pane hidden here keeps its place for when it shows again.
export interface LayoutMenuProps {
  mac: boolean;
  // The panes the menu can show or hide, with whether each shows now.
  panes: ReadonlyArray<{ id: PaneId; label: string; shown: boolean }>;
  onPane: (id: PaneId, show: boolean) => void;
  onPreset: (p: Preset) => void;
  onSplit: (edge: "right" | "bottom") => void;
  zoomed: boolean;
  onZoom: () => void;
  own: boolean;
  onOwn: (on: boolean) => void;
  onReset: () => void;
}

export function LayoutMenu(p: LayoutMenuProps) {
  const [open, setOpen] = useState(false);
  const run = (f: () => void) => () => {
    setOpen(false);
    f();
  };
  const m = p.mac ? "⌘" : "Ctrl+";
  return (
    <Popover
      open={open}
      onClose={() => setOpen(false)}
      align="end"
      label="Layout"
      className="layout-menu max-h-[min(36rem,calc(100vh-5rem))] w-64 overflow-y-auto"
      trigger={
        <Button variant="ghost-muted" size="icon-sm" aria-label="Layout" title="Layout" aria-expanded={open} onClick={() => setOpen((o) => !o)}>
          <PanesIcon weight={open ? "fill" : "regular"} />
        </Button>
      }
    >
      <MenuLabel>Presets</MenuLabel>
      {PRESETS.map((x) => (
        <MenuItem key={x.id} onClick={run(() => p.onPreset(x.id))} title={x.hint} data-preset={x.id}>
          <span className="flex-1">{x.label}</span>
        </MenuItem>
      ))}
      <MenuSeparator />
      <MenuLabel>Panes</MenuLabel>
      {p.panes.map((x) => (
        <MenuItem key={x.id} role="menuitemcheckbox" aria-checked={x.shown} onClick={run(() => p.onPane(x.id, !x.shown))}>
          <span className="grid size-3.5 place-items-center">{x.shown && <Check />}</span>
          <span className="flex-1">{x.label}</span>
        </MenuItem>
      ))}
      <MenuSeparator />
      <MenuItem onClick={run(() => p.onSplit("right"))}>
        <ColumnsPlusRight /> <span className="flex-1">Split right</span>
        <span className="text-2xs text-faint">{m}⇧\</span>
      </MenuItem>
      <MenuItem onClick={run(() => p.onSplit("bottom"))}>
        <RowsPlusBottom /> <span className="flex-1">Split down</span>
        <span className="text-2xs text-faint">{m}⇧-</span>
      </MenuItem>
      <MenuItem onClick={run(p.onZoom)}>
        {p.zoomed ? <CornersIn /> : <CornersOut />} <span className="flex-1">{p.zoomed ? "Unzoom" : "Zoom the focused pane"}</span>
        <span className="text-2xs text-faint">{m}⇧M</span>
      </MenuItem>
      <MenuSeparator />
      <MenuItem role="menuitemcheckbox" aria-checked={p.own} onClick={run(() => p.onOwn(!p.own))} title="This project keeps a layout of its own; the others share one">
        <span className="grid size-3.5 place-items-center">{p.own && <Check />}</span>
        <span className="flex-1">Only this project</span>
      </MenuItem>
      <MenuItem onClick={run(p.onReset)}>
        <RotateCcw /> Reset layout
      </MenuItem>
    </Popover>
  );
}
