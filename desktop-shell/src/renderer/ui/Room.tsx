import { applyAgentChip } from "@web/composer";
import type { Session } from "@web/protocol";
import { sessionMode } from "@web/state";
import { Ellipsis, FileDiff, Folder, ListChecks, MessageCircleQuestion, Settings as SettingsIcon, Square } from "lucide-react";
import { type ComponentProps, type RefObject, useMemo, useState } from "react";
import type { EngineStop } from "../../shared/bridge";
import { type DeckClient, serves } from "../client";
import { FILES_LIMIT } from "../completion";
import type { Team } from "../crew";
import { buildLanes, filterLane } from "../lanes";
import { usePrefs } from "../prefs";
import { sendHint } from "../queue";
import { EMPTY_ROOM, useRoomMetas } from "../roomMeta";
import { type ShellSlice, shallowEqual, threadSlice, useAppSlice } from "../select";
import type { Panel } from "../ui";
import { AgentsStrip } from "./AgentsStrip";
import { ApprovalCard } from "./ApprovalCard";
import { Composer } from "./Composer";
import { EngineStopped } from "./EngineStopped";
import { BranchPicker, type Git } from "./Git";
import { modKey } from "./keys";
import { Badge } from "./kit/badge";
import { Button } from "./kit/button";
import { MenuItem, MenuSeparator, Popover } from "./kit/menu";
import { Lanes } from "./Lanes";
import { ModePicker } from "./ModePicker";
import { NewThread } from "./NewThread";
import { RoomHeader } from "./RoomHeader";
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
  return <Timeline messages={messages} roster={t.roster} more={t.more} onOlder={() => c.loadOlder(sid)} {...rest} />;
}

function MoreMenu({
  busy,
  queued,
  onStop,
  onPanel,
  onSettings,
}: {
  busy: boolean;
  queued: number;
  onStop: () => void;
  onPanel: (p: Panel) => void;
  onSettings: () => void;
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
      label="More"
      className="w-56"
      trigger={
        <Button variant="ghost-muted" size="icon-sm" aria-label="More" title="More" aria-expanded={open} onClick={() => setOpen((o) => !o)}>
          <Ellipsis />
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

// The room: its header and lane strip, then the timeline and the composer,
// or the new-thread screen, or the notice of a stopped engine.
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
}) {
  const { draft, setDraft, files, paths, crew, lane, room, send } = r;
  const prefs = usePrefs();
  const api = c.project;
  const sid = open?.id ?? null;
  // What the Deck says of the room beyond the session: its branch and leases.
  const metas = useRoomMetas(c.rooms);
  const meta = (sid === null ? undefined : metas.get(sid)) ?? EMPTY_ROOM;
  // "@" lists the tree's files too when the Deck serves ListFiles.
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
  const listModels = (kind: string) => c.listModels(sid ?? "", kind);
  const setModel = (kind: string, model: string, effort?: string) => sid !== null && void c.setModel(sid, kind, model, effort);
  const common = { draft, setDraft, kinds, images: imagesOn, onSlash: send.runSlash, onError: toast, inputRef, sendKey: prefs.sendKey };

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
      />
      {open !== undefined && (
        <Lanes
          lanes={buildLanes({ roster, agents: crew?.agents ?? null, shares: r.shares, waiting: pending.map((a) => a.agent), leases: meta.leases })}
          theme={state.theme}
          filter={lane}
          onFilter={r.setLane}
          listModels={listModels}
          setModel={setModel}
          onStop={room.interrupt}
          onSteer={address}
        />
      )}
      {engine !== null ? (
        <EngineStopped stop={engine} onRestart={() => c.restart?.() ?? Promise.resolve()} />
      ) : open === undefined ? (
        <NewThread
          loaded={state.sessionsLoaded}
          project={project}
          kinds={kinds}
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
            placeholder="Ask anything. $ picks an agent, $codex+ starts another."
          />
        </NewThread>
      ) : (
        <>
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
          <div className="shrink-0 px-5 pb-3">
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
              banner={
                pending[0] !== undefined && (
                  <div className="flex flex-col gap-1" aria-label="Approvals for this thread">
                    <ApprovalCard
                      a={pending[0]}
                      index={0}
                      count={pending.length}
                      className="border-0 bg-transparent"
                      answerers={room.answerers}
                      onAnswer={(d) => void c.answerApproval(pending[0]!.session_id, pending[0]!.id, d)}
                    />
                  </div>
                )
              }
              toolbar={<MoreMenu busy={open.busy} queued={queued} onStop={room.stop} onPanel={(p) => onPanel(p)} onSettings={onSettings} />}
              strip={
                <>
                  <ModePicker
                    mode={sessionMode(open)}
                    allowYolo={state.server?.allow_yolo ?? false}
                    confirmYolo={prefs.confirmYolo}
                    onSet={(m) => void c.setMode(open.id, m)}
                  />
                  <span className="h-3.5 w-px shrink-0 bg-border" />
                  <AgentsStrip
                    sessionId={open.id}
                    roster={roster}
                    kinds={kinds}
                    approvals={state.approvals}
                    theme={state.theme}
                    api={api}
                    agents={crew?.agents ?? null}
                    draft={draft}
                    setDraft={setDraft}
                    hint={open.busy ? sendHint(prefs.sendKey, mac) : null}
                    onFocus={focusInput}
                    listModels={listModels}
                    setModel={setModel}
                  />
                  <span className="ml-auto flex min-w-0 shrink items-center">
                    {api !== undefined && git.status?.repo === true ? (
                      <BranchPicker git={git} project={api} onError={toast} />
                    ) : (
                      <span className="flex min-w-0 items-center gap-1.5 px-1.5 text-muted-foreground/70 text-xs">
                        <Folder className="size-3.5 shrink-0" />
                        <span className="truncate">{project || "Deck"}</span>
                      </span>
                    )}
                  </span>
                </>
              }
            />
          </div>
        </>
      )}
    </>
  );
}
