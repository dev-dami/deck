import type { ReactNode } from "react";
import { cn } from "../lib/cn";
import { type PanelDock, type Prefs, SIDEBAR_WIDTH, type SidebarSide, setPrefs } from "../prefs";
import { Button } from "./kit/button";
import { segmentItem, segmentTrack } from "./kit/segmented";
import { Row } from "./kit/settings";

// Settings > Appearance > Layout: where the sidebar sits and where the
// side panel (Changes, Graph, Tasks and the rest) docks, each drawn as the
// window it makes, then how tight the window is spaced and the sidebar's
// width, which its edge also drags (Sidebar.tsx, Grip).
export function LayoutPicker({ p }: { p: Prefs }) {
  const sides: ReadonlyArray<{ id: SidebarSide; label: string }> = [
    { id: "left", label: "Sidebar on the left" },
    { id: "right", label: "Sidebar on the right" },
  ];
  const docks: ReadonlyArray<{ id: PanelDock; label: string }> = [
    { id: "opposite", label: "Panel opposite" },
    { id: "beside", label: "Panel beside the sidebar" },
    { id: "float", label: "Panel floating" },
  ];
  return (
    <>
      <div className="flex flex-col gap-3 py-3 @md:flex-row @md:gap-6">
        <Group label="Sidebar" hint="Or drag its edge across the window.">
          {sides.map((s) => (
            <Diagram key={s.id} label={s.label} on={p.sidebarSide === s.id} onClick={() => setPrefs({ sidebarSide: s.id })}>
              <Window side={s.id} dock={p.panelDock} />
            </Diagram>
          ))}
        </Group>
        <Group label="Side panel" hint="Where Changes, Graph and Tasks open.">
          {docks.map((d) => (
            <Diagram key={d.id} label={d.label} on={p.panelDock === d.id} onClick={() => setPrefs({ panelDock: d.id })}>
              <Window side={p.sidebarSide} dock={d.id} />
            </Diagram>
          ))}
        </Group>
      </div>
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

// The window a layout makes, in miniature: the sidebar in the accent, the
// room, and the side panel docked or floating over the room.
function Window({ side, dock }: { side: SidebarSide; dock: PanelDock }) {
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
