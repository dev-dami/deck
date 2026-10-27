import { applyAgentChip } from "@web/composer";
import type { Session } from "@web/protocol";
import { sessionMode } from "@web/state";
import { Ellipsis, FileDiff, Folder, Hourglass, ListChecks, MessageCircleQuestion, Settings as SettingsIcon, Square } from "./icons";
import { type ComponentProps, type RefObject, useCallback, useEffect, useMemo, useState } from "react";
import type { EngineStop } from "../../shared/bridge";
import { type DeckClient, serves } from "../client";
import { FILES_LIMIT } from "../completion";
import type { Team } from "../crew";
import { buildLanes, filterLane } from "../lanes";
import { whenOf } from "../limits";
import { pokeLimits, useLimits, useNow } from "../limitsState";
import { focusMessage, useFocusedMessage } from "../palette";
import { usePrefs } from "../prefs";
import { useMentionables } from "../profiles";
import { sendHint } from "../queue";
import { gateMentionables } from "../teamGate";
import { EMPTY_ROOM, useRoomMetas } from "../roomMeta";
import { type ShellSlice, shallowEqual, threadSlice, useAppSlice } from "../select";
import type { Panel } from "../ui";
import { usePickerSource } from "./AgentPicker";
import { RaceBuilder } from "./RaceBuilder";
import { AgentsStrip } from "./AgentsStrip";
import { ApprovalCard } from "./ApprovalCard";
import { Composer } from "./Composer";
import { EngineStopped } from "./EngineStopped";
import { BranchPicker, type Git } from "./Git";
import { WorkGraph } from "./graph/WorkGraph";
import { modKey } from "./keys";
import { Badge } from "./kit/badge";
import { Button } from "./kit/button";
import { MenuItem, MenuSeparator, Popover } from "./kit/menu";
import { Lanes } from "./Lanes";
import { QuotaMark, RoomQuota, sendAtReset, WaitBanner } from "./Limits";
import { ModePicker } from "./ModePicker";
import { NewThread } from "./NewThread";
import { RoomHeader } from "./RoomHeader";
import type { RoomView } from "./roomView";
import { Timeline } from "./Timeline";
import { NEW, type OpenRoom } from "./useRoom";

// The open thread's timeline. It reads that thread's messages alone, so
// tokens in other threads do not render it, nor the shell around it. lane
// shows one lane's messages alone (the lane strip's filter).
function LiveTimeline({
  c,
  sid,
  lane,
  ...rest
}: { c: DeckClient; sid: string; lane: string | null } & Omit<ComponentProps<typeof Timeline>, "messages" | "roster" | "more" | "onOlder">) {
  const select = useMemo(() => threadSlice(sid), [sid]);
  const t = useAppSlice(c.store, select, shallowEqual);
  const messages = useMemo(() => filterLane(t.messages, lane), [t.messages, lane]);
  // Image parts and a palette hit's message, for a Deck that serves them.
  const images = serves(c, "ReadImage");
  const readImage = useMemo(
    () => (images ? (mid: string, index: number) => c.readImage?.(sid, mid, index) ?? Promise.resolve(null) : undefined),
    [c, sid, images],
  );
  const focus = useFocusedMessage(sid);
  return (
    <Timeline
      messages={messages}
      roster={t.roster}
      more={t.more}
      onOlder={() => c.loadOlder(sid)}
      {...(readImage === undefined ? {} : { readImage })}
      focusMessage={focus}
      onFocused={() => focusMessage(sid, null)}
      {...rest}
    />
  );
}

