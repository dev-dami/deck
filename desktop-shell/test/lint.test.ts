import { join } from "node:path";
import { expect, test } from "vitest";
import { findViolations } from "../../pkg/remote/web/scripts/innerhtml";

// The phone client's rule, applied here too: no API that turns a string into
// markup, so text from Deck is always rendered as text.
test("no innerHTML-style API in the app sources", () => {
  expect(findViolations(join(__dirname, "..", "src"))).toEqual([]);
});
