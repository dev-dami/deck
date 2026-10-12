// JSON with sorted keys, the form the wire fixtures are written in and the
// form both sides compare after a round trip (§3.7).

function sortKeys(v: unknown): unknown {
  if (Array.isArray(v)) return v.map(sortKeys);
  if (typeof v === "object" && v !== null) {
    const out: Record<string, unknown> = {};
    for (const k of Object.keys(v).sort()) {
      const x = (v as Record<string, unknown>)[k];
      if (x !== undefined) out[k] = sortKeys(x);
    }
    return out;
  }
  return v;
}

// Compact: no whitespace.
export function canonicalJson(v: unknown): string {
  return JSON.stringify(sortKeys(v));
}

// Pretty: 2-space indent and a trailing newline, as the fixture files are.
export function prettyJson(v: unknown): string {
  return `${JSON.stringify(sortKeys(v), null, 2)}\n`;
}