function MoreMenu({
  busy,
  queued,
  onStop,
  onPanel,
  onSettings,
  sendAt,
}: {
  busy: boolean;
  queued: number;
  onStop: () => void;
  onPanel: (p: Panel) => void;
  onSettings: () => void;
  // "Send at next reset": when it would send the draft, and the send; null
  // while there is no draft or no reset is known.
  sendAt: { label: string; go: () => void } | null;
}) {
  const [open, setOpen] = useState(false);
  const run = (f: () => void) => () => {
    setOpen(false);
    f();
  };
  return (
    <Popover
      open={open}
      onClose={() => setOpen(false)}
      side="top"
      align="end"
      label="More"
      className="w-56"
      trigger={
        <Button variant="ghost-muted" size="icon" aria-label="More" title="More" aria-expanded={open} onClick={() => setOpen((o) => !o)}>
          <Ellipsis className="size-[18px]" weight="bold" />
        </Button>
      }
    >
      {busy && (
        <>
          <MenuItem className="text-destructive-foreground" onClick={run(onStop)}>
            <Square /> Stop all agents
          </MenuItem>
          <MenuSeparator />
        </>
      )}
      {sendAt !== null && (
        <>
          <MenuItem onClick={run(sendAt.go)} data-send-at-reset="">
            <Hourglass /> <span className="flex-1">Send at next reset</span>
            <span className="text-2xs text-faint tabular-nums">{sendAt.label}</span>
          </MenuItem>
          <MenuSeparator />
        </>
      )}
      <MenuItem onClick={run(() => onPanel("changes"))}>
        <FileDiff /> Open diff
      </MenuItem>
      <MenuItem onClick={run(() => onPanel("side"))}>
        <MessageCircleQuestion /> <span className="flex-1">Side chat</span>
      </MenuItem>
      <MenuItem onClick={run(() => onPanel("tasks"))}>
        <ListChecks /> <span className="flex-1">Tasks</span>
        {queued > 0 && <Badge variant="muted">{queued}</Badge>}
      </MenuItem>
      <MenuSeparator />
      <MenuItem onClick={run(onSettings)}>
        <SettingsIcon /> Settings
      </MenuItem>
    </Popover>
  );
}

// The floating composer's dock: it measures itself into --composer-h on
// the room's body (a ResizeObserver), so the timeline pads its end and the
// jump pill sits above the capsule however tall a draft or an approval
// makes it. The height counts the 12px the capsule floats over the edge.
// The shared room's composer floats the same way.
export function useDockHeight() {
  return useCallback((el: HTMLDivElement | null) => {
    const body = el?.parentElement;
    if (el === null || body === null || body === undefined) return;
    const set = () => body.style.setProperty("--composer-h", `${Math.ceil(el.getBoundingClientRect().height)}px`);
    set();
    const watch = new ResizeObserver(set);
    watch.observe(el);
    return () => watch.disconnect();
  }, []);
}

