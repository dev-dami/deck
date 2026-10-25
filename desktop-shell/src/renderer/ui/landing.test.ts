import { expect, test } from "vitest";
import { AGENT_HELP, startCards, withStart } from "./landing";

const prefixes = (kinds: string[]) => startCards(kinds).map((c) => c.prefix);

test("the start cards name claude and codex when both are installed", () => {
  expect(prefixes(["claude", "codex", "opencode"])).toEqual(["$claude ", "$claude $codex+ ", "$claude×3 "]);
  expect(startCards(["codex", "claude"])[0]?.line).toBe("Ask claude to …");
});

test("the start cards use only the kinds that are installed", () => {
  expect(prefixes(["codex", "opencode"])).toEqual(["$codex ", "$codex $opencode+ ", "$codex×3 "]);
  expect(prefixes(["claude", "opencode"])).toEqual(["$claude ", "$claude $opencode+ ", "$claude×3 "]);
  // One kind splits the work between two agents of that kind.
  expect(prefixes(["opencode"])).toEqual(["$opencode ", "$opencode $opencode+ ", "$opencode×3 "]);
  // Before the Deck answers, claude.
  expect(prefixes([])).toEqual(["$claude ", "$claude $claude+ ", "$claude×3 "]);
});

test("a start card replaces the agents at the start of the draft and keeps the rest", () => {
  expect(withStart("$claude ", "")).toBe("$claude ");
  expect(withStart("$claude×3 ", "$codex $claude+ fix the limiter")).toBe("$claude×3 fix the limiter");
  expect(withStart("$codex ", "fix $claude's test")).toBe("$codex fix $claude's test");
});

test("each install link is a web page", () => {
  for (const a of AGENT_HELP) if (a.url !== null) expect(a.url).toMatch(/^https:\/\//);
});
