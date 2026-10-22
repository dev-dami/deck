import { decodeTheme } from "../../../../pkg/remote/web/src/protocol";
import { isHexColour } from "../../../../pkg/remote/web/src/theme";
import { mix } from "./palettes";

export interface KindTone {
  tint: string;
  mark: string;
}
export type KindTones = Record<string, KindTone>;

// The computer's theme tints each agent kind and gives it a mark, as the
// desktop shows them. The server sends it as unknown, so it is decoded here.
export function kindTones(serverTheme: unknown): KindTones {
  const theme = decodeTheme(serverTheme);
  if (theme === null) return {};
  const out: KindTones = {};
  for (const [kind, tone] of Object.entries(theme.kinds)) {
    if (isHexColour(tone.tint)) out[kind] = { tint: tone.tint.toLowerCase(), mark: tone.mark };
  }
  return out;
}

// The instance number at the end of an agent id; 1 when there is none.
export function instanceOf(agentId: string): number {
  const m = /(\d+)$/.exec(agentId);
  return m?.[1] === undefined ? 1 : Math.max(1, Number(m[1]));
}

// A colour for an agent: its kind's tint, lighter for each further instance so
// two agents of one kind differ (desktop-shell/src/renderer/agents.ts).
export function agentTint(
  tones: KindTones,
  agentId: string,
  kind: string,
  fallback: string,
): string {
  const base = tones[kind]?.tint ?? fallback;
  const step = (instanceOf(agentId) - 1) % 4;
  return step === 0 ? base : mix(base, "#ffffff", step * 0.18);
}

export function agentMark(tones: KindTones, kind: string): string {
  return tones[kind]?.mark || "●";
}
