import type { AgentLine, Approval, ModelChoice, Theme } from "@web/protocol";
import { kindColour, kindMark } from "@web/theme";
import { AtSign, Check, ChevronDown, Loader2, Plus, Square } from "lucide-react";
import { useEffect, useState } from "react";
import { agentColour } from "../agents";
import { agentStatus } from "../approvals";
import { cn } from "../lib/cn";
import { Button } from "./kit/button";
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
    <div className="max-h-64 overflow-y-auto">
      {models.map((m) => (
        <div key={m.id} className="menu-row flex items-center gap-1">
          <MenuItem className={cn("flex-1", m.id === line.model && "on font-medium")} onClick={() => choose(m.id)}>
            <span className="flex size-3.5 items-center justify-center">
              {m.id === line.model && <Check />}
            </span>
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

const statusDot = {
  busy: "animate-deck-pulse",
  waiting: "",
  idle: "opacity-60",
} as const;

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
              "agent-model flex h-7 max-w-56 items-center gap-1.5 rounded-full border px-2.5 text-xs transition-colors hover:bg-accent",
              status === "waiting" && "border-warning/50 bg-warning/8",
              load !== null && "bg-accent",
            )}
            title={`${line.id}: ${line.model ?? "default model"} — ${status}`}
            aria-label={`${line.id} (${status})`}
            onClick={() => (load === null ? setLoad(listModels()) : close())}
          >
            <span
              className={cn("agent-dot size-1.5 shrink-0 rounded-full", statusDot[status])}
              style={{ background: status === "waiting" ? "var(--warning)" : colour }}
            />
            <span className="agent-name shrink-0 font-medium" style={{ color: colour }}>
              {kindMark(theme, line.kind)} {line.name}
            </span>
            <span className="min-w-0 truncate text-muted-foreground">
              {line.model ?? "default"}
              {line.effort !== undefined ? ` · ${line.effort}` : ""}
            </span>
            <span
              className={cn(
                "agent-status shrink-0",
                status === "busy" && "status-busy text-muted-foreground",
                status === "waiting" && "status-waiting text-warning-foreground",
                status === "idle" && "sr-only",
              )}
            >
              {status}
            </span>
            <ChevronDown className="size-3 shrink-0 text-muted-foreground" />
          </button>
        }
      >
        <MenuLabel>
          {line.id} · {line.kind}
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

// Every agent in the thread: who it is, what it runs and whether it works,
// waits for an approval or idles. "Add agent" starts another of a kind.
export function AgentsStrip({
  sessionId,
  roster,
  approvals,
  kinds,
  theme,
  onInterrupt,
  onAdd,
  onMention,
  listModels,
  setModel,
}: {
  sessionId: string | null;
  roster: AgentLine[];
  approvals: Approval[];
  kinds: string[];
  theme: Theme | null;
  onInterrupt: (agent: string) => void;
  onAdd: (kind: string) => void;
  onMention: (agent: string) => void;
  listModels: (kind: string) => Promise<ModelChoice[] | null>;
  setModel: (kind: string, model: string, effort?: string) => void;
}) {
  const [adding, setAdding] = useState(false);
  return (
    <div className="agents flex min-w-0 flex-wrap items-center gap-1.5" aria-label="Agents in this thread">
      <Popover
        open={adding}
        onClose={() => setAdding(false)}
        side="top"
        label="Add agent"
        trigger={
          <Button
            variant="ghost-muted"
            size="xs"
            className="add-agent h-7 rounded-full"
            aria-expanded={adding}
            aria-label="Add agent"
            title="Add an agent"
            onClick={() => setAdding((a) => !a)}
          >
            <Plus />
            {roster.length === 0 && <span>Agent</span>}
          </Button>
        }
      >
        <MenuLabel>Start another agent</MenuLabel>
        {kinds.map((k) => (
          <MenuItem
            key={k}
            onClick={() => {
              setAdding(false);
              onAdd(k);
            }}
          >
            <span className="w-4 text-center font-mono" style={{ color: kindColour(k) }}>
              {kindMark(theme, k) || "●"}
            </span>
            {k}
          </MenuItem>
        ))}
        {kinds.length === 0 && <p className="px-2 py-1.5 text-muted-foreground text-xs">This Deck names no agents.</p>}
      </Popover>
      {roster.map((l) => (
        <Chip
          key={l.id}
          line={l}
          status={sessionId === null ? "idle" : agentStatus(l, sessionId, approvals)}
          theme={theme}
          onInterrupt={() => onInterrupt(l.id)}
          onMention={() => onMention(l.id)}
          listModels={() => listModels(l.kind)}
          setModel={(model, effort) => setModel(l.kind, model, effort)}
        />
      ))}
    </div>
  );
}
