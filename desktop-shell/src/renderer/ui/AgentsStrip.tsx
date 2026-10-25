import { activeAgents, toggleAgent } from "@web/composer";
import type { AgentLine, Approval, ModelChoice, Theme } from "@web/protocol";
import { kindColour, kindMark } from "@web/theme";
import { AtSign, Check, Loader2, Plus, Square } from "lucide-react";
import { useEffect, useState } from "react";
import { agentColour } from "../agents";
import { agentStatus } from "../approvals";
import { cn } from "../lib/cn";
import { MenuItem, MenuLabel, MenuSeparator, Popover } from "./kit/menu";

export function Models({
  line,
  load,
  choose,
}: {
  line: AgentLine;
  load: Promise<ModelChoice[] | null>;
  choose: (model: string, effort?: string) => void;
}) {
  const [models, setModels] = useState<ModelChoice[] | null | "loading">("loading");
  useEffect(() => {
    let live = true;
    setModels("loading");
    void load.then((m) => {
      if (live) setModels(m);
    });
    return () => {
      live = false;
    };
  }, [load]);
  if (models === "loading") {
    return (
      <p className="flex items-center gap-2 px-2 py-1.5 text-muted-foreground text-xs">
        <Loader2 className="size-3 animate-spin" /> Loading models…
      </p>
    );
  }
  if (models === null || models.length === 0) return <p className="px-2 py-1.5 text-muted-foreground text-xs">No models.</p>;
  return (
    <div className="max-h-56 overflow-y-auto">
      {models.map((m) => (
        <div key={m.id} className="menu-row flex items-center gap-1">
          <MenuItem className={cn("flex-1", m.id === line.model && "on font-medium")} onClick={() => choose(m.id)}>
            <span className="flex size-3.5 items-center justify-center">{m.id === line.model && <Check />}</span>
            <span className="truncate">{m.label}</span>
          </MenuItem>
          {(m.efforts ?? []).map((e) => (
            <button
              key={e}
              type="button"
              className={cn(
                "effort shrink-0 rounded px-1.5 py-0.5 text-2xs text-muted-foreground hover:bg-accent hover:text-foreground",
                m.id === line.model && e === line.effort && "bg-accent text-foreground",
              )}
              onClick={() => choose(m.id, e)}
            >
              {e}
            </button>
          ))}
        </div>
      ))}
    </div>
  );
}

function Mark({ kind, theme, className }: { kind: string; theme: Theme | null; className?: string }) {
  return (
    <span className={cn("grid size-4 shrink-0 place-items-center font-mono text-[11px]", className)} style={{ color: kindColour(kind) }}>
      {kindMark(theme, kind) || "●"}
    </span>
  );
}

const statusRail = { busy: "", waiting: "", idle: "opacity-50" } as const;

function Chip({
  line,
  status,
  theme,
  onInterrupt,
  onMention,
  listModels,
  setModel,
}: {
  line: AgentLine;
  status: "busy" | "waiting" | "idle";
  theme: Theme | null;
  onInterrupt: () => void;
  onMention: () => void;
  listModels: () => Promise<ModelChoice[] | null>;
  setModel: (model: string, effort?: string) => void;
}) {
  const [load, setLoad] = useState<Promise<ModelChoice[] | null> | null>(null);
  const colour = agentColour(line.id, line.kind);
  const close = () => setLoad(null);
  return (
    <div className={`agent agent-${status}`} data-agent={line.id}>
      <Popover
        open={load !== null}
        onClose={close}
        side="top"
        label={`Models for ${line.kind}`}
        className="w-64"
        trigger={
          <button
            type="button"
            className={cn(
              "agent-model relative flex h-6 max-w-48 cursor-pointer items-center gap-1.5 rounded-md pr-1.5 pl-2.5 text-muted-foreground text-xs outline-none transition-colors hover:bg-hover hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring",
              load !== null && "bg-selected text-foreground",
            )}
            title={`${line.id}: ${line.model ?? "default model"} (${status})`}
            aria-label={`${line.id} (${status})`}
            onClick={() => (load === null ? setLoad(listModels()) : close())}
          >
            {/* The agent's hue as a 2px rail, never as a fill. */}
            <span
              aria-hidden
              className={cn("agent-dot absolute inset-y-1 left-1 w-0.5 rounded-full bg-current", statusRail[status])}
              style={{ color: status === "waiting" ? "var(--warning)" : colour }}
            />
            <span className="agent-name truncate font-mono text-2xs">
              {kindMark(theme, line.kind)} {line.name}
            </span>
            {status === "busy" && <span aria-hidden className="size-1.5 shrink-0 animate-deck-pulse rounded-full bg-signal" />}
            <span
              className={cn(
                "agent-status shrink-0",
                status === "busy" && "status-busy text-info-foreground",
                status === "waiting" && "status-waiting text-warning-foreground",
                status === "idle" && "sr-only",
              )}
            >
              {status}
            </span>
          </button>
        }
      >
        <MenuLabel>
          {line.id} · {line.model ?? "default model"}
        </MenuLabel>
        <Models
          line={line}
          load={load ?? Promise.resolve(null)}
          choose={(model, effort) => {
            setModel(model, effort);
            close();
          }}
        />
        <MenuSeparator />
        <MenuItem
          onClick={() => {
            close();
            onMention();
          }}
        >
          <AtSign /> Message ${line.id}
        </MenuItem>
        {line.busy && (
          <MenuItem
            className="agent-stop text-destructive-foreground"
            aria-label={`Interrupt ${line.id}`}
            onClick={() => {
              close();
              onInterrupt();
            }}
          >
            <Square /> Stop {line.id}
          </MenuItem>
        )}
      </Popover>
    </div>
  );
}

