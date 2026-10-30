import { type Image, MAX_FRAME_BYTES } from "./protocol";

const ALLOWED = new Set(["image/png", "image/jpeg", "image/gif", "image/webp"]);

export const MAX_IMAGE_BYTES = 8 << 20;

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

// The wire name is a display name, never a path (§3.4).
export function displayName(name: string): string {
  const base = name.split(/[\\/]/).pop() ?? "";
  return base === "" ? "image" : base.slice(0, 120);
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

// A blob's bytes as base64, without FileReader: a chunk of a video upload.
export async function blobBase64(b: Blob): Promise<string> {
  const bytes = new Uint8Array(await b.arrayBuffer());
  let s = "";
  for (let i = 0; i < bytes.length; i += 0x8000) {
    s += String.fromCharCode(...bytes.subarray(i, i + 0x8000));
  }
  return btoa(s);
}

// ---- labels -------------------------------------------------------------------

// The longest label kept (the engine cuts longer ones too).
export const MAX_LABEL = 60;

// A label as it is sent: one line, at most MAX_LABEL characters.
export function cleanLabel(s: string): string {
  return [
    ...s
      .split(/\s+/)
      .filter((w) => w !== "")
      .join(" "),
  ]
    .slice(0, MAX_LABEL)
    .join("")
    .trim();
}

// Where an attachment came from: a paste (a screenshot) or a file the
// person picked or dropped.
export type Source = "paste" | "file";

// The label an attachment starts with: a pasted image is "Screenshot 1", a
// file its name without the extension, else "Image 1" or "Video 1"; the
// number is the first one the attached labels do not use yet.
export function defaultLabel(
  kind: "image" | "video",
  source: Source,
  fileName: string,
  taken: readonly string[],
): string {
  if (source === "file") {
    const base = displayName(fileName).replace(/\.[^.]*$/, "");
    const named = cleanLabel(base);
    // Names a browser makes up for a paste or a capture say nothing.
    if (named !== "" && !/^(image|video|blob|pasted)$/i.test(named)) return named;
  }
  const stem = kind === "video" ? "Video" : source === "paste" ? "Screenshot" : "Image";
  let n = 1;
  while (taken.includes(`${stem} ${n}`)) n++;
  return `${stem} ${n}`;
}

// ---- video --------------------------------------------------------------------

export const VIDEO_TYPES = new Set(["video/mp4", "video/webm", "video/quicktime"]);

// The largest video a client sends: from the desktop to a Deck on the same
// machine, and from the phone (or a paired desktop) in upload chunks.
export const MAX_VIDEO_BYTES_LOCAL = 50 << 20;
export const MAX_VIDEO_BYTES_REMOTE = 25 << 20;

// The bytes of a video sent per media.upload call (the server allows 4 MB).
export const UPLOAD_CHUNK_BYTES = 3 << 20;

export function isVideo(type: string): boolean {
  return type.startsWith("video/");
}

export function checkVideo(
  file: { size: number; type: string; name: string },
  max: number,
): ImageCheck {
  if (!VIDEO_TYPES.has(file.type)) {
    return { ok: false, reason: `${file.name}: only MP4, WebM or QuickTime videos can be sent.` };
  }
  if (file.size > max) {
    return { ok: false, reason: `${file.name} is larger than ${max >> 20} MB.` };
  }
  return { ok: true };
}

// Whether a video can go to the room's lead now, and if not why, and
// whether Deck can send it as frames instead.
export interface VideoGate {
  // The Deck takes video at all (feature "video").
  available: boolean;
  ok: boolean;
  // Why it cannot: "claude-opus-5-5 can't watch video".
  reason: string;
  // Deck can send it as still frames (its machine has ffmpeg).
  frames: boolean;
  // Why it cannot send frames either; "" when it can.
  framesReason: string;
}

export function videoGate(
  features: readonly string[],
  lead: { video?: boolean; slug?: string; model?: string; kind?: string } | null | undefined,
): VideoGate {
  const frames = features.includes("video_frames");
  const framesReason = frames ? "" : "Sending frames needs ffmpeg on the computer running Deck.";
  if (!features.includes("video")) {
    return {
      available: false,
      ok: false,
      reason: "This Deck takes no video.",
      frames: false,
      framesReason: "This Deck takes no video.",
    };
  }
  if (lead?.video === true) return { available: true, ok: true, reason: "", frames, framesReason };
  const who = lead?.slug || lead?.model || lead?.kind || "This agent";
  return { available: true, ok: false, reason: `${who} can't watch video`, frames, framesReason };
}

// An attachment as a composer holds it until it is sent: the wire Image,
// and for a video the file itself, uploaded or read on send. path is the
// file's own path where the client can tell it (the desktop).
export type Outgoing = Image & { file?: Blob; path?: string };

// A video attached while the lead cannot watch one goes only as frames.
export function blockedVideo(atts: readonly Outgoing[], gate: VideoGate): boolean {
  return atts.some((a) => isVideo(a.mime) && !gate.ok && a.frames !== true);
}
