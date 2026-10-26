import { Check, MessageCircleQuestion, Pencil } from "./icons";
import { type CSSProperties, useEffect, useState } from "react";
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

// A plan as an artefact, set off from the prose by two hairlines rather
// than boxed: the heading, the steps in
// prose type, numbered in the agent's hue (ticked once approved),
// and Approve, Edit and Ask. Buttons show on the room's latest plan only,
// never in a read-only room.
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
      className="plan-card flex flex-col gap-3 border-hairline-strong border-y py-3.5"
      aria-label={plan.heading ?? "Plan"}
      data-plan={id}
      style={{ "--hue": colour } as CSSProperties}
    >
      <header className="flex items-baseline gap-2.5">
        <h3 className="plan-heading min-w-0 truncate font-semibold text-[13px] text-foreground">{plan.heading ?? "Plan"}</h3>
        <span className="text-2xs text-faint tabular-nums">
          {plan.steps.length} steps
        </span>
        <span className="plan-index ml-auto shrink-0 font-mono text-(--hue) text-2xs">{index}</span>
      </header>
      {/* The steps, numbered in type in the agent's hue; ticked once the
          plan is approved. */}
      <ol className="plan-steps flex flex-col gap-2.5">
        {plan.steps.map((s, i) => (
          <li key={i} className="flex items-baseline gap-3 text-prose">
            <span aria-hidden className="w-4 shrink-0 text-right font-mono text-(--hue) text-xs tabular-nums">
              {state === null ? `${i + 1}` : "✓"}
            </span>
            <span className="sr-only">{i + 1}.</span>
            <div className="min-w-0 flex-1 [&_.md_p]:m-0">
              <Markdown text={s} />
            </div>
          </li>
        ))}
      </ol>
      {state !== null ? (
        <p className="plan-state pl-7 text-2xs text-muted-foreground">
          {state === "auto" ? "Approved automatically. The agent goes ahead." : "Approved. The agent goes ahead."}
        </p>
      ) : (
        actions !== null && (
          <div className="plan-actions flex flex-wrap items-center gap-1.5 pl-7">
            <Button size="sm" disabled={busy} onClick={() => void approve("manual")}>
              <Check weight="bold" />
              Approve
            </Button>
            <Button size="sm" variant="outline" onClick={() => actions.edit(agent, plan.steps)}>
              <Pencil />
              Edit
            </Button>
            <Button size="sm" variant="ghost-muted" onClick={() => actions.ask(agent)}>
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
