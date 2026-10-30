import type { ArtifactConflict } from "../shared/artifacts";

// A stale artifact edit fails with gRPC Aborted, and the engine attaches an
// ArtifactConflict to the status (google.rpc.Status in grpc-status-details-bin).
// Main reads it by hand, since the two messages are small and the renderer
// must not have to parse prose to learn which revision won.

const CONFLICT_TYPE = "type.googleapis.com/deck.v1.ArtifactConflict";

interface Field {
  num: number;
  varint: number;
  bytes: Uint8Array | null;
}

// The fields of one protobuf message, or null when it is malformed. Only
// varint and length-delimited fields are kept; the others are skipped.
function fields(buf: Uint8Array): Field[] | null {
  const out: Field[] = [];
  let i = 0;
  const varint = (): number | null => {
    let v = 0;
    let shift = 0;
    while (i < buf.length && shift < 35) {
      const b = buf[i++] as number;
      v += (b & 0x7f) * 2 ** shift;
      if ((b & 0x80) === 0) return v;
      shift += 7;
    }
    return null;
  };
  while (i < buf.length) {
    const tag = varint();
    if (tag === null) return null;
    const wire = tag & 7;
    const num = Math.floor(tag / 8);
    if (wire === 0) {
      const v = varint();
      if (v === null) return null;
      out.push({ num, varint: v, bytes: null });
    } else if (wire === 2) {
      const len = varint();
      if (len === null || i + len > buf.length) return null;
      out.push({ num, varint: 0, bytes: buf.subarray(i, i + len) });
      i += len;
    } else if (wire === 1) i += 8;
    else if (wire === 5) i += 4;
    else return null;
  }
  return i === buf.length ? out : null;
}

const text = (f: Field | undefined): string => (f?.bytes ? new TextDecoder().decode(f.bytes) : "");

// The conflict a gRPC status carries, or null when it carries none.
export function conflictFromStatus(details: Uint8Array): ArtifactConflict | null {
  const status = fields(details);
  if (status === null) return null;
  for (const any of status) {
    if (any.num !== 3 || any.bytes === null) continue;
    const parts = fields(any.bytes);
    if (parts === null || text(parts.find((p) => p.num === 1)) !== CONFLICT_TYPE) continue;
    const value = parts.find((p) => p.num === 2)?.bytes;
    const c = value == null ? null : fields(value);
    if (c === null) return null;
    const reason = text(c.find((p) => p.num === 4));
    if (reason !== "stale_revision" && reason !== "create_key_reused") return null;
    return {
      id: text(c.find((p) => p.num === 1)),
      baseRevision: c.find((p) => p.num === 2)?.varint ?? 0,
      currentRevision: c.find((p) => p.num === 3)?.varint ?? 0,
      reason,
    };
  }
  return null;
}
