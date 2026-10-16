import { dbDelete, dbGetAll, dbPut, memoryBackend, setBackendForTests } from "./db";

// Computer record for the hosted v3 Computers list (plan §8 B4, §9 shared
// interface). The phone keeps one entry per paired computer; the relay token
// is an opaque bearer that only gates relay access, never content.
export interface ComputerRecord {
  cid: string;
  name: string;
  relayUrl: string;
  lan: string[];
  deviceId: string | null;
  // Opaque bearer; stored but never logged.
  relayToken: string | null;
  online?: boolean;
  lastSeen?: string | null;
  createdAt: string;
  // The computer's X25519 static key (base64url), pinned from the QR code.
  computerStatic?: string;
  // This phone's static key for this computer. The private half is a
  // non-extractable CryptoKey: it can be used but never read out.
  phoneKey?: PhoneKeyRecord;
}

export interface PhoneKeyRecord {
  pub: Uint8Array;
  priv: CryptoKey;
}

function isPhoneKey(v: unknown): v is PhoneKeyRecord {
  if (!isRecord(v)) return false;
  return (
    v["pub"] instanceof Uint8Array &&
    typeof CryptoKey !== "undefined" &&
    v["priv"] instanceof CryptoKey
  );
}

const STORE = "computers";

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

function decodeRow(v: unknown): ComputerRecord | null {
  if (!isRecord(v)) return null;
  const { cid, name, relayUrl, lan, deviceId, relayToken, online, lastSeen, createdAt } = v;
  const { computerStatic, phoneKey } = v;
  if (typeof cid !== "string" || cid === "" || typeof name !== "string" || name === "") return null;
  if (typeof relayUrl !== "string" || relayUrl === "") return null;
  if (!Array.isArray(lan) || !lan.every((x): x is string => typeof x === "string")) return null;
  if (typeof createdAt !== "string" || createdAt === "") return null;
  const row: ComputerRecord = {
    cid,
    name,
    relayUrl,
    lan,
    deviceId: typeof deviceId === "string" ? deviceId : null,
    relayToken: typeof relayToken === "string" ? relayToken : null,
    createdAt,
  };
  if (typeof online === "boolean") row.online = online;
  if (typeof lastSeen === "string") row.lastSeen = lastSeen;
  if (typeof computerStatic === "string") row.computerStatic = computerStatic;
  if (isPhoneKey(phoneKey)) row.phoneKey = phoneKey;
  return row;
}

function encodeRow(c: ComputerRecord): Record<string, unknown> {
  return { ...c };
}

export async function listComputers(): Promise<ComputerRecord[]> {
  const rows = await dbGetAll(STORE);
  return rows
    .map(decodeRow)
    .filter((r): r is ComputerRecord => r !== null)
    .sort((a, b) => (a.createdAt < b.createdAt ? -1 : a.createdAt > b.createdAt ? 1 : 0));
}

export async function addComputer(c: ComputerRecord): Promise<void> {
  await dbPut(STORE, c.cid, encodeRow(c));
}

export async function renameComputer(cid: string, name: string): Promise<boolean> {
  const all = await listComputers();
  const found = all.find((c) => c.cid === cid);
  if (found === undefined || name.trim() === "") return false;
  await dbPut(STORE, cid, encodeRow({ ...found, name: name.trim() }));
  return true;
}

export async function removeComputer(cid: string): Promise<void> {
  await dbDelete(STORE, cid);
}

// Revocation drops keys and tokens but keeps the row greyed, so the UI can
// show "revoked" instead of silently forgetting the computer (B6 renders it).
export async function forgetOnRevoke(cid: string): Promise<boolean> {
  const all = await listComputers();
  const found = all.find((c) => c.cid === cid);
  if (found === undefined) return false;
  // The keys go too: without a token or a device there is nothing to use them
  // for, and a re-pairing makes new ones.
  const { phoneKey: _drop, ...rest } = found;
  await dbPut(STORE, cid, encodeRow({ ...rest, deviceId: null, relayToken: null, online: false }));
  return true;
}

export { memoryBackend, setBackendForTests };
