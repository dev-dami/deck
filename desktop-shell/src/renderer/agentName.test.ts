import { describe, expect, test } from "vitest";
import { agentName, atName } from "./agentName";

describe("agentName", () => {
  test("a kind's agents read as people say them, not as ids", () => {
    expect(agentName("claude-1")).toBe("Claude");
    expect(agentName("claude1")).toBe("Claude");
    expect(agentName("claude")).toBe("Claude");
    expect(agentName("claude2")).toBe("Claude 2");
    expect(agentName("codex-3")).toBe("Codex 3");
    expect(agentName("opencode")).toBe("OpenCode");
  });

  test("a named profile, an unknown id and a subagent keep their own words", () => {
    expect(agentName("charles")).toBe("charles");
    expect(agentName("w2")).toBe("w2");
    expect(agentName("claude-1/explore")).toBe("Claude/explore");
    expect(agentName("")).toBe("");
    expect(atName("codex-1")).toBe("@Codex");
  });
});
