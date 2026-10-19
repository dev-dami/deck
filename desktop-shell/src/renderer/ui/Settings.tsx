import type { AppState } from "@web/state";
import { kindColour, kindMark } from "@web/theme";
import { Monitor, Moon, Palette, Sun } from "lucide-react";
import type { ReactNode } from "react";
import { cn } from "../lib/cn";
import { type Prefs, setPrefs, usePrefs } from "../prefs";
import { SETTINGS_SECTIONS, type SettingsSection } from "../ui";
import { Kbd } from "./kit/badge";
import { Button } from "./kit/button";
import { modKey } from "./keys";

// After T3 Code's settings layout: a section title, then a rounded group
// of rows, each with a title, a description and a control on the right.
function Section({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="space-y-2.5">
      <div className="flex min-h-7 items-center px-4">
        <h2 className="text-foreground/70 text-sm">{title}</h2>
      </div>
      <div className="rounded-xl border border-border/60 bg-card/40 shadow-xs/5 [&>*+*]:border-border/50 [&>*+*]:border-t">
        {children}
      </div>
    </section>
  );
}

function Row({ title, description, control }: { title: ReactNode; description?: ReactNode; control?: ReactNode }) {
  return (
    <div className="flex flex-col gap-3 px-4 py-3 sm:flex-row sm:items-center sm:justify-between" data-slot="settings-row">
      <div className="min-w-0 space-y-1">
        <h3 className="font-medium text-sm">{title}</h3>
        {description !== undefined && <p className="text-muted-foreground text-xs leading-relaxed">{description}</p>}
      </div>
      {control !== undefined && <div className="flex shrink-0 items-center gap-2">{control}</div>}
    </div>
  );
}

function Switch({ checked, onChange, label }: { checked: boolean; onChange: (v: boolean) => void; label: string }) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      aria-label={label}
      onClick={() => onChange(!checked)}
      className={cn(
        "relative inline-flex h-5 w-9 shrink-0 cursor-pointer items-center rounded-full transition-colors",
        checked ? "bg-primary" : "bg-input",
      )}
    >
      <span
        className={cn(
          "inline-block size-4 rounded-full bg-white shadow-sm transition-transform",
          checked ? "translate-x-[18px]" : "translate-x-0.5",
        )}
      />
    </button>
  );
}

function Segmented<T extends string>({
  value,
  options,
  onChange,
  label,
}: {
  value: T;
  options: ReadonlyArray<{ value: T; label: string; icon?: ReactNode }>;
  onChange: (v: T) => void;
  label: string;
}) {
  return (
    <div className="inline-flex rounded-lg border bg-background p-0.5" role="radiogroup" aria-label={label}>
      {options.map((o) => (
        <button
          key={o.value}
          type="button"
          role="radio"
          aria-checked={value === o.value}
          onClick={() => onChange(o.value)}
          className={cn(
            "flex h-7 cursor-pointer items-center gap-1.5 rounded-md px-2.5 text-xs",
            value === o.value ? "bg-accent font-medium text-foreground" : "text-muted-foreground hover:text-foreground",
          )}
        >
          {o.icon}
          {o.label}
        </button>
      ))}
    </div>
  );
}

const set =
  <K extends keyof Prefs>(k: K) =>
  (v: Prefs[K]) =>
    setPrefs({ [k]: v } as Partial<Prefs>);

function General({ p }: { p: Prefs }) {
  return (
    <>
      <Section title="Composer">
        <Row
          title="Send with"
          description="Which keys send the message. Shift+Enter always adds a line."
          control={
            <Segmented
              label="Send with"
              value={p.sendKey}
              onChange={set("sendKey")}
              options={[
                { value: "enter", label: "Enter" },
                { value: "mod-enter", label: "Ctrl+Enter" },
              ]}
            />
          }
        />
        <Row
          title="Confirm Yolo mode"
          description="Ask before a thread switches to Yolo, where agents run everything without asking."
          control={<Switch label="Confirm Yolo mode" checked={p.confirmYolo} onChange={set("confirmYolo")} />}
        />
      </Section>
      <Section title="Threads">
        <Row
          title="Fold finished work"
          description='Fold the tool calls and thinking of a finished reply behind "Worked for …".'
          control={<Switch label="Fold finished work" checked={p.foldWork} onChange={set("foldWork")} />}
        />
        <Row
          title="Time format"
          control={
            <Segmented
              label="Time format"
              value={p.clock}
              onChange={set("clock")}
              options={[
                { value: "12h", label: "12-hour" },
                { value: "24h", label: "24-hour" },
              ]}
            />
          }
        />
      </Section>
    </>
  );
}

