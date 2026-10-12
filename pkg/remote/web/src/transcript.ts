import type { Message, Part } from "./protocol";

export type Chunk =
  | { k: "text"; text: string; truncated: boolean }
  | { k: "reasoning"; text: string; truncated: boolean }
  | { k: "tool"; call: Part | null; result: Part | null }
  | { k: "image"; name: string; mime: string };

// Pairs each tool_result with its tool_call so a call and its outcome render as
// one card. Results whose call is missing (older history window) stand alone.
export function groupParts(parts: Part[]): Chunk[] {
  const chunks: Chunk[] = [];
  const openCalls = new Map<string, number>();
  for (const p of parts) {
    switch (p.type) {
      case "text":
        if ((p.text ?? "") !== "") {
          chunks.push({ k: "text", text: p.text ?? "", truncated: p.text_truncated === true });
        }
        break;
      case "reasoning":
        if ((p.text ?? "") !== "") {
          chunks.push({
            k: "reasoning",
            text: p.text ?? "",
            truncated: p.text_truncated === true,
          });
        }
        break;
      case "tool_call": {
        if (p.id !== undefined) openCalls.set(p.id, chunks.length);
        chunks.push({ k: "tool", call: p, result: null });
        break;
      }
      case "tool_result": {
        const idx = p.tool_call_id === undefined ? undefined : openCalls.get(p.tool_call_id);
        const target = idx === undefined ? undefined : chunks[idx];
        if (target?.k === "tool" && target.result === null) target.result = p;
        else chunks.push({ k: "tool", call: null, result: p });
        break;
      }
      case "image":
        chunks.push({ k: "image", name: p.image?.name ?? "image", mime: p.image?.mime ?? "" });
        break;
      default:
        // Unknown part types come from a newer server; skip rather than fail.
        break;
    }
  }
  return chunks;
}

export function messageText(m: Message): string {
  return m.parts
    .filter((p) => p.type === "text")
    .map((p) => p.text ?? "")
    .join("\n");
}

export function isNearBottom(
  el: { scrollTop: number; scrollHeight: number; clientHeight: number },
  slack = 96,
): boolean {
  return el.scrollHeight - el.scrollTop - el.clientHeight <= slack;
}
