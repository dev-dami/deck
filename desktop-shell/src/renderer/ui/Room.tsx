import { applyAgentChip } from "@web/composer";
import { MAX_VIDEO_BYTES_LOCAL, MAX_VIDEO_BYTES_REMOTE, videoGate } from "@web/images";
import type { Session } from "@web/protocol";
import { sessionMode, sessionTitle } from "@web/state";
import { Ellipsis, FileDiff, Flag, Folder, Hourglass, ListChecks, MessageCircleQuestion, Settings as SettingsIcon, Square } from "./icons";
import { type ComponentProps, type ReactNode, type RefObject, useCallback, useEffect, useMemo, useState } from "react";
import type { EngineStop } from "../../shared/bridge";
import { type DeckClient, serves } from "../client";
import { FILES_LIMIT } from "../completion";
import { subsOf, type Team } from "../crew";
import { agentStatus } from "../approvals";
import { buildLanes, filterLane, soloRoom, subParent } from "../lanes";
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
import type { Pick } from "./AgentPicker";
import { highlightCounts, stripItem } from "../highlights";
import { useRoomLive } from "../live";
import { Composer } from "./Composer";
import { HighlightsPill, HighlightsStrip } from "./HighlightsPill";
import { EngineStopped } from "./EngineStopped";
import { BranchPicker, type Git } from "./Git";
import { WorkGraph } from "./graph/WorkGraph";
import { modKey } from "./keys";
import { Badge } from "./kit/badge";
import { Button } from "./kit/button";
import { MenuItem, MenuSeparator, Popover } from "./kit/menu";
import { Lanes } from "./Lanes";
import { QuotaMark, RoomQuota, sendAtReset, WaitBanner } from "./Limits";
import { RoomLevel } from "./LevelPicker";
import { type AnsweredRecord, RoomAsk } from "./RoomAsk";
import { ModePicker } from "./ModePicker";
import { NewThread } from "./NewThread";
import { type Lead, LeadPicker } from "./LeadPicker";
import { RoomHeader } from "./RoomHeader";
import type { RoomView } from "./roomView";
import { Timeline, OpenThread } from "./Timeline";
import { NEW, type OpenRoom } from "./useRoom";
import { type WorkflowId, workflows as workflowsFor } from "../workflows";

// The open thread's timeline. It reads that thread's messages alone, so
// tokens in other threads do not render it, nor the shell around it. lane
// shows one lane's messages alone (the lane strip's filter).
function LiveTimeline({
  c,
  sid,
  lane,
  subs,
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
      // A subagent's lane: its work alone, open.
      subAlone={lane !== null && ((name) => subParent(name) !== null || subs?.has(name) === true)(lane.replace(/^agent:/, ""))}
      {...(subs === undefined ? {} : { subs })}
      {...rest}
    />
  );
}

