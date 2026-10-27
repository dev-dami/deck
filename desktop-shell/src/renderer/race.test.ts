import { describe, expect, test } from "vitest";
import type { AgentModels } from "./agentCatalog";
import {
  addEntrant,
  canAdd,
  type Entrant,
  entrantDetail,
  modelLabel,
  newRace,
  nextEntrant,
  RACE_MAX,
  raceProblem,
  raceSummary,
  removeEntrant,
  replaceEntrant,
  toWire,
} from "./race";

const ent = (agent: string, model = "", effort = "", kind = agent, profile = false): Entrant => ({ agent, kind, model, effort, profile });
const catalog: AgentModels[] = [
  { kind: "claude", failed: false, models: [{ id: "", label: "Default", efforts: [] }, { id: "opus", label: "Opus", efforts: ["low", "high"] }] },
  { kind: "codex", failed: false, models: [{ id: "", label: "Default", efforts: [] }] },
];

describe("the race builder", () => {
  test("mixes kinds and repeats one on the same model", () => {
    let d = newRace("  fix the flaky limiter test ");
    expect(d).toEqual({ brief: "fix the flaky limiter test", entrants: [], judge: true });
    d = addEntrant(d, ent("codex"));
    d = addEntrant(d, ent("claude", "opus", "high"));
    d = addEntrant(d, ent("claude", "opus", "high"));
    expect(raceProblem(d)).toBeNull();
    expect(toWire(d)).toEqual([{ agent: "codex" }, { agent: "claude", model: "opus", effort: "high" }, { agent: "claude", model: "opus", effort: "high" }]);
    expect(raceSummary(d)).toBe("3 agents: codex, claude ×2");
  });

  test("a named agent enters by its name and races its kind's models", () => {
    const d = addEntrant(addEntrant(newRace("x"), ent("ada", "", "", "claude", true)), ent("pi"));
    expect(toWire(d)).toEqual([{ agent: "ada" }, { agent: "pi" }]);
    expect(entrantDetail(d.entrants[0] as Entrant, catalog)).toBe("Default");
  });

  test("edits and removes an entrant in place", () => {
    let d = addEntrant(addEntrant(newRace("x"), ent("codex")), ent("claude"));
    d = replaceEntrant(d, 1, ent("claude", "opus"));
    expect(d.entrants.map((e) => `${e.agent}/${e.model}`)).toEqual(["codex/", "claude/opus"]);
    expect(replaceEntrant(d, 5, ent("pi"))).toBe(d);
    d = removeEntrant(d, 0);
    expect(d.entrants.map((e) => e.agent)).toEqual(["claude"]);
  });

  test("caps the race at six and says what keeps it from starting", () => {
    let d = newRace("");
    expect(raceProblem(d)).toBe("Add at least 2 agents.");
    d = addEntrant(d, ent("claude"));
    expect(raceProblem(d)).toBe("Add one more agent.");
    d = addEntrant(d, ent("claude"));
    expect(raceProblem(d)).toBe("Say what the race is for.");
    for (let i = 0; i < 10; i++) d = addEntrant(d, ent("codex"));
    expect(d.entrants).toHaveLength(RACE_MAX);
    expect(canAdd(d)).toBe(false);
    expect(raceProblem({ ...d, brief: "go" })).toBeNull();
  });

  test("adding starts on the last entrant, so the same one again is a click", () => {
    expect(nextEntrant(newRace(), ["codex", "claude"])).toEqual(ent("codex"));
    expect(nextEntrant(newRace(), [])).toBeNull();
    const d = addEntrant(newRace(), ent("claude", "opus", "low"));
    expect(nextEntrant(d, ["codex"])).toEqual(ent("claude", "opus", "low"));
  });

  test("labels a model as the picker does", () => {
    expect(modelLabel(catalog, "claude", "opus")).toBe("Opus");
    expect(modelLabel(catalog, "claude", "")).toBe("Default");
    expect(modelLabel(null, "claude", "sonnet-x")).toBe("sonnet-x");
    expect(entrantDetail(ent("claude", "opus", "high"), catalog)).toBe("Opus · high");
  });
});
