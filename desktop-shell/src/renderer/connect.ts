import { looksLikeInvite, pairOfferFromHash, parseInvite } from "@web/auth";
import type { PairInput } from "../shared/bridge";

export type ParsedPair = { ok: true; input: PairInput } | { ok: false; message: string };

const NEEDS_LINK =
  "Paste the link or the invite line that 'deck remote pair' prints.";

// Reads what the person pasted: the invite line (deck://pair?…, which names
// the fingerprint) or the pairing URL (http(s)://host:port/#pair=CODE). An
// https URL carries no fingerprint, so it needs the one typed in the
// fingerprint field. Main checks the result again before it connects.
export function parsePairText(text: string, typedFingerprint: string): ParsedPair {
  const s = text.trim();
  const typed = typedFingerprint.trim() === "" ? null : typedFingerprint.trim();
  if (looksLikeInvite(s)) {
    const offer = parseInvite(s);
    if (offer === null || offer.host === null || offer.port === null) {
      return { ok: false, message: "That invite line is not valid. Copy the whole line." };
    }
    return {
      ok: true,
      input: {
        host: offer.host,
        port: Number(offer.port),
        tls: true,
        fingerprint: offer.fingerprint,
        code: offer.code,
      },
    };
  }
  let url: URL;
  try {
    url = new URL(s);
  } catch {
    return { ok: false, message: NEEDS_LINK };
  }
  if (url.protocol !== "http:" && url.protocol !== "https:") return { ok: false, message: NEEDS_LINK };
  const offer = pairOfferFromHash(url.hash);
  if (offer === null) return { ok: false, message: "The link has no pairing code (#pair=…)." };
  const tls = url.protocol === "https:";
  const fingerprint = offer.fingerprint ?? typed;
  if (tls && fingerprint === null) {
    return {
      ok: false,
      message: "Type the certificate fingerprint that deck remote prints, or paste the invite line.",
    };
  }
  return {
    ok: true,
    input: {
      host: url.hostname.replace(/^\[|\]$/g, ""),
      port: Number(url.port === "" ? (tls ? "443" : "80") : url.port),
      tls,
      fingerprint: tls ? fingerprint : null,
      code: offer.code,
    },
  };
}