// ⌘/Ctrl+Shift+G turns the room between the conversation and the graph.
// The shell's own keys take Shift with Y only, so the room listens itself.
function useViewKey(on: boolean, toggle: () => void) {
  useEffect(() => {
    if (!on) return;
    const onKey = (e: KeyboardEvent) => {
      if (!(e.metaKey || e.ctrlKey) || !e.shiftKey || e.altKey || e.code !== "KeyG") return;
      e.preventDefault();
      toggle();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [on, toggle]);
}

// The room: its header and lane strip, then its body: the timeline or the
// work graph with the composer floating over it, or the new-thread screen,
// or the notice of a stopped engine.
export function Room({
  c,
  state,
  open,
  project,
  source,
  mac,
  engine,
  r,
  inputRef,
  focusInput,
  sidebar,
  onShowSidebar,
  git,
  team,
  terminal,
  onTerminal,
  panel,
  onPanel,
  onDiff,
  onTogglePanel,
  onSettings,
  onPreview,
  toast,
  info,
  onOpenThread,
}: {
  c: DeckClient;
  state: ShellSlice;
  open: Session | undefined;
  project: string;
  // The project's source, for the composer's history.
  source: string;
  mac: boolean;
  engine: EngineStop | null;
  // The open room's state and verbs (useOpenRoom).
  r: OpenRoom;
  inputRef: RefObject<HTMLTextAreaElement | null>;
  focusInput: () => void;
  sidebar: boolean;
  onShowSidebar: () => void;
  git: Git;
  team: Team | string | null;
  terminal: boolean;
  onTerminal: () => void;
  panel: Panel | null;
  // Opens a tab of the side panel, with a file to show in Changes.
  onPanel: (p: Panel, path?: string) => void;
  onDiff: () => void;
  onTogglePanel: () => void;
  onSettings: () => void;
  // Opens a dev server's URL in the Preview tab.
  onPreview: (url: string) => void;
  toast: (text: string) => void;
  info: (text: string) => void;
  // Opens another thread of the project (the graph's History).
  onOpenThread?: (id: string) => void;
}) {
  const { draft, setDraft, files, paths, crew, lane, room, send } = r;
  const prefs = usePrefs();
  const api = c.project;
  const sid = open?.id ?? null;
  // How the room shows its thread (ui/roomView.ts); the same for every room.
  const [view, setView] = useState<RoomView>("conversation");
  const toggleView = useCallback(() => setView((v) => (v === "graph" ? "conversation" : "graph")), []);
  useViewKey(open !== undefined && engine === null, toggleView);
  const dock = useDockHeight();
  // What the Deck says of the room beyond the session: its branch and leases.
  const metas = useRoomMetas(c.rooms);
  const meta = (sid === null ? undefined : metas.get(sid)) ?? EMPTY_ROOM;
  // "#" lists the tree's files too when the Deck serves ListFiles.
  const listFiles = useMemo(
    () => (sid === null || !serves(c, "ListFiles") ? undefined : async (q: string) => (await c.listFiles?.(sid, q, FILES_LIMIT))?.paths ?? null),
    [c, sid],
  );
  const roster = sid === null ? [] : (state.roster[sid] ?? []);
  const kinds = state.server?.kinds ?? [];
  const pending = sid === null ? [] : state.approvals.filter((a) => a.session_id === sid);
  const queued = sid === null ? 0 : (state.queue[sid]?.length ?? 0);
  const imagesOn = state.server?.features.includes("images") ?? false;
  const online = state.link === "online";
  const address = (agent: string) => {
    setDraft(applyAgentChip(draft, agent));
    focusInput();
  };
  // Every agent's models, listed once per Deck and kept (agentCatalog.ts):
  // the pickers open on them at once.
  const picker = usePickerSource(c, state.server, sid);
  const setModel = (kind: string, model: string, effort?: string) => sid !== null && void c.setModel(sid, kind, model, effort);
  // "@" offers the Deck's profiles and the team's people too, while the
  // team is on (teamGate.ts).
  const named = gateMentionables(prefs.team, useMentionables(c, team));
  // Plan limits: the agents' quotas, a wait for a reset, and when the draft
  // would go at the next one.
  const limits = useLimits(api).data;
  const now = useNow();
  const resetAt = sid === null || draft.trim() === "" ? null : sendAtReset(limits, draft, roster, sid, now);
  const sendAt =
    resetAt === null || sid === null || api === undefined
      ? null
      : {
          label: whenOf(resetAt.at, now, prefs.clock === "12h"),
          go: () => {
            const text = draft;
            setDraft("");
            void api.scheduleSend(sid, text, resetAt.at).then((err) => {
              if (err !== null) {
                setDraft(text);
                toast(err);
                return;
              }
              info(`Scheduled for ${whenOf(resetAt.at, Date.now(), prefs.clock === "12h")}, when ${resetAt.kind}'s limit resets.`);
              pokeLimits(api);
            });
          },
        };
  const common = { draft, setDraft, kinds, images: imagesOn, onSlash: send.runSlash, onError: toast, inputRef, sendKey: prefs.sendKey, theme: state.theme, ...named };
  // /race: the builder over the composer, until it starts the race or closes.
  const raceBuilder = r.race !== null && (
    <RaceBuilder
      key={r.race.n}
      brief={r.race.brief}
      kinds={kinds}
      named={named.profiles}
      picker={picker}
      theme={state.theme}
      // A local Deck judges through StartRace; a paired one through race.start.
      canJudge={c.capabilities === undefined || serves(c, "StartRace")}
      onStart={send.startRace}
      onClose={() => {
        r.closeRace();
        focusInput();
      }}
    />
  );

  return (
    <>
      <RoomHeader
        c={c}
        branch={meta.branch}
        onChanged={r.refreshChanges}
        sidebar={sidebar}
        onShowSidebar={onShowSidebar}
        project={project}
        open={open}
        loaded={state.sessionsLoaded}
        roster={roster}
        theme={state.theme}
        onRename={(title) => open !== undefined && void c.renameSession(open.id, title)}
        api={api}
        git={git}
        team={team}
        shares={r.shares}
        onShare={r.setShare}
        shareAsk={r.shareAsk}
        files={files}
        terminal={terminal}
        terminalKey={`${modKey(mac)}J`}
        onTerminal={onTerminal}
        diff={panel === "changes"}
        panelOpen={panel !== null}
        onDiff={onDiff}
        onPanel={onTogglePanel}
        onError={toast}
        onInfo={info}
        onPreview={onPreview}
        {...(open !== undefined && engine === null ? { view, onView: setView } : {})}
      />
      {open !== undefined && (
        <Lanes
          lanes={buildLanes({ roster, agents: crew?.agents ?? null, shares: r.shares, waiting: pending.map((a) => a.agent), leases: meta.leases })}
          theme={state.theme}
          filter={lane}
          onFilter={r.setLane}
          picker={picker}
          setModel={setModel}
          onStop={room.interrupt}
          onSteer={address}
          mark={(l) => <QuotaMark api={api} kind={l.agentKind} agent={l.name} sessionId={open.id} />}
        />
      )}
      {engine !== null ? (
        <EngineStopped stop={engine} onRestart={() => c.restart?.() ?? Promise.resolve()} />
      ) : open === undefined ? (
        <NewThread
          loaded={state.sessionsLoaded}
          project={project}
          kinds={kinds}
          theme={state.theme}
          draft={draft}
          setDraft={setDraft}
          onFocus={focusInput}
          canIsolate={serves(c, "IsolateSession")}
        >
          <Composer
            key={NEW}
            {...common}
            roster={[]}
            online={online && state.sessionsLoaded}
            onSend={send.start}
            above={raceBuilder}
            placeholder={named.mentions ? "Ask anything. @ picks an agent, # a file, /race races several." : "Ask anything. $ picks an agent, /race races several."}
          />
        </NewThread>
      ) : (
        // The body: the timeline (or the graph) runs the full height, under
        // the composer, which floats 12px over the sheet's bottom edge.
        <div className="room-body relative flex min-h-0 flex-1 flex-col">
          {view === "graph" ? (
            <WorkGraph
              c={c}
              api={api}
              sid={open.id}
              onOpenMessage={(thread, mid) => {
                // A History node: the conversation, at that message.
                r.setLane(null);
                focusMessage(thread, mid);
                if (thread !== open.id) onOpenThread?.(thread);
                setView("conversation");
              }}
              roster={roster}
              files={files}
              theme={state.theme}
              lane={lane}
              onLane={(l) => {
                r.setLane(l);
                // "Show its messages": the conversation, filtered to that lane.
                if (l !== null) setView("conversation");
              }}
              onOpenFile={(path) => onPanel("changes", path)}
              onOpenTasks={() => onPanel("tasks")}
              onSteer={address}
              onStop={room.interrupt}
            />
          ) : (
            <LiveTimeline
              c={c}
              sid={open.id}
              lane={lane}
              theme={state.theme}
              changes={files}
              onOpenDiff={(path) => onPanel("changes", path)}
              mode={room.mode}
              actions={room.actions}
              notes={r.notes}
            />
          )}
          <div ref={dock} className="composer-dock pointer-events-none absolute inset-x-0 bottom-0 z-20 px-4 pb-3 *:pointer-events-auto">
            <Composer
              key={open.id}
              {...common}
              roster={roster}
              online={online}
              onSend={room.send}
              onSteer={send.steer}
              paths={paths}
              listFiles={listFiles}
              history={{ project: source, room: open.id }}
              busy={open.busy}
              onStop={room.stop}
              notice={room.notice}
              above={raceBuilder}
              banner={
                pending[0] !== undefined ? (
                  <div className="flex flex-col gap-1" aria-label="Approvals for this thread">
                    <ApprovalCard
                      a={pending[0]}
                      index={0}
                      count={pending.length}
                      className="bg-transparent ring-0"
                      answerers={room.answerers}
                      onAnswer={(d) => void c.answerApproval(pending[0]!.session_id, pending[0]!.id, d)}
                    />
                  </div>
                ) : (
                  limits?.waits.some((w) => w.sessionId === open.id) === true && <WaitBanner api={api} sessionId={open.id} onError={toast} />
                )
              }
              toolbar={
                <>
                  <span className="flex min-w-0 shrink items-center">
                    {api !== undefined && git.status?.repo === true ? (
                      <BranchPicker git={git} project={api} onError={toast} />
                    ) : (
                      <span className="room-folder flex h-7 min-w-0 items-center gap-1.5 px-2 text-faint text-xs">
                        <Folder className="size-3.5 shrink-0" />
                        <span className="truncate">{project || "Deck"}</span>
                      </span>
                    )}
                  </span>
                  <MoreMenu busy={open.busy} queued={queued} onStop={room.stop} onPanel={(p) => onPanel(p)} onSettings={onSettings} sendAt={sendAt} />
                </>
              }
              strip={
                <>
                  <ModePicker
                    mode={sessionMode(open)}
                    allowYolo={state.server?.allow_yolo ?? false}
                    confirmYolo={prefs.confirmYolo}
                    onSet={(m) => void c.setMode(open.id, m)}
                  />
                  <span aria-hidden className="mx-0.5 h-4 w-px shrink-0 bg-hairline-strong" />
                  <AgentsStrip
                    sessionId={open.id}
                    roster={roster}
                    approvals={state.approvals}
                    theme={state.theme}
                    api={api}
                    agents={crew?.agents ?? null}
                    draft={draft}
                    setDraft={setDraft}
                    hint={open.busy ? sendHint(prefs.sendKey, mac) : null}
                    onFocus={focusInput}
                  />
                  <RoomQuota api={api} roster={roster} />
                </>
              }
            />
          </div>
        </div>
      )}
    </>
  );
}