function Appearance({ p }: { p: Prefs }) {
  return (
    <>
      <Section title="Theme">
        <Row
          title="Color mode"
          description="Deck follows the theme of the Deck you are connected to."
          control={
            <Segmented
              label="Color mode"
              value={p.theme}
              onChange={set("theme")}
              options={[
                { value: "deck", label: "Deck", icon: <Palette className="size-3.5" /> },
                { value: "system", label: "System", icon: <Monitor className="size-3.5" /> },
                { value: "light", label: "Light", icon: <Sun className="size-3.5" /> },
                { value: "dark", label: "Dark", icon: <Moon className="size-3.5" /> },
              ]}
            />
          }
        />
      </Section>
      <Section title="Sidebar">
        <Row
          title="Thread rows"
          description="Cards show the project, the last message and the agents; compact rows show the title only."
          control={
            <Segmented
              label="Thread rows"
              value={p.rows}
              onChange={set("rows")}
              options={[
                { value: "cards", label: "Cards" },
                { value: "compact", label: "Compact" },
              ]}
            />
          }
        />
      </Section>
    </>
  );
}

function Agents({ state }: { state: AppState }) {
  const kinds = state.server?.kinds ?? [];
  return (
    <>
      <Section title="Agent kinds">
        {kinds.length === 0 && <Row title="No agents" description="This Deck names no agent kinds." />}
        {kinds.map((k) => (
          <Row
            key={k}
            title={
              <span className="flex items-center gap-2">
                <span className="grid size-5 place-items-center rounded-md bg-secondary font-mono text-xs" style={{ color: kindColour(k) }}>
                  {kindMark(state.theme, k) || k.slice(0, 1)}
                </span>
                {k}
              </span>
            }
            description={
              <>
                Type <code className="font-mono">${k}</code> to reach a free {k}, <code className="font-mono">${k}+</code> to
                start another, <code className="font-mono">${k}*</code> to reach all of them.
              </>
            }
          />
        ))}
      </Section>
      <Section title="Permissions">
        <Row
          title="Yolo mode"
          description={
            state.server?.allow_yolo === true
              ? "Allowed: a thread may run every command and edit without asking."
              : "Not allowed for this window: the paired Deck has to allow it."
          }
        />
        <Row title="Models" description="Each agent's model and effort are picked per thread, from the composer's agent picker." />
      </Section>
    </>
  );
}

function Connection({ state, where, transport, onDisconnect }: { state: AppState; where: string; transport: string; onDisconnect: () => void }) {
  const s = state.server;
  return (
    <>
      <Section title="This Deck">
        <Row title="Project" description={s?.project ?? "—"} />
        <Row title="Where" description={where} />
        <Row title="Transport" description={transport} />
        <Row title="Status" description={state.link} />
        <Row
          title="Switch Deck"
          description="Close this Deck and open another folder or pairing."
          control={
            <Button size="sm" variant="outline" onClick={onDisconnect}>
              Switch Deck
            </Button>
          }
        />
      </Section>
      <Section title="About">
        <Row title="Deck version" description={s?.version ?? "—"} />
        <Row title="Protocol" description={s === null ? "—" : String(s.protocol)} />
        <Row title="Features" description={s === null || s.features.length === 0 ? "—" : s.features.join(", ")} />
      </Section>
    </>
  );
}

function Keybindings({ mac }: { mac: boolean }) {
  const m = modKey(mac).trim();
  const rows: ReadonlyArray<[string, string[]]> = [
    ["Search threads and commands", [m, "K"]],
    ["New thread", [m, "N"]],
    ["Send the message", ["Enter"]],
    ["New line", ["Shift", "Enter"]],
    ["Pick an agent", ["$"]],
    ["Rename a thread", ["Double-click"]],
  ];
  return (
    <Section title="Keyboard shortcuts">
      {rows.map(([what, keys]) => (
        <Row
          key={what}
          title={what}
          control={
            <span className="flex items-center gap-1">
              {keys.map((k) => (
                <Kbd key={k}>{k}</Kbd>
              ))}
            </span>
          }
        />
      ))}
    </Section>
  );
}

export function SettingsPage({
  section,
  state,
  where,
  transport,
  mac,
  onDisconnect,
}: {
  section: SettingsSection;
  state: AppState;
  where: string;
  transport: string;
  mac: boolean;
  onDisconnect: () => void;
}) {
  const p = usePrefs();
  const label = SETTINGS_SECTIONS.find((x) => x.section === section)?.label ?? "Settings";
  return (
    <div className="settings flex min-h-0 flex-1 flex-col" data-settings={section}>
      <header className="topbar drag flex h-12 shrink-0 items-center gap-2 border-b px-5 text-sm">
        <span className="text-muted-foreground">Settings</span>
        <span className="text-muted-foreground/50">/</span>
        <h1 className="title font-medium">{label}</h1>
      </header>
      <div className="min-h-0 flex-1 overflow-y-auto">
        <div className="mx-auto flex w-full max-w-3xl flex-col gap-8 px-6 py-8">
          {section === "general" && <General p={p} />}
          {section === "appearance" && <Appearance p={p} />}
          {section === "agents" && <Agents state={state} />}
          {section === "connection" && (
            <Connection state={state} where={where} transport={transport} onDisconnect={onDisconnect} />
          )}
          {section === "keybindings" && <Keybindings mac={mac} />}
        </div>
      </div>
    </div>
  );
}
