import type { AppState } from "@web/state";
import { kindMark } from "@web/theme";
import { ChevronRight, CircleCheck, Monitor, Moon, Palette, Sun } from "./icons";
import { type ReactNode, useContext, useEffect, useState } from "react";
import type { AppInfo, DeckBridge } from "../../shared/bridge";
import { serves } from "../client";
import { cn } from "../lib/cn";
import { NOTIFY_KINDS } from "../notifications";
import { type Prefs, setPrefs, usePrefs } from "../prefs";
import { ACCENTS, DIFF_THEMES } from "../themes";
import { AppIconPicker } from "./AppIconPicker";
import { SETTINGS_SECTIONS, type SettingsSection } from "../ui";
import { Kbd } from "./kit/badge";
import { Button } from "./kit/button";
import { DiffView, LineComments } from "./DiffView";
import { segmentItem, segmentTrack } from "./kit/segmented";
import { Row, Section } from "./kit/settings";
import { Switch } from "./kit/switch";
import { PhonePage } from "./Phone";
import { ProfilesDeck, ProfilesEditor } from "./ProfilesEditor";
import { modKey } from "./keys";
import { ThemeGrid } from "./ThemeCard";
import { WallpaperPicker } from "./WallpaperPicker";
import { AgentGlyph } from "./Welcome";

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
    <div
      className={cn(segmentTrack(), "segmented inline-grid auto-cols-fr grid-flow-col")}
      role="radiogroup"
      aria-label={label}
    >
      {options.map((o) => (
        <button
          key={o.value}
          type="button"
          role="radio"
          aria-checked={value === o.value}
          onClick={() => onChange(o.value)}
          className={segmentItem({ on: value === o.value })}
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

// What each section holds, for the page's head.
const LINES: Record<SettingsSection, string> = {
  general: "How messages send, how threads read, and when Deck lets you know.",
  appearance: "The theme and its accent, how diffs look, and how much moves.",
  agents: "The agents this Deck runs, and what they may do on their own.",
  connection: "The Deck this window drives, and the versions behind it.",
  phone: "Drive this Deck from the phone app, over your Wi-Fi.",
  keybindings: "Every shortcut in the window, by where you use it.",
};

// The page's head: the section's name, large, and one line on what is in it.
function Head({ section, label }: { section: SettingsSection; label: string }) {
  return (
    <div className="flex animate-enter flex-col gap-1">
      <h2 className="text-balance font-semibold text-2xl tracking-[-0.03em]">{label}</h2>
      <p className="text-pretty text-muted-foreground text-sm">{LINES[section]}</p>
    </div>
  );
}

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
      <Section title="Notifications">
        <Row
          title="Notifications"
          description="Tell me what happens in my rooms: in the bell at the foot of the sidebar, on the app's icon, and as system notifications."
          control={<Switch label="Notifications" checked={p.notifications} onChange={set("notifications")} />}
        />
        {p.notifications && (
          <>
            <Row
              title="Only while Deck is in the background"
              description="Off, a room you are not looking at notifies you too while Deck is in front."
              control={<Switch label="Only while Deck is in the background" checked={p.notifyUnfocusedOnly} onChange={set("notifyUnfocusedOnly")} />}
            />
            <Row
              title="Sound"
              description="Play the system sound with each notification."
              control={<Switch label="Sound" checked={p.sound} onChange={set("sound")} />}
            />
            {NOTIFY_KINDS.map((k) => (
              <Row
                key={k.id}
                title={k.label}
                description={k.description}
                control={<Switch label={k.label} checked={p.notifyKinds[k.id]} onChange={(on) => setPrefs({ notifyKinds: { ...p.notifyKinds, [k.id]: on } })} />}
              />
            ))}
          </>
        )}
      </Section>
    </>
  );
}

const MULTICOLOR = "conic-gradient(#ff3b30, #ff9500, #ffcc00, #28cd41, #007aff, #af52de, #ff2d55, #ff3b30)";

// The accents as beads: the colour under a highlight, with the
// theme's own as the many-coloured bead, as on a Mac. The chosen one holds
// a white dot and a ring; its name shows under the row.
function Accents({ p }: { p: Prefs }) {
  const chosen = ACCENTS.find((a) => a.id === p.accent) ?? ACCENTS[0];
  return (
    <div className="flex flex-col items-start gap-1.5 @md:items-end">
      <div className="flex items-center gap-2" role="radiogroup" aria-label="Accent">
        {ACCENTS.map((a) => {
          const on = p.accent === a.id;
          const fill = a.color === "" ? MULTICOLOR : a.color;
          return (
            <button
              key={a.id}
              type="button"
              role="radio"
              aria-checked={on}
              aria-label={a.label}
              title={a.label}
              onClick={() => setPrefs({ accent: a.id })}
              className={cn(
                "grid size-[22px] cursor-pointer place-items-center rounded-full outline-none transition-transform duration-200 hover:scale-110 active:scale-95 focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background",
                on && "ring-2 ring-foreground/30 ring-offset-2 ring-offset-background",
              )}
              style={{
                background: `linear-gradient(180deg, rgb(255 255 255 / 0.38), rgb(255 255 255 / 0) 58%), ${fill}`,
                boxShadow: "inset 0 0 0 0.5px rgb(0 0 0 / 0.18), inset 0 1px 0 rgb(255 255 255 / 0.4)",
              }}
            >
              {on && <span aria-hidden className="size-2 rounded-full bg-white shadow-[0_0_2px_rgb(0_0_0/0.3)]" />}
            </button>
          );
        })}
      </div>
      <span className="text-2xs text-faint" aria-hidden>
        {chosen?.id === "theme" ? "The theme's own" : chosen?.label}
      </span>
    </div>
  );
}

function Appearance({ p }: { p: Prefs }) {
  return (
    <>
      <Section title="Theme">
        <ThemeGrid palette={p.palette} />
        <WallpaperPicker />
        <div className="flex flex-col gap-0.5 pt-3">
          <h3 className="text-[13px]">App icon</h3>
          <p className="text-pretty text-muted-foreground text-xs leading-relaxed">The theme's own, or one you pick.</p>
          <AppIconPicker className="-mx-3" />
        </div>
        <Row
          title="Color mode"
          description="Light or dark for the themes that have both: Deck's own, the system's, or yours."
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
        <Row title="Accent" description="The color of buttons, links and highlights." control={<Accents p={p} />} />
      </Section>
      <Diffs p={p} />
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
      <MotionSettings p={p} />
    </>
  );
}

function MotionSettings({ p }: { p: Prefs }) {
  return (
    <Section title="Motion">
      <Row
        title="Motion"
        description="Reduced holds the window still: no shockwave, no slide-ins, no pulses. System follows the operating system."
        control={
          <Segmented
            label="Motion"
            value={p.motion}
            onChange={set("motion")}
            options={[
              { value: "system", label: "System" },
              { value: "full", label: "Full" },
              { value: "reduced", label: "Reduced" },
            ]}
          />
        }
      />
      <Row
        title="Play the Yolo effect"
        description="A shockwave crosses the window when the open thread switches to Yolo. Off, the mode chip flashes instead."
        control={<Switch label="Play the Yolo effect" checked={p.yoloEffect} onChange={set("yoloEffect")} />}
      />
    </Section>
  );
}

// A small change for the diff theme's preview: a keyword, a string, a
// number, a comment and a changed word.
const PREVIEW = [
  "@@ -1,6 +1,6 @@ export function retry(times: number) {",
  " // Wait a little longer after each failure.",
  "-const delay = 100;",
  "+const delay = 250;",
  ' const label = "retry";',
  "-for (let i = 0; i < times; i++) wait(delay);",
  "+for (let i = 0; i < times; i++) wait(delay * (i + 1));",
  " return label;",
].join("\n");

function Diffs({ p }: { p: Prefs }) {
  return (
    <Section title="Diffs">
      <Row
        title="Diff theme"
        description="Match app theme takes the window's colours. The others set their own colours for added and removed lines and for code."
      />
      <div className="space-y-3 pt-1 pb-4">
        <div className="-mx-1.5 grid grid-cols-2 gap-1 @lg:grid-cols-3" role="radiogroup" aria-label="Diff theme">
          {DIFF_THEMES.map((t) => {
            const on = p.diffTheme === t.id;
            return (
              <button
                key={t.id}
                type="button"
                role="radio"
                aria-checked={on}
                onClick={() => setPrefs({ diffTheme: t.id })}
                className={cn(
                  "flex cursor-pointer items-center gap-2.5 rounded-[10px] p-1.5 pr-2 text-left text-xs outline-none transition-colors hover:bg-hover focus-visible:ring-2 focus-visible:ring-ring",
                  on && "bg-selected",
                )}
              >
                <span
                  aria-hidden
                  className="flex h-7 w-10 shrink-0 flex-col justify-center gap-[3px] overflow-hidden rounded-[6px] px-1.5 ring-1 ring-hairline"
                  style={{ background: t.swatch[0] }}
                >
                  <span className="h-[3px] w-full rounded-full opacity-80" style={{ background: t.swatch[1] }} />
                  <span className="h-[3px] w-2/3 rounded-full opacity-80" style={{ background: t.swatch[2] }} />
                  <span className="h-[3px] w-1/2 rounded-full" style={{ background: t.swatch[3] }} />
                </span>
                <span className={cn("min-w-0 flex-1 truncate", on ? "font-medium" : "text-muted-foreground")}>{t.label}</span>
                {on && <CircleCheck aria-hidden weight="fill" className="size-3.5 shrink-0 text-primary" />}
              </button>
            );
          })}
        </div>
        <div className="diff-preview overflow-hidden rounded-[10px] ring-1 ring-hairline" aria-label="Diff theme preview">
          <LineComments.Provider value={null}>
            <DiffView diff={PREVIEW} numbered path="retry.ts" />
          </LineComments.Provider>
        </div>
      </div>
      <Row
        title="Split view"
        description="Show the old file next to the new one when the side panel is expanded."
        control={
          <Segmented
            label="Diff view"
            value={p.diffView}
            onChange={set("diffView")}
            options={[
              { value: "unified", label: "Unified" },
              { value: "split", label: "Split" },
            ]}
          />
        }
      />
      <Row
        title="Wrap long lines"
        description="Wrap lines that do not fit. Otherwise each file scrolls sideways."
        control={<Switch label="Wrap long lines" checked={p.diffWrap} onChange={set("diffWrap")} />}
      />
    </Section>
  );
}

function Agents({ state, p }: { state: AppState; p: Prefs }) {
  const kinds = state.server?.kinds ?? [];
  const yolo = state.server?.allow_yolo === true;
  // A Deck that keeps profiles reads "@" as an agent's sigil, as it reads
  // "$"; an older one reads only "$".
  const deck = useContext(ProfilesDeck);
  const at = deck !== null && serves(deck.c, "ListProfiles") ? "@" : "$";
  return (
    <>
      <Section title="Agent kinds">
        {kinds.length === 0 && <Row title="No agents" description="This Deck names no agent kinds." />}
        {kinds.map((k) => (
          <div key={k} className="flex items-start gap-3 py-3">
            <AgentGlyph kind={k} mark={kindMark(state.theme, k)} className="mt-px text-base" />
            <div className="min-w-0 space-y-0.5">
              <h3 className="font-medium text-[13px]">{k}</h3>
              <p className="text-pretty text-muted-foreground text-xs leading-relaxed">
                Type <code className="font-mono text-foreground/80">{at}{k}</code> to reach a free {k},{" "}
                <code className="font-mono text-foreground/80">{at}{k}+</code> to start another,{" "}
                <code className="font-mono text-foreground/80">{at}{k}*</code> to reach all of them.
                {at === "@" && (
                  <>
                    {" "}
                    <code className="font-mono text-foreground/80">${k}</code> works too.
                  </>
                )}
              </p>
            </div>
          </div>
        ))}
      </Section>
      {/* The Team switch (teamGate.ts): off, the named agents and the
          teammates leave the window, and their settings with them. */}
      <Section title="Team">
        <Row
          title="Team"
          description={
            p.team
              ? "Named agents, teammates, shared rooms and the Team page."
              : "Off: only plain agents show. Your profiles and team link are kept, and come back when you turn it on."
          }
          control={<Switch label="Team" checked={p.team} onChange={set("team")} />}
        />
      </Section>
      {p.team && <ProfilesEditor kinds={kinds} theme={state.theme} />}
      <Section title="Permissions">
        <Row
          title="Yolo mode"
          description={
            yolo ? "A thread may run every command and edit without asking." : "Not allowed for this window: the paired Deck has to allow it."
          }
          control={<Value tone={yolo ? "on" : "off"}>{yolo ? "Allowed" : "Not allowed"}</Value>}
        />
        <Row title="Models" description="Each agent's model and effort are picked per thread, from the composer's agent picker." />
      </Section>
      <Section title="Plans">
        <Row
          title="Approve plans automatically"
          description="When an agent posts a plan in Plan mode, approve it at once and let the agent go ahead."
          control={<Switch label="Approve plans automatically" checked={p.autoApprovePlans} onChange={set("autoApprovePlans")} />}
        />
      </Section>
    </>
  );
}

// A setting that is read, not set: its value on the right, quiet. A state
// carries its dot.
function Value({ children, mono, tone }: { children: ReactNode; mono?: boolean; tone?: "on" | "wait" | "off" }) {
  return (
    <span className={cn("flex min-w-0 max-w-[22rem] items-center gap-1.5 text-muted-foreground text-xs @md:justify-end", mono === true && "font-mono text-2xs")}>
      {tone !== undefined && (
        <span
          aria-hidden
          className={cn("size-1.5 shrink-0 rounded-full", tone === "on" ? "bg-success" : tone === "wait" ? "animate-deck-pulse bg-warning" : "bg-faint")}
        />
      )}
      <span className="min-w-0 truncate @md:text-right" title={typeof children === "string" ? children : undefined}>
        {children}
      </span>
    </span>
  );
}

const LINK: Record<string, { label: string; tone: "on" | "wait" | "off" }> = {
  online: { label: "Connected", tone: "on" },
  connecting: { label: "Connecting…", tone: "wait" },
  offline: { label: "Offline", tone: "off" },
};

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
  const link = LINK[state.link] ?? { label: state.link, tone: "off" as const };
  return (
    <>
      <Section title="This Deck">
        <Row title="Project" control={<Value>{s?.project ?? "—"}</Value>} />
        <Row title="Where" control={<Value mono>{where}</Value>} />
        <Row title="Transport" control={<Value>{transport}</Value>} />
        <Row title="Status" control={<Value tone={link.tone}>{link.label}</Value>} />
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
        <Row title="App version" control={<Value mono>{app?.version ?? "—"}</Value>} />
        <Row title="Deck version" control={<Value mono>{s?.version ?? "—"}</Value>} />
        <Row
          title="Electron"
          control={<Value mono>{app === null ? "—" : `${app.electron} (Chromium ${app.chromium}, ${app.platform})`}</Value>}
        />
        <Row title="Protocol" control={<Value mono>{s === null ? "—" : String(s.protocol)}</Value>} />
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
  const groups: ReadonlyArray<[string, ReadonlyArray<[string, string[]]>]> = [
    [
      "Window",
      [
        ["Search threads and commands", [m, "K"]],
        ["Settings", [m, ","]],
        ["Toggle the sidebar", [m, "B"]],
        ["Toggle the terminal", [m, "J"]],
        ["Zoom in, out, actual size", [m, "=", "-", "0"]],
      ],
    ],
    [
      "Threads",
      [
        ["New thread", [m, "N"]],
        ["Open a folder", [m, "O"]],
        ["Next room that needs you", [m, mac ? "⌥" : "Alt", "L"]],
        ["Open room 1 to 9 of the sidebar (hold to see the numbers)", [m, "1…9"]],
        ["Rename a thread", ["Double-click"]],
      ],
    ],
    [
      "Composer",
      [
        ["Send the message", ["Enter"]],
        ["New line", ["Shift", "Enter"]],
        ["Toggle Yolo mode", [m, "Shift", "Y"]],
        ["Mention an agent or a person", ["@"]],
        ["Pick an agent (the older sigil)", ["$"]],
        ["Add a file", ["#"]],
        ["Run a command, at the start", ["/"]],
      ],
    ],
  ];
  return (
    <>
      {groups.map(([title, rows]) => (
        <Section key={title} title={title}>
          {rows.map(([what, keys]) => (
            <Row
              key={what}
              title={<span className="font-normal">{what}</span>}
              control={
                <span className="flex items-center gap-1">
                  {keys.map((k) => (
                    <Kbd key={k} className="min-w-6 px-1.5">
                      {k}
                    </Kbd>
                  ))}
                </span>
              }
            />
          ))}
        </Section>
      ))}
    </>
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
      <header className="topbar drag flex h-13 shrink-0 items-center gap-1.5 px-5 text-sm">
        <span className="text-muted-foreground">Settings</span>
        <ChevronRight aria-hidden className="size-3 text-faint" />
        <h1 className="title font-semibold">{label}</h1>
      </header>
      <div className="min-h-0 flex-1 overflow-y-auto">
        <div key={section} className="mx-auto flex w-full max-w-[42rem] flex-col gap-10 px-6 pt-6 pb-16">
          <Head section={section} label={label} />
          {section === "general" && <General p={p} />}
          {section === "appearance" && <Appearance p={p} />}
          {section === "agents" && <Agents state={state} p={p} />}
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
