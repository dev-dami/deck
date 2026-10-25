import type { AppState } from "@web/state";
import { kindColour, kindMark } from "@web/theme";
import { Monitor, Moon, Palette, Sun } from "lucide-react";
import { type ReactNode, useEffect, useState } from "react";
import type { AppInfo, DeckBridge } from "../../shared/bridge";
import { cn } from "../lib/cn";
import { type Prefs, setPrefs, usePrefs } from "../prefs";
import { ACCENTS, THEMES } from "../themes";
import { SETTINGS_SECTIONS, type SettingsSection } from "../ui";
import { Kbd } from "./kit/badge";
import { Button } from "./kit/button";
import { Row, Section } from "./kit/settings";
import { PhonePage } from "./Phone";
import { modKey } from "./keys";

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
    <div className="inline-flex rounded-lg border border-hairline bg-canvas p-0.5" role="radiogroup" aria-label={label}>
      {options.map((o) => (
        <button
          key={o.value}
          type="button"
          role="radio"
          aria-checked={value === o.value}
          onClick={() => onChange(o.value)}
          className={cn(
            "flex h-7 cursor-pointer items-center gap-1.5 rounded-md px-2.5 text-xs",
            value === o.value ? "bg-selected text-foreground inset-shadow-hl" : "text-muted-foreground hover:text-foreground",
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
          description="Light or dark for Tabletop and T3: Deck's own, the system's, or yours. The other themes have their own."
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
      <Section title="Theme catalog">
        <div className="grid grid-cols-2 gap-2 p-3 sm:grid-cols-4" role="radiogroup" aria-label="Theme">
          {THEMES.map((t) => (
            <button
              key={t.id}
              type="button"
              role="radio"
              aria-checked={p.palette === t.id}
              onClick={() => setPrefs({ palette: t.id })}
              className={cn(
                "flex cursor-pointer flex-col gap-2 rounded-lg border border-hairline p-2 text-left text-xs outline-none transition-colors hover:bg-hover focus-visible:ring-2 focus-visible:ring-ring",
                p.palette === t.id && "border-foreground/40 bg-selected",
              )}
            >
              <span className="flex h-10 overflow-hidden rounded-md border border-hairline" style={{ background: t.swatch[0] }}>
                <span className="w-1/3" style={{ background: t.swatch[1] }} />
                <span className="flex flex-1 flex-col justify-center gap-1 px-1.5">
                  <span className="h-1 w-3/4 rounded-full" style={{ background: t.swatch[2] }} />
                  <span className="h-1 w-1/2 rounded-full" style={{ background: t.swatch[3] }} />
                </span>
              </span>
              <span className="font-medium">{t.label}</span>
            </button>
          ))}
        </div>
        <Row
          title="Accent"
          description="The color of buttons, links and highlights."
          control={
            <div className="flex items-center gap-1.5" role="radiogroup" aria-label="Accent">
              {ACCENTS.map((a) => (
                <button
                  key={a.id}
                  type="button"
                  role="radio"
                  aria-checked={p.accent === a.id}
                  aria-label={a.label}
                  title={a.label}
                  onClick={() => setPrefs({ accent: a.id })}
                  className={cn(
                    "grid size-6 cursor-pointer place-items-center rounded-full border text-2xs",
                    p.accent === a.id && "ring-2 ring-foreground/60 ring-offset-1 ring-offset-background",
                  )}
                  style={a.color === "" ? undefined : { background: a.color }}
                >
                  {a.color === "" ? "T" : ""}
                </button>
              ))}
            </div>
          }
        />
      </Section>
      <Section title="Sidebar">
        <Row
          title="Threads"
          description="Under their project folders, or in one list across projects, newest first."
          control={
            <Segmented
              label="Threads"
              value={p.grouping}
              onChange={set("grouping")}
              options={[
                { value: "projects", label: "By project" },
                { value: "recent", label: "By recent" },
              ]}
            />
          }
        />
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

function Connection({
  state,
  where,
  transport,
  bridge,
  onDisconnect,
}: {
  state: AppState;
  where: string;
  transport: string;
  bridge: DeckBridge;
  onDisconnect: () => void;
}) {
  const s = state.server;
  const [app, setApp] = useState<AppInfo | null>(null);
  useEffect(() => {
    void bridge.appInfo().then(setApp);
  }, [bridge]);
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
        <Row title="App version" description={app?.version ?? "—"} />
        <Row title="Deck version" description={s?.version ?? "—"} />
        <Row title="Electron" description={app === null ? "—" : `${app.electron} (Chromium ${app.chromium}, ${app.platform})`} />
        <Row title="Protocol" description={s === null ? "—" : String(s.protocol)} />
        <Row title="Features" description={s === null || s.features.length === 0 ? "—" : s.features.join(", ")} />
        <Row
          title="Logs"
          description="What the app and its engines wrote, for a bug report."
          control={
            <Button size="sm" variant="outline" onClick={() => void bridge.openLogs()}>
              Open logs folder
            </Button>
          }
        />
      </Section>
    </>
  );
}

function Keybindings({ mac }: { mac: boolean }) {
  const m = modKey(mac).trim();
  const rows: ReadonlyArray<[string, string[]]> = [
    ["Search threads and commands", [m, "K"]],
    ["New thread", [m, "N"]],
    ["Open a folder", [m, "O"]],
    ["Settings", [m, ","]],
    ["Toggle the sidebar", [m, "B"]],
    ["Toggle the terminal", [m, "J"]],
    ["Zoom in, out, actual size", [m, "=", "-", "0"]],
    ["Send the message", ["Enter"]],
    ["New line", ["Shift", "Enter"]],
    ["Toggle Yolo mode", [m, "Shift", "Y"]],
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
  bridge,
  folder,
  onDisconnect,
}: {
  section: SettingsSection;
  state: AppState;
  where: string;
  transport: string;
  mac: boolean;
  bridge: DeckBridge;
  // The folder of the open project when it is a local one, which a phone link shares.
  folder: string | null;
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
            <Connection state={state} where={where} transport={transport} bridge={bridge} onDisconnect={onDisconnect} />
          )}
          {section === "phone" && <PhonePage bridge={bridge} folder={folder} />}
          {section === "keybindings" && <Keybindings mac={mac} />}
        </div>
      </div>
    </div>
  );
}
