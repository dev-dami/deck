import { Check, MessageCircleQuestion, Pencil } from "./icons";
import { useEffect, useState } from "react";
import type { Plan } from "../plans";
import { usePrefs } from "../prefs";
import { Button } from "./kit/button";
import { Markdown } from "./Markdown";

export interface PlanActions {
  // Switches the room out of plan and tells the agent to go ahead.
  approve(agent: string): Promise<boolean>;
  edit(agent: string, steps: readonly string[]): void;
  ask(agent: string): void;
}

// The plans approved in this window, by message id: the card stays a card
// after the room leaves plan mode, and approves itself once at most.
const approved = new Map<string, "manual" | "auto">();

// A plan as an artefact: the heading, the steps as a checklist in prose
// type, and Approve, Edit and Ask. The rail is the agent's hue. Buttons show
// on the room's latest plan only, never in a read-only room.
export function PlanCard({
  id,
  plan,
  agent,
  colour,
  index,
  actions,
}: {
  id: string;
  plan: Plan;
  agent: string;
  colour: string;
  // The room's corner index: the agent's glyph and the room's short id.
  index: string;
  actions: PlanActions | null;
}) {
  const prefs = usePrefs();
  const [state, setState] = useState(() => approved.get(id) ?? null);
  const [busy, setBusy] = useState(false);

  const approve = async (how: "manual" | "auto") => {
    if (actions === null || approved.has(id)) return;
    approved.set(id, how);
    setBusy(true);
    const ok = await actions.approve(agent);
    setBusy(false);
    if (ok) setState(how);
    else approved.delete(id);
  };

  // Once, when the card appears with the preference on.
  useEffect(() => {
    if (prefs.autoApprovePlans && actions !== null && !approved.has(id)) void approve("auto");
  }, [id, prefs.autoApprovePlans, actions]);

  return (
    <section
      className="plan-card relative overflow-hidden rounded-lg border border-hairline bg-surface-1 py-3 pr-4 pl-5 inset-shadow-hl"
      aria-label={plan.heading ?? "Plan"}
      data-plan={id}
    >
      <span aria-hidden className="absolute inset-y-0 left-0 w-0.5" style={{ background: colour }} />
      <header className="mb-2 flex items-baseline gap-2">
        <span className="plan-index shrink-0 font-mono text-2xs" style={{ color: colour }}>
          {index}
        </span>
        <h3 className="plan-heading min-w-0 truncate font-medium text-[13px] text-foreground">{plan.heading ?? "Plan"}</h3>
        <span className="font-mono text-2xs text-faint tabular-nums">
          {plan.steps.length} steps
        </span>
      </header>
      <ol className="plan-steps flex flex-col gap-2">
        {plan.steps.map((s, i) => (
          <li key={i} className="flex items-start gap-3 text-prose">
            <span
              aria-hidden
              className="mt-[0.4em] grid size-3.5 shrink-0 place-items-center rounded-[4px] border border-hairline-strong"
            />
            <span className="sr-only">{i + 1}.</span>
            <div className="min-w-0 flex-1 [&_.md_p]:m-0">
              <Markdown text={s} />
            </div>
          </li>
        ))}
      </ol>
      {state !== null ? (
        <p className="plan-state mt-3 text-2xs text-faint">
          {state === "auto" ? "Approved automatically. The agent goes ahead." : "Approved. The agent goes ahead."}
        </p>
      ) : (
        actions !== null && (
          <div className="plan-actions mt-3 flex flex-wrap items-center gap-1.5">
            <Button size="xs" disabled={busy} onClick={() => void approve("manual")}>
              <Check />
              Approve
            </Button>
            <Button size="xs" variant="outline" onClick={() => actions.edit(agent, plan.steps)}>
              <Pencil />
              Edit
            </Button>
            <Button size="xs" variant="ghost-muted" onClick={() => actions.ask(agent)}>
              <MessageCircleQuestion />
              Ask
            </Button>
          </div>
        )
      )}
    </section>
  );
}

// Whether this window approved the plan in a message.
export function wasApproved(id: string): boolean {
  return approved.has(id);
}
