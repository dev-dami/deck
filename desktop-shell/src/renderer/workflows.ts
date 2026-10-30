import type { Level } from "./orchestration";

// Multi-agent workflows: what the new-thread screen offers beside the one
// agent every thread starts with. A thread is a single agent unless the
// person picks one of these. Each says who works, at which orchestration
// level, and how it ends, so a crew never runs open-ended: the engine's
// turn budget, its review rounds (store.MaxReviewChanges) and the race's
// keep-one-entry are the stops.
//
// The presets follow systems that run agents as a team without looping:
// a lead with an independent reviewer of another kind (MetaGPT's roles,
// MAST's verifier), best-of-N with a judge (Codex attempts), a split by
// area that a lead integrates (Magentic-One's orchestrator), and two
// independent answers for a decision.

export type WorkflowId = "plan" | "review" | "race" | "split" | "second" | "factory";

export interface Workflow {
  id: WorkflowId;
  title: string;
  // One line: who does what.
  line: string;
  // How it ends, for the chip over the composer.
  ends: string;
  // The thread's orchestration level.
  level: Level;
  // The agent kinds it uses, the lead first.
  crew: string[];
  // A race opens the race builder; the others send their first message.
  race: boolean;
  // The mode the thread starts in: "plan" waits for the person to approve
  // a plan before anything changes.
  mode?: "plan";
}

// The lead is the default agent when installed; the other is the first
// installed kind that is not the lead, for a second opinion and review.
export function crewOf(kinds: readonly string[], lead: string): { lead: string; other: string } {
  const one = kinds.includes(lead) ? lead : (kinds[0] ?? lead);
  const other = kinds.find((k) => k !== one) ?? one;
  return { lead: one, other };
}

export function workflows(kinds: readonly string[], defaultKind: string): Workflow[] {
  const { lead, other } = crewOf(kinds, defaultKind);
  const two = other !== lead;
  const list: Workflow[] = [
    {
      id: "plan",
      title: "Plan, build, review",
      line: `${lead} plans; you approve; it builds and ${other} reviews`,
      ends: `ends when ${other} approves, or after 3 rounds of changes you decide`,
      level: "assist",
      crew: [lead, other],
      race: false,
      mode: "plan",
    },
    {
      id: "review",
      title: "Build and review",
      line: `${lead} builds it, ${other} reviews before it is done`,
      ends: `ends when ${other} approves, or after 3 rounds of changes you decide`,
      level: "assist",
      crew: [lead, other],
      race: false,
    },
    {
      id: "race",
      title: "Best of several",
      line: "Agents try it on their own branches; keep the best",
      ends: "ends when you keep one entry",
      level: "off",
      crew: two ? [lead, other] : [lead, lead],
      race: true,
    },
    {
      id: "split",
      title: "Split by area",
      line: `${lead} plans the parts, helpers build them on worktrees, ${lead} merges`,
      ends: "ends when every part is merged and the tests pass",
      level: "auto",
      crew: [lead],
      race: false,
    },
    {
      id: "factory",
      title: "Full crew",
      line: `${lead} leads a team on the board; every helper's work is reviewed`,
      ends: "pauses after 96 turns the agents start without you",
      level: "full",
      crew: [lead],
      race: false,
    },
  ];
  if (two) {
    list.splice(2, 0, {
      id: "second",
      title: "Second opinion",
      line: `${lead} and ${other} answer on their own; you compare`,
      ends: "ends when both have answered",
      level: "off",
      crew: [lead, other],
      race: false,
    });
  }
  return list;
}

// The first message of a workflow: the person's goal, addressed to who
// starts, then the terms of the workflow for the agents. Terms name the ops
// the engine enforces (task review, worktrees, the board), so the shape of
// the run does not rest on the agents' goodwill alone.
export function workflowMessage(w: Workflow, goal: string): string {
  const g = goal.trim();
  const [lead = "claude", other = lead] = w.crew;
  switch (w.id) {
    case "plan":
      return [
        `@${lead} ${g}`,
        "",
        `[workflow: plan, build and review] First write a plan for the user to approve; change nothing yet. Once they approve it, track the work as one task with execute task.create {title, owner: "${lead}", review: "${other}"}, do it and run the tests, then report it with task.done {id, result}: Deck has ${other} review it and sends you its notes. Fix what it asks; after 3 rounds the user decides.`,
      ].join("\n");
    case "review":
      return [
        `@${lead} ${g}`,
        "",
        `[workflow: build and review] Track this as one task with execute task.create {title, owner: "${lead}", review: "${other}"}. Do the work and run the tests, then report it with task.done {id, result}: Deck has ${other} review it and sends you its notes. Fix what it asks; after 3 rounds the user decides.`,
      ].join("\n");
    case "second":
      return [`@${lead} @${other} ${g}`, "", "[workflow: second opinion] Answer on your own, without messaging each other; the user compares the answers."].join("\n");
    case "split":
      return [
        `@${lead} ${g}`,
        "",
        "[workflow: split by area] Split this into independent parts by area. Put each on the task board with task.create {title, brief, deps, isolate: true}, its brief saying what to hand back and when it is done; Deck runs ready parts with helpers on worktrees of their own. When every part is done, merge each worktree (worktree.merge), run the tests and report what changed.",
      ].join("\n");
    case "factory":
      return [
        `@${lead} ${g}`,
        "",
        "[workflow: full crew] You lead. Plan the work on the task board with deps, give each part a brief with what to hand back and when it is done, and let Deck run the ready parts; another kind of agent reviews each helper's work. Integrate, run the tests and report.",
      ].join("\n");
    case "race":
      return g;
  }
}