// Every agent in the thread, in the composer's context strip: its state
// (busy, waiting for an approval, idle) and a menu for its model.
export function AgentsStrip({
  sessionId,
  roster,
  approvals,
  theme,
  onInterrupt,
  onMention,
  listModels,
  setModel,
}: {
  sessionId: string;
  roster: AgentLine[];
  approvals: Approval[];
  theme: Theme | null;
  onInterrupt: (agent: string) => void;
  onMention: (agent: string) => void;
  listModels: (kind: string) => Promise<ModelChoice[] | null>;
  setModel: (kind: string, model: string, effort?: string) => void;
}) {
  return (
    <div className="agents flex min-w-0 flex-wrap items-center gap-0.5" aria-label="Agents in this thread">
      {roster.map((l) => (
        <Chip
          key={l.id}
          line={l}
          status={agentStatus(l, sessionId, approvals)}
          theme={theme}
          onInterrupt={() => onInterrupt(l.id)}
          onMention={() => onMention(l.id)}
          listModels={() => listModels(l.kind)}
          setModel={(model, effort) => setModel(l.kind, model, effort)}
        />
      ))}
      {roster.length === 0 && <span className="px-1.5 text-muted-foreground/60 text-xs">No agents yet</span>}
    </div>
  );
}

// The agents a message can go to, each in a card of its own: tapping one
// addresses it ("$codex"), tapping several addresses each, and none leaves
// the message to whoever spoke last. Starting another agent is a separate
// action. Before anyone has joined, the cards are the installed CLIs.
export function AgentCards({
  draft,
  setDraft,
  roster,
  kinds,
  theme,
  sessionId,
  approvals,
  onAdd,
  onFocus,
}: {
  draft: string;
  setDraft: (s: string) => void;
  roster: AgentLine[];
  kinds: string[];
  theme: Theme | null;
  sessionId: string | null;
  approvals: Approval[];
  onAdd: (kind: string) => void;
  onFocus: () => void;
}) {
  const [adding, setAdding] = useState(false);
  const addressed = activeAgents(draft);
  const cards =
    roster.length > 0
      ? roster.map((l) => ({
          id: l.id,
          name: l.name,
          kind: l.kind,
          detail: l.model ?? "default model",
          status: sessionId === null ? ("idle" as const) : agentStatus(l, sessionId, approvals),
        }))
      : kinds.map((k) => ({ id: k, name: k, kind: k, detail: "starts on send", status: null }));
  return (
    <div className="agent-cards flex flex-wrap items-stretch gap-2" role="group" aria-label="Send to">
      {cards.map((c) => {
        const on = addressed.includes(c.id);
        const colour = kindColour(c.kind);
        return (
          <button
            key={c.id}
            type="button"
            role="checkbox"
            aria-checked={on}
            aria-label={`Send to ${c.id}`}
            data-agent={c.id}
            className={cn(
              "agent-card relative flex min-w-40 cursor-pointer items-center gap-2.5 overflow-hidden rounded-lg border border-hairline bg-surface-1 py-2 pr-2.5 pl-3.5 text-left outline-none inset-shadow-hl transition-colors before:absolute before:inset-y-0 before:left-0 before:w-0.5 before:bg-current hover:bg-surface-2 focus-visible:ring-2 focus-visible:ring-ring",
              on && "on border-foreground/25 bg-surface-2",
            )}
            style={{ color: colour }}
            onClick={() => {
              setDraft(toggleAgent(draft, c.id));
              onFocus();
            }}
          >
            <span aria-hidden className="grid size-5 shrink-0 place-items-center font-mono text-xs">
              {kindMark(theme, c.kind) || "●"}
            </span>
            <span className="min-w-0 flex-1">
              <span className="block truncate font-medium text-foreground text-sm leading-4">{c.name}</span>
              <span className="block truncate font-mono text-2xs text-muted-foreground leading-4">
                {c.detail}
                {c.status === "busy" ? " · working" : c.status === "waiting" ? " · needs you" : ""}
              </span>
            </span>
            {c.status !== null && (
              <span
                aria-hidden
                className={cn(
                  "size-1.5 shrink-0 rounded-full",
                  c.status === "busy" ? "animate-deck-pulse bg-signal" : c.status === "waiting" ? "bg-warning" : "bg-success/70",
                )}
              />
            )}
          </button>
        );
      })}
      {roster.length > 0 && (
        <Popover
          open={adding}
          onClose={() => setAdding(false)}
          side="top"
          label="Add an agent"
          className="w-64"
          trigger={
            <button
              type="button"
              className="add-agent flex cursor-pointer items-center gap-1.5 rounded-lg border border-hairline-strong border-dashed px-3.5 text-muted-foreground text-sm outline-none transition-colors hover:border-foreground/30 hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring aria-expanded:text-foreground"
              aria-expanded={adding}
              onClick={() => setAdding(!adding)}
            >
              <Plus className="size-4" /> Add agent
            </button>
          }
        >
          <MenuLabel>Start another agent</MenuLabel>
          {kinds.map((k) => (
            <MenuItem
              key={k}
              aria-label={`Start another ${k}`}
              onClick={() => {
                setAdding(false);
                onAdd(k);
              }}
            >
              <Mark kind={k} theme={theme} />
              <span className="flex-1">{k}</span>
              {roster.some((l) => l.kind === k) && <span className="text-2xs text-muted-foreground">another</span>}
            </MenuItem>
          ))}
          {kinds.length === 0 && <p className="px-2 py-1.5 text-muted-foreground text-xs">This Deck names no agents.</p>}
        </Popover>
      )}
    </div>
  );
}
