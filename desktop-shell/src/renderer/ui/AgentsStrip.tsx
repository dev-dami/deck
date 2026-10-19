import type { AgentLine, Approval, ModelChoice, Theme } from "@web/protocol";
import { kindColour, kindMark } from "@web/theme";
import { useEffect, useState } from "react";
import { agentColour } from "../agents";
import { agentStatus } from "../approvals";

function ModelMenu({
  line,
  load,
  choose,
  close,
}: {
  line: AgentLine;
  load: Promise<ModelChoice[] | null>;
  choose: (model: string, effort?: string) => void;
  close: () => void;
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
  return (
    <div className="menu" role="menu" aria-label={`Models for ${line.kind}`} onMouseLeave={close}>
      {models === "loading" && <p className="muted">Loading…</p>}
      {models === null && <p className="muted">No models.</p>}
      {Array.isArray(models) &&
        models.map((m) => (
          <div key={m.id} className="menu-row">
            <button
              type="button"
              role="menuitem"
              className={m.id === line.model ? "on" : ""}
              onClick={() => choose(m.id)}
            >
              {m.label}
            </button>
            {(m.efforts ?? []).map((e) => (
              <button key={e} type="button" className="effort" onClick={() => choose(m.id, e)}>
                {e}
              </button>
            ))}
          </div>
        ))}
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
  listModels,
  setModel,
}: {
  sessionId: string;
  roster: AgentLine[];
  approvals: Approval[];
  kinds: string[];
  theme: Theme | null;
  onInterrupt: (agent: string) => void;
  onAdd: (kind: string) => void;
  listModels: (kind: string) => Promise<ModelChoice[] | null>;
  setModel: (kind: string, model: string, effort?: string) => void;
}) {
  const [adding, setAdding] = useState(false);
  const [models, setModels] = useState<{ line: AgentLine; load: Promise<ModelChoice[] | null> } | null>(null);
  return (
    <div className="agents" aria-label="Agents in this thread">
      {roster.map((l) => {
        const status = agentStatus(l, sessionId, approvals);
        const colour = agentColour(l.id, l.kind);
        return (
          <div key={l.id} className={`agent agent-${status}`} data-agent={l.id}>
            <span className="agent-dot" style={{ background: colour }} />
            <span className="agent-name" style={{ color: colour }}>
              {kindMark(theme, l.kind)} {l.name}
            </span>
            <button
              type="button"
              className="agent-model"
              title="Change the model"
              onClick={() => setModels({ line: l, load: listModels(l.kind) })}
            >
              {l.model ?? "model"}
              {l.effort !== undefined ? ` · ${l.effort}` : ""}
            </button>
            <span className={`agent-status status-${status}`}>{status}</span>
            {l.busy && (
              <button type="button" className="agent-stop" onClick={() => onInterrupt(l.id)} aria-label={`Interrupt ${l.id}`}>
                Stop
              </button>
            )}
          </div>
        );
      })}
      {roster.length === 0 && <span className="muted">No agents yet. The first message starts one.</span>}
      <div className="add-agent">
        <button type="button" onClick={() => setAdding((a) => !a)} aria-expanded={adding}>
          + Add agent
        </button>
        {adding && (
          <div className="menu" role="menu" aria-label="Add agent" onMouseLeave={() => setAdding(false)}>
            {kinds.map((k) => (
              <button
                key={k}
                type="button"
                role="menuitem"
                style={{ color: kindColour(k) }}
                onClick={() => {
                  setAdding(false);
                  onAdd(k);
                }}
              >
                {kindMark(theme, k)} {k}
              </button>
            ))}
          </div>
        )}
      </div>
      {models !== null && (
        <ModelMenu
          line={models.line}
          load={models.load}
          close={() => setModels(null)}
          choose={(model, effort) => {
            setModel(models.line.kind, model, effort);
            setModels(null);
          }}
        />
      )}
    </div>
  );
}
