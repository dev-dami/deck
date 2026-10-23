import type { PhoneDevice, PhoneState } from "../shared/bridge";

// A pairing code the page is showing: the link, its QR and when it lapses.
export interface ShownCode {
  pairUrl: string;
  qr: string[];
  expiresAt: number;
  // How many phones were paired when it was made; a new phone means the
  // one-use code is spent.
  devices: number;
}

export function shownCode(p: PhoneState): ShownCode | null {
  if (p.pairUrl === null || p.qr === null || p.expiresAt === null) return null;
  const expiresAt = Date.parse(p.expiresAt);
  if (Number.isNaN(expiresAt)) return null;
  return { pairUrl: p.pairUrl, qr: p.qr, expiresAt, devices: p.devices.length };
}

// Whether a code can still be scanned: not expired, and no phone has paired
// since it was made.
export function codeUsable(code: ShownCode, devices: PhoneDevice[], now: number): boolean {
  return now < code.expiresAt && devices.length <= code.devices;
}

// One SVG path for the dark modules of a QR (rows of "1" and "0"): each run
// of dark modules in a row is a rectangle, so a code draws as a few hundred
// segments, not thousands of elements.
export function qrPath(rows: string[]): string {
  const out: string[] = [];
  rows.forEach((row, y) => {
    let x = 0;
    while (x < row.length) {
      if (row[x] !== "1") {
        x++;
        continue;
      }
      let end = x;
      while (end < row.length && row[end] === "1") end++;
      out.push(`M${x} ${y}h${end - x}v1h-${end - x}z`);
      x = end;
    }
  });
  return out.join("");
}
