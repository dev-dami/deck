import { useState } from "preact/hooks";
import type { AgentLine, ModelChoice } from "../protocol";
import { useController, useLoad } from "./hooks";
import { Sheet } from "./Sheet";

interface AgentsSheetProps {
  sessionId: string;
  roster: AgentLine[];
  onClose: () => void;
}

export function AgentsSheet({ sessionId, roster, onClose }: AgentsSheetProps) {
  const [picking, setPicking] = useState<AgentLine | null>(null);
  const controller = useController();

  if (picking !== null) {
    return (
      <ModelPicker
        sessionId={sessionId}
        agent={picking}
        onBack={() => setPicking(null)}
        onClose={onClose}
      />
    );
  }

  return (
    <Sheet title="Agents" onClose={onClose}>
      {roster.length === 0 ? (
        <p class="muted">No agents in this session yet.</p>
      ) : (
        <ul class="list">
          {roster.map((a) => (
            <li key={a.name} class="agent-row">
              <div class="row-main">
                <span class="row-title">
                  {a.busy ? <span class="busy-dot" role="img" aria-label="Working" /> : null}$
                  {a.name}
                </span>
                <span class="row-sub">
                  {a.kind}
                  {a.model !== undefined ? ` / ${a.model}` : ""}
                  {a.effort !== undefined ? ` / ${a.effort}` : ""}
                </span>
              </div>
              <div class="btn-row">
                <button type="button" class="btn" onClick={() => setPicking(a)}>
                  Model
                </button>
                {a.busy ? (
                  <button
                    type="button"
                    class="btn danger"
                    onClick={() => void controller.interrupt(sessionId, a.name)}
                  >
                    Interrupt
                  </button>
                ) : null}
              </div>
            </li>
          ))}
        </ul>
      )}
    </Sheet>
  );
}

interface ModelPickerProps {
  sessionId: string;
  agent: AgentLine;
  onBack: () => void;
  onClose: () => void;
}

function ModelPicker({ sessionId, agent, onBack, onClose }: ModelPickerProps) {
  const controller = useController();
  const models = useLoad<ModelChoice[]>(
    () => controller.listModels(sessionId, agent.kind),
    `${sessionId}:${agent.kind}`,
  );
  const [model, setModel] = useState<string | null>(null);
  const [effort, setEffort] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  const choices = models.data ?? [];
  const selectedId = model ?? choices.find((c) => c.selected === true)?.id ?? agent.model ?? null;
  const selected = choices.find((c) => c.id === selectedId);
  const efforts = selected?.efforts ?? [];
  const effortValue = effort !== null && efforts.includes(effort) ? effort : (agent.effort ?? null);

  const apply = async () => {
    if (selectedId === null) return;
    setSaving(true);
    const chosenEffort =
      effortValue !== null && efforts.includes(effortValue) ? effortValue : undefined;
    await controller.setModel(sessionId, agent.kind, selectedId, chosenEffort);
    setSaving(false);
    onClose();
  };

  return (
    <Sheet title={`$${agent.name} model`} onClose={onClose}>
      {models.loading ? <p class="muted">Loading models...</p> : null}
      {!models.loading && choices.length === 0 ? (
        <p class="muted">No models to choose from.</p>
      ) : null}
      <ul class="list">
        {choices.map((c) => (
          <li key={c.id}>
            <button
              type="button"
              class={`row option ${c.id === selectedId ? "selected" : ""}`}
              aria-pressed={c.id === selectedId}
              onClick={() => {
                setModel(c.id);
                setEffort(null);
              }}
            >
              <span class="row-title">{c.label}</span>
            </button>
          </li>
        ))}
      </ul>
      {efforts.length > 0 ? (
        <fieldset class="efforts">
          <legend>Effort</legend>
          <div class="chips">
            {efforts.map((e) => (
              <button
                key={e}
                type="button"
                class={`chip ${e === effortValue ? "active" : ""}`}
                aria-pressed={e === effortValue}
                onClick={() => setEffort(e)}
              >
                {e}
              </button>
            ))}
          </div>
        </fieldset>
      ) : null}
      <div class="btn-row">
        <button type="button" class="btn" onClick={onBack}>
          Back
        </button>
        <button
          type="button"
          class="btn primary"
          disabled={saving || selectedId === null}
          onClick={() => void apply()}
        >
          {saving ? "Applying" : "Apply"}
        </button>
      </div>
    </Sheet>
  );
}
