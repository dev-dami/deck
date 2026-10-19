import { activeAgent } from "@web/composer";
import type { AgentLine, Approval, ModelChoice, Theme } from "@web/protocol";
import { kindColour, kindMark } from "@web/theme";
import { AtSign, Check, ChevronDown, Loader2, Plus, Sparkles, Square } from "lucide-react";
import { useEffect, useState } from "react";
import { agentColour } from "../agents";
import { agentStatus } from "../approvals";
import { cn } from "../lib/cn";
import { MenuItem, MenuLabel, MenuSeparator, Popover } from "./kit/menu";

function Models({
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

// The composer's picker, where T3 Code picks a provider and model: who the
// next message goes to, another agent to start, and the model of the agent
// it goes to.
export function AgentPicker({
  draft,
  roster,
  kinds,
  theme,
  onAddress,
  onAuto,
  onAdd,
  listModels,
  setModel,
}: {
  draft: string;
  roster: AgentLine[];
  kinds: string[];
  theme: Theme | null;
  onAddress: (agent: string) => void;
  onAuto: () => void;
  onAdd: (kind: string) => void;
  listModels: (kind: string) => Promise<ModelChoice[] | null>;
  setModel: (kind: string, model: string, effort?: string) => void;
}) {
  const [open, setOpen] = useState(false);
  const [load, setLoad] = useState<Promise<ModelChoice[] | null> | null>(null);
  const mention = activeAgent(draft);
  const target = mention === null ? undefined : roster.find((l) => l.id === mention);
  const targetKind = target?.kind ?? (mention !== null && kinds.includes(mention.replace(/[+*]$/, "")) ? mention : null);
  const close = () => {
    setOpen(false);
    setLoad(null);
  };
  return (
    <Popover
      open={open}
      onClose={close}
      side="top"
      label="Agents"
      className="w-72"
      trigger={
        <button
          type="button"
          className="agent-picker flex h-8 min-w-0 cursor-pointer items-center gap-1.5 rounded-md px-2 text-muted-foreground text-sm hover:bg-accent hover:text-foreground aria-expanded:bg-accent"
          aria-expanded={open}
          aria-label="Pick an agent"
          onClick={() => {
            if (open) return close();
            setOpen(true);
            // The models of the agent the draft addresses.
            if (target !== undefined) setLoad(listModels(target.kind));
          }}
        >
          {target !== undefined ? (
            <>
              <Mark kind={target.kind} theme={theme} />
              <span className="truncate text-foreground">{target.id}</span>
              {target.model !== undefined && <span className="truncate">{target.model}</span>}
            </>
          ) : targetKind !== null && mention !== null ? (
            <>
              <Mark kind={targetKind} theme={theme} />
              <span className="truncate text-foreground">${mention}</span>
            </>
          ) : (
            <>
              <Sparkles className="size-4 shrink-0" />
              <span className="truncate text-foreground">{roster.length === 0 ? "Pick an agent" : "Any agent"}</span>
            </>
          )}
          <ChevronDown className="size-3.5 shrink-0" />
        </button>
      }
    >
      {roster.length > 0 && (
        <>
          <MenuLabel>Send to</MenuLabel>
          <MenuItem
            onClick={() => {
              close();
              onAuto();
            }}
          >
            <Sparkles />
            <span className="flex-1">Any agent</span>
            {mention === null && <Check />}
          </MenuItem>
          {roster.map((l) => (
            <MenuItem
              key={l.id}
              onClick={() => {
                close();
                onAddress(l.id);
              }}
            >
              <Mark kind={l.kind} theme={theme} />
              <span className="flex-1 truncate">
                {l.id} <span className="text-muted-foreground">{l.model ?? ""}</span>
              </span>
              {l.busy && <span className="text-2xs text-muted-foreground">busy</span>}
              {mention === l.id && <Check />}
            </MenuItem>
          ))}
          <MenuSeparator />
        </>
      )}
      <MenuLabel>Start another</MenuLabel>
      {kinds.map((k) => (
        <MenuItem
          key={k}
          aria-label={`Start another ${k}`}
          onClick={() => {
            close();
            onAdd(k);
          }}
        >
          <Mark kind={k} theme={theme} />
          <span className="flex-1">{k}</span>
          <Plus className="text-muted-foreground" />
        </MenuItem>
      ))}
      {kinds.length === 0 && <p className="px-2 py-1.5 text-muted-foreground text-xs">This Deck names no agents.</p>}
      {target !== undefined && load !== null && (
        <>
          <MenuSeparator />
          <MenuLabel>Model for {target.id}</MenuLabel>
          <Models
            line={target}
            load={load}
            choose={(model, effort) => {
              setModel(target.kind, model, effort);
              close();
            }}
          />
        </>
      )}
    </Popover>
  );
}

const statusDot = { busy: "animate-deck-pulse", waiting: "", idle: "opacity-50" } as const;

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
              "agent-model flex h-6 max-w-48 cursor-pointer items-center gap-1.5 rounded-md px-1.5 text-muted-foreground/80 text-xs transition-colors hover:bg-accent hover:text-foreground",
              load !== null && "bg-accent text-foreground",
            )}
            title={`${line.id}: ${line.model ?? "default model"} (${status})`}
            aria-label={`${line.id} (${status})`}
            onClick={() => (load === null ? setLoad(listModels()) : close())}
          >
            <span
              className={cn("agent-dot size-1.5 shrink-0 rounded-full", statusDot[status])}
              style={{ background: status === "waiting" ? "var(--warning)" : colour }}
            />
            <span className="agent-name truncate">
              {kindMark(theme, line.kind)} {line.name}
            </span>
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
