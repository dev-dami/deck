import type { ReactNode } from "react";
import { cn } from "../lib/cn";
import { resetGlobalLayout } from "../layout/store";
import { type PanelDock, type Prefs, SIDEBAR_WIDTH, type SidebarSide, setPrefs } from "../prefs";
import { Button } from "./kit/button";
import { segmentItem, segmentTrack } from "./kit/segmented";
import { Row } from "./kit/settings";

// Settings > Appearance > Layout: where the sidebar sits (a column on the
// left or the right, or the threads as a bar across the top) and where the
// side panel (Changes, Graph, Tasks and the rest) docks, each drawn as the
// window it makes, then where the pages open, how tight the window is
// spaced and the sidebar's width, which its edge also drags (Sidebar.tsx,
// Grip).
export function LayoutPicker({ p }: { p: Prefs }) {
  const sides: ReadonlyArray<{ id: SidebarSide; label: string }> = [
    { id: "left", label: "Sidebar on the left" },
    { id: "right", label: "Sidebar on the right" },
    { id: "top", label: "Threads in a top bar" },
  ];
  const docks: ReadonlyArray<{ id: PanelDock; label: string }> = [
    { id: "opposite", label: "Panel opposite" },
    { id: "beside", label: "Panel beside the sidebar" },
    { id: "float", label: "Panel floating" },
  ];
  return (
    <>
      <div className="flex flex-col gap-3 py-3 @md:flex-row @md:gap-6">
        <Group label="Sidebar" hint="Or drag its edge across the window. The top bar lists the threads as tabs.">
          {sides.map((s) => (
            <Diagram key={s.id} label={s.label} on={p.sidebarSide === s.id} onClick={() => setPrefs({ sidebarSide: s.id })}>
              <Window side={s.id} dock={p.panelDock} />
            </Diagram>
          ))}
        </Group>
        <Group label="Side panel" hint="Where Changes, Graph and Tasks open. The room's layout goes back to Default.">
          {docks.map((d) => (
            <Diagram
              key={d.id}
              label={d.label}
              on={p.panelDock === d.id}
              onClick={() => {
                setPrefs({ panelDock: d.id });
                resetGlobalLayout();
              }}
            >
              <Window side={p.sidebarSide} dock={d.id} />
            </Diagram>
          ))}
        </Group>
      </div>
      <Row
        title="Pages"
        description="Where Settings, Usage, Team and the inbox open from the sidebar. Either way a page can move: its corner button puts it at the side, and at the side a button makes it full size."
        control={
          <div className={cn(segmentTrack(), "inline-grid auto-cols-fr grid-flow-col")} role="radiogroup" aria-label="Pages">
            {(["full", "side"] as const).map((d) => (
              <button
                key={d}
                type="button"
                role="radio"
                aria-checked={p.pages === d}
                onClick={() => setPrefs({ pages: d })}
                className={segmentItem({ on: p.pages === d })}
              >
                {d === "full" ? "Full window" : "At the side"}
              </button>
            ))}
          </div>
        }
      />
      <Row
        title="Density"
        description="Compact tightens the padding and the gaps everywhere; the text keeps its size."
        control={
          <div className={cn(segmentTrack(), "inline-grid auto-cols-fr grid-flow-col")} role="radiogroup" aria-label="Density">
            {(["comfortable", "compact"] as const).map((d) => (
              <button
                key={d}
                type="button"
                role="radio"
                aria-checked={p.density === d}
                onClick={() => setPrefs({ density: d })}
                className={segmentItem({ on: p.density === d })}
              >
                {d === "comfortable" ? "Comfortable" : "Compact"}
              </button>
            ))}
          </div>
        }
      />
      {/* The top bar has no width to set; the column's waits for it. */}
      {p.sidebarSide !== "top" && (
        <Row
          title="Sidebar width"
          description={`${p.sidebarWidth}px. Drag the sidebar's edge, or focus it and use the arrow keys.`}
          control={
            <Button
              size="sm"
              variant="outline"
              disabled={p.sidebarWidth === SIDEBAR_WIDTH.default}
              onClick={() => setPrefs({ sidebarWidth: SIDEBAR_WIDTH.default })}
            >
              Reset
            </Button>
          }
        />
      )}
    </>
  );
}