function MoreMenu({
  busy,
  queued,
  highlights,
  onStop,
  onPanel,
  onSettings,
  sendAt,
}: {
  busy: boolean;
  queued: number;
  // Open Highlights, or null for a Deck that does not serve them.
  highlights: number | null;
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
      {highlights !== null && (
        <MenuItem onClick={run(() => onPanel("highlights"))}>
          <Flag /> <span className="flex-1">Highlights</span>
          {highlights > 0 && <Badge variant="muted">{highlights}</Badge>}
        </MenuItem>
      )}
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
  diff,
  panelOpen,
  onPanel,
  onDiff,
  onTogglePanel,
  layout,
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
  // The side panel's Changes shows; any of its views shows.
  diff: boolean;
  panelOpen: boolean;
  // Opens a tab of the side panel, with a file to show in Changes.
  onPanel: (p: Panel, path?: string) => void;
  onDiff: () => void;
  onTogglePanel: () => void;
  // The Layout menu, in the header beside the panels' buttons.
  layout?: ReactNode;
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
  // What the person answered the room's last question, shown on the composer.
  const [answered, setAnswered] = useState<AnsweredRecord | null>(null);
  // The multi-agent workflow picked on the new-thread screen for its next
  // send; none is the one agent every thread starts as. A workflow sets the
  // new thread's level, which only a local Deck takes at creation.
  const [workflow, setWorkflow] = useState<WorkflowId | null>(null);
  // The agent picked for the next new thread; null is the default kind.
  const [nextLead, setNextLead] = useState<Lead | null>(null);
  const defaultKind = kinds.includes("claude") || kinds.length === 0 ? "claude" : (kinds[0] ?? "claude");
  const flows = c.capabilities === undefined ? [] : workflowsFor(kinds, nextLead?.kind ?? defaultKind);
  const picked = flows.find((w) => w.id === workflow) ?? null;
  const live = useRoomLive(c, sid);
  const strip = serves(c, "Highlights") ? stripItem(live.items) : null;
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
  const lanes = buildLanes({ roster, agents: crew?.agents ?? null, shares: r.shares, waiting: pending.map((a) => a.agent), leases: meta.leases });
  // A video reaches the lead only when its model takes one; a paired Deck
  // takes smaller ones, uploaded in chunks.
  const video = videoGate(state.server?.features ?? [], meta.lead ?? open?.lead);
  const videoMax = c.capabilities === undefined ? MAX_VIDEO_BYTES_REMOTE : MAX_VIDEO_BYTES_LOCAL;
  const common = { draft, setDraft, images: imagesOn, video, videoMax, onSlash: send.runSlash, onError: toast, inputRef, sendKey: prefs.sendKey, theme: state.theme };
  // The room's lead: the agent the person talks to (LeadPicker). The room's
  // own, else its first agent that is no subagent; on the new-thread screen
  // the one picked for it, else the default kind.
  const roomLead = ((): Lead | null => {
    if (meta.lead !== null) {
      const line = roster.find((l) => l.id === meta.lead?.agent);
      return { ...meta.lead, color: line?.color ?? "" };
    }
    // Without the Deck's word, the agent the room started with.
    const subs = new Set((crew?.agents ?? []).filter((a) => (a.depth ?? 0) > 0).map((a) => a.name));
    const line = roster.find((l) => !subs.has(l.id)) ?? roster[0];
    return line === undefined ? null : { agent: line.id, kind: line.kind, model: line.model ?? "", effort: line.effort ?? "", profile: line.profile ?? "", color: line.color ?? "" };
  })();
  const newLead: Lead = nextLead ?? { agent: defaultKind, kind: defaultKind, model: "", effort: "", profile: "" };
  const leadPicker = (lead: Lead, onPick: (p: Pick) => void, status?: string) => (
    <LeadPicker lead={lead} status={status} source={picker} kinds={kinds} named={named.profiles ?? []} theme={state.theme} onPick={onPick} />
  );
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
        parent={((p) => (p === "" ? null : { id: p, title: sessionTitle(state.sessions.find((x) => x.id === p)) }))(meta.parent)}
        {...(onOpenThread !== undefined ? { onOpenParent: onOpenThread } : {})}
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
        diff={diff}
        panelOpen={panelOpen}
        onDiff={onDiff}
        onPanel={onTogglePanel}
        onError={toast}
        onInfo={info}
        onPreview={onPreview}
        {...(layout === undefined ? {} : { layout })}
        {...(open !== undefined && engine === null ? { view, onView: setView } : {})}
      />
      {open !== undefined && !soloRoom(lanes) && (
        <Lanes
          lanes={lanes}
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
          onFocus={focusInput}
          canIsolate={serves(c, "IsolateSession")}
          workflows={flows}
          picked={workflow}
          onPick={setWorkflow}
        >
          <Composer
            key={NEW}
            {...common}
            online={online && state.sessionsLoaded}
            onSend={async (text, images) => {
              if (picked === null) return send.start(text, images, newLead);
              const ok = await send.startWorkflow(picked, text, images, newLead);
              if (ok) setWorkflow(null);
              return ok;
            }}
            above={raceBuilder}
            placeholder={`Ask ${newLead.profile || newLead.kind} anything. # adds a file, /race races agents.`}
            strip={leadPicker(newLead, (p) => setNextLead({ agent: p.agent, kind: p.kind, model: p.model, effort: p.effort, profile: p.agent !== p.kind ? p.agent : "" }))}
          />
        </NewThread>
      ) : (
        // The body: the timeline (or the graph) runs the full height, under
        // the composer, which floats 12px over the sheet's bottom edge.
        <OpenThread.Provider value={onOpenThread ?? null}>
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
                subs={subsOf(crew?.agents)}
                parentThread={meta.parent === "" ? null : { id: meta.parent, title: sessionTitle(state.sessions.find((x) => x.id === meta.parent)) }}
              />
            )}
            <div ref={dock} className="composer-dock pointer-events-none absolute inset-x-0 bottom-0 z-20 px-4 pb-3 *:pointer-events-auto">
              <Composer
                key={open.id}
                {...common}
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
                  pending[0] !== undefined || answered?.session === open.id ? (
                    <RoomAsk
                      c={c}
                      sessionId={open.id}
                      pending={pending}
                      answerers={room.answerers}
                      focusQuestion={draft.trim() === ""}
                      answered={answered}
                      setAnswered={setAnswered}
                    />
                  ) : limits?.waits.some((w) => w.sessionId === open.id) === true ? (
                    <WaitBanner api={api} sessionId={open.id} onError={toast} />
                  ) : (
                    strip === null ? false : <HighlightsStrip item={strip} onOpen={() => onPanel("highlights")} />
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
                    <HighlightsPill c={c} sid={open.id} onOpen={() => onPanel("highlights")} />
                    <MoreMenu busy={open.busy} queued={queued} highlights={serves(c, "Highlights") ? highlightCounts(live.items).open : null} onStop={room.stop} onPanel={(p) => onPanel(p)} onSettings={onSettings} sendAt={sendAt} />
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
                    {api !== undefined && <RoomLevel api={api} sessionId={open.id} onError={toast} />}
                    <span aria-hidden className="mx-0.5 h-4 w-px shrink-0 bg-hairline-strong" />
                    {roomLead !== null &&
                      leadPicker(
                        roomLead,
                        (p) => void c.setModel(open.id, p.agent, p.model, p.effort),
                        ((line) => (line === undefined ? "idle" : agentStatus(line, open.id, state.approvals)))(roster.find((l) => l.id === roomLead.agent)),
                      )}
                    {open.busy && <span className="send-hint hidden shrink-0 px-1.5 text-[11px] text-faint @[46rem]/bar:inline">{sendHint(prefs.sendKey, mac)}</span>}
                    <RoomQuota api={api} roster={roster} />
                  </>
                }
              />
            </div>
          </div>
        </OpenThread.Provider>
      )}
    </>
  );
}
