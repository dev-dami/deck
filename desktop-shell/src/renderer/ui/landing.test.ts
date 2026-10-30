import { expect, test } from "vitest";
import { AGENT_HELP } from "./landing";

test("each install link is a web page", () => {
  for (const a of AGENT_HELP) if (a.url !== null) expect(a.url).toMatch(/^https:\/\//);
});