function Group({ label, hint, children }: { label: string; hint: string; children: ReactNode }) {
  return (
    <div className="flex min-w-0 flex-col gap-2">
      <div>
        <h3 className="text-[13px]">{label}</h3>
        <p className="text-2xs text-faint">{hint}</p>
      </div>
      <div className="flex gap-2" role="radiogroup" aria-label={label}>
        {children}
      </div>
    </div>
  );
}

function Diagram({ label, on, onClick, children }: { label: string; on: boolean; onClick: () => void; children: ReactNode }) {
  return (
    <button
      type="button"
      role="radio"
      aria-checked={on}
      aria-label={label}
      title={label}
      onClick={onClick}
      className={cn(
        "relative block h-14 w-22 shrink-0 cursor-pointer overflow-hidden rounded-lg bg-(--backdrop) outline-none ring-1 ring-hairline transition-[box-shadow,transform] duration-200 hover:-translate-y-px focus-visible:ring-2 focus-visible:ring-ring",
        on && "ring-2 ring-primary ring-offset-2 ring-offset-background",
      )}
    >
      {children}
    </button>
  );
}

// The window a layout makes, in miniature: the sidebar in the accent (a
// column, or a bar across the top with its tabs), the room, and the side
// panel docked or floating over the room.
function Window({ side, dock }: { side: SidebarSide; dock: PanelDock }) {
  if (side === "top") return <TopWindow dock={dock} />;
  const sidebar = <span className="w-[24%] rounded-[3px] bg-primary/55" />;
  const room = <span className="flex-1 rounded-[3px] bg-surface-1" />;
  const panel = <span className="w-[26%] rounded-[3px] bg-faint/45" />;
  const docked = dock === "float" ? [sidebar, room] : dock === "beside" ? [sidebar, panel, room] : [sidebar, room, panel];
  return (
    <span aria-hidden className={cn("absolute inset-0 flex gap-[3px] p-[3px]", side === "right" && "flex-row-reverse")}>
      {docked.map((el, i) => (
        <span key={i} className="contents">
          {el}
        </span>
      ))}
      {dock === "float" && (
        <span className={cn("absolute inset-y-[7px] w-[24%] rounded-[3px] bg-faint/60 shadow-sm", side === "left" ? "right-[7px]" : "left-[7px]")} />
      )}
    </span>
  );
}

// The threads' top bar in miniature: the bar in the accent with a tab lit
// and two at rest, and under it the room with the panel as the bar docks
// it (opposite on the right, beside on the left).
function TopWindow({ dock }: { dock: PanelDock }) {
  const panel = <span className="w-[28%] rounded-[3px] bg-faint/45" />;
  return (
    <span aria-hidden className="absolute inset-0 flex flex-col gap-[3px] p-[3px]">
      <span className="flex h-[11px] shrink-0 items-center gap-[2px] rounded-[3px] bg-primary/55 px-[3px]">
        <span className="h-[5px] w-[14px] rounded-[2px] bg-background/75" />
        <span className="h-[5px] w-[10px] rounded-[2px] bg-background/30" />
        <span className="h-[5px] w-[10px] rounded-[2px] bg-background/30" />
      </span>
      <span className="relative flex min-h-0 flex-1 gap-[3px]">
        {dock === "beside" && panel}
        <span className="flex-1 rounded-[3px] bg-surface-1" />
        {dock === "opposite" && panel}
        {dock === "float" && <span className="absolute inset-y-[4px] right-[4px] w-[26%] rounded-[3px] bg-faint/60 shadow-sm" />}
      </span>
    </span>
  );
}
