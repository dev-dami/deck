import { expect, test } from "vitest";
import { AGENT_HELP, startCards, withStart } from "./landing";

const prefixes = (kinds: string[]) => startCards(kinds).map((c) => c.prefix);

test("the start cards name claude and codex when both are installed", () => {
  expect(prefixes(["claude", "codex", "opencode"])).toEqual(["$claude ", "$claude $codex ", "/race "]);
  expect(startCards(["codex", "claude"])[0]?.line).toBe("Ask claude to …");
});

test("the start cards use only the kinds that are installed", () => {
  expect(prefixes(["codex", "opencode"])).toEqual(["$codex ", "$codex $opencode ", "/race "]);
  expect(prefixes(["claude", "opencode"])).toEqual(["$claude ", "$claude $opencode ", "/race "]);
  // One kind splits the work by asking it: it starts helpers itself.
  expect(prefixes(["opencode"])).toEqual(["$opencode ", "$opencode ", "/race "]);
  // Before the Deck answers, claude.
  expect(prefixes([])).toEqual(["$claude ", "$claude ", "/race "]);
  // No start uses the forms Deck no longer reads.
  expect(prefixes(["claude", "codex"]).join(" ")).not.toMatch(/[+*×]/);
});

test("a start card replaces the agents at the start of the draft and keeps the rest", () => {
  expect(withStart("$claude ", "")).toBe("$claude ");
  expect(withStart("/race ", "$codex $claude fix the limiter")).toBe("/race fix the limiter");
  expect(withStart("$codex ", "/race fix the limiter")).toBe("$codex fix the limiter");
  expect(withStart("$codex ", "fix $claude's test")).toBe("$codex fix $claude's test");
});

test("each install link is a web page", () => {
  for (const a of AGENT_HELP) if (a.url !== null) expect(a.url).toMatch(/^https:\/\//);
});
