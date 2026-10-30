import type { ArtifactRevisionFiles, ArtifactSummary } from "../shared/artifacts";

const encoder = new TextEncoder();
const crcTable = Array.from({ length: 256 }, (_, n) => {
  let crc = n;
  for (let bit = 0; bit < 8; bit++) crc = (crc & 1) !== 0 ? 0xedb88320 ^ (crc >>> 1) : crc >>> 1;
  return crc >>> 0;
});

function crc32(bytes: Uint8Array): number {
  let crc = 0xffffffff;
  for (const byte of bytes) crc = (crcTable[(crc ^ byte) & 255] as number) ^ (crc >>> 8);
  return (crc ^ 0xffffffff) >>> 0;
}

// A bounded source archive uses ZIP's STORE method. Source is UTF-8 and at
// most 1 MiB/32 files, so ZIP64 and compression streams are unnecessary.
export function sourceZip(files: ArtifactRevisionFiles["files"]): Uint8Array<ArrayBuffer> {
  if (files.length === 0 || files.length > 32) throw new Error("Invalid artifact file collection.");
  const chunks: Uint8Array[] = [];
  const directory: Uint8Array[] = [];
  let offset = 0, directoryBytes = 0, sourceBytes = 0;
  const seen = new Set<string>();
  for (const file of files) {
    if (!/^[A-Za-z0-9_./-]{1,160}$/.test(file.path) || file.path.startsWith("/") || file.path.split("/").some((p) => p === "" || p === "." || p === "..") || seen.has(file.path)) {
      throw new Error("Invalid artifact source path.");
    }
    seen.add(file.path);
    const name = encoder.encode(file.path), data = encoder.encode(file.content);
    sourceBytes += data.length;
    if (data.length > 256 * 1024 || sourceBytes > 1024 * 1024) throw new Error("Artifact source exceeds the export limit.");
    const crc = crc32(data);
    const local = new Uint8Array(30 + name.length);
    const l = new DataView(local.buffer);
    l.setUint32(0, 0x04034b50, true); l.setUint16(4, 20, true); l.setUint16(6, 0x0800, true);
    l.setUint16(12, 0x0021, true); l.setUint32(14, crc, true);
    l.setUint32(18, data.length, true); l.setUint32(22, data.length, true); l.setUint16(26, name.length, true);
    local.set(name, 30);
    chunks.push(local, data);
    const central = new Uint8Array(46 + name.length);
    const c = new DataView(central.buffer);
    c.setUint32(0, 0x02014b50, true); c.setUint16(4, 20, true); c.setUint16(6, 20, true); c.setUint16(8, 0x0800, true);
    c.setUint16(14, 0x0021, true); c.setUint32(16, crc, true);
    c.setUint32(20, data.length, true); c.setUint32(24, data.length, true); c.setUint16(28, name.length, true);
    c.setUint32(42, offset, true); central.set(name, 46);
    directory.push(central); directoryBytes += central.length; offset += local.length + data.length;
  }
  const end = new Uint8Array(22), e = new DataView(end.buffer);
  e.setUint32(0, 0x06054b50, true); e.setUint16(8, files.length, true); e.setUint16(10, files.length, true);
  e.setUint32(12, directoryBytes, true); e.setUint32(16, offset, true);
  const zip = new Uint8Array(offset + directoryBytes + end.length);
  let at = 0;
  for (const part of [...chunks, ...directory, end]) { zip.set(part, at); at += part.length; }
  return zip;
}

export function sourceDownload(summary: ArtifactSummary, revision: ArtifactRevisionFiles): { name: string; blob: Blob } {
  if (summary.kind !== "react" && revision.files.length === 1) {
    const file = revision.files[0]!;
    return { name: file.path.split("/").at(-1) || "artifact.txt", blob: new Blob([file.content], { type: "text/plain;charset=utf-8" }) };
  }
  const title = summary.title.toLowerCase().replace(/[^a-z0-9_-]+/g, "-").replace(/^-+|-+$/g, "") || "artifact";
  return { name: `${title}-r${revision.revision}.zip`, blob: new Blob([sourceZip(revision.files)], { type: "application/zip" }) };
}
