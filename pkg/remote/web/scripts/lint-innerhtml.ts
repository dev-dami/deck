import { join } from "node:path";
import { findViolations } from "./innerhtml";

const src = join(import.meta.dir, "..", "src");
const violations = findViolations(src);
for (const v of violations) {
  console.error(`${v.file}:${v.line}: banned HTML-injection API: ${v.text}`);
}
if (violations.length > 0) process.exit(1);
console.log("no innerHTML-style APIs in src/");
