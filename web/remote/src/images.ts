import { MAX_FRAME_BYTES, MAX_IMAGE_BYTES } from "./protocol";

const ALLOWED = new Set(["image/png", "image/jpeg", "image/gif", "image/webp"]);

// Base64 inflates by 4/3 and the whole frame must stay under MAX_FRAME_BYTES.
const FRAME_HEADROOM = 64 * 1024;

export function base64Length(bytes: number): number {
  return Math.ceil(bytes / 3) * 4;
}

export type ImageCheck = { ok: true } | { ok: false; reason: string };

export function checkImage(
  file: { size: number; type: string; name: string },
  attachedBase64Bytes: number,
): ImageCheck {
  if (!ALLOWED.has(file.type)) {
    return { ok: false, reason: `${file.name}: only PNG, JPEG, GIF or WebP images can be sent.` };
  }
  if (file.size > MAX_IMAGE_BYTES) {
    return { ok: false, reason: `${file.name} is larger than 8 MB.` };
  }
  if (attachedBase64Bytes + base64Length(file.size) > MAX_FRAME_BYTES - FRAME_HEADROOM) {
    return { ok: false, reason: "Attached images are too large to send together." };
  }
  return { ok: true };
}

export function readAsBase64(file: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const r = new FileReader();
    r.onerror = () => reject(r.error ?? new Error("could not read file"));
    r.onload = () => {
      const s = r.result;
      const comma = typeof s === "string" ? s.indexOf(",") : -1;
      if (typeof s !== "string" || comma < 0) reject(new Error("unexpected file encoding"));
      else resolve(s.slice(comma + 1));
    };
    r.readAsDataURL(file);
  });
}
