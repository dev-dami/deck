import { describe, expect, test } from "vitest";
import { agentColour, agentTint } from "./agents";
import { type CommandDeps, paletteCommands } from "./palette";
import { defaultPrefs, merge } from "./prefs";
import { gateMentionables, gateTeam, tintOf } from "./teamGate";

const noop = () => {};
const deps = (team: (() => void) | null): CommandDeps => ({
  mac: false,
  terminal: true,
  newThread: noop,
  togglePanel: noop,
  toggleSidePanel: noop,
  toggleSidebar: noop,
  settings: noop,
  usage: noop,
  team,
  inbox: noop,
  nextAttention: noop,
  room: noop,
  toggleTerminal: noop,
  theme: noop,
  addProject: noop,
  switchDeck: noop,
  welcome: noop,
});

describe("the Team switch", () => {
  test("is on by default, and a stored choice survives a load", () => {
    expect(defaultPrefs.team).toBe(true);
    expect(merge({}).team).toBe(true);
    expect(merge({ team: false }).team).toBe(false);
    expect(merge({ team: "off" }).team).toBe(true);
    // Turning it off keeps every other setting.
    expect(merge({ ...defaultPrefs, team: false })).toEqual({ ...defaultPrefs, team: false });
  });

  test("off, the team reads as none; on, as it is", () => {
    const team = { available: true, teammates: [] };
    expect(gateTeam(true, team)).toBe(team);
    expect(gateTeam(false, team)).toBeNull();
    expect(gateTeam(true, null)).toBeNull();
  });

  test("off, @ offers no profiles or people, and still reads as an agent's sigil", () => {
    const named = { profiles: [{ name: "charles", kind: "claude" }], people: [{ name: "alice" }], mentions: true };
    expect(gateMentionables(true, named)).toBe(named);
    expect(gateMentionables(false, named)).toEqual({ profiles: undefined, people: undefined, mentions: true });
  });

  test("off, a named member wears its kind's hue", () => {
    const roster = [{ id: "charles", name: "charles", color: "teal" }];
    expect(agentTint("charles", roster)).toBe("teal");
    expect(agentColour("charles", "claude", tintOf("teal", true))).toBe("teal");
    expect(agentColour("charles", "claude", tintOf("teal", false))).toBe(agentColour("charles", "claude"));
    // A second member from the profile is a shade lighter, as kinds are.
    expect(agentColour("charles-2", "claude", "teal")).toMatch(/^color-mix\(in oklab, teal, white 18%\)$/);
  });

  test("off, the palette has no Open team", () => {
    expect(paletteCommands(deps(noop)).some((c) => c.id === "team")).toBe(true);
    expect(paletteCommands(deps(null)).some((c) => c.id === "team")).toBe(false);
  });
});
