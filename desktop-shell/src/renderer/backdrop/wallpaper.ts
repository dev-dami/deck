import { useSyncExternalStore } from "react";

// The person's own wallpaper: its bytes in this renderer's IndexedDB, and
// "custom" in prefs (prefs.ts, wallpaper) while it is the picture. Like the
// prefs, it never leaves this window.

const DB = "deck.desktop";
const STORE = "wallpaper";
const KEY = "custom";
// A picture larger than this is refused: it is a backdrop, not an archive.
export const MAX_BYTES = 30 * 1024 * 1024;

function open(): Promise<IDBDatabase> {
  return new Promise((resolve, reject) => {
    const req = indexedDB.open(DB, 1);
    req.onupgradeneeded = () => req.result.createObjectStore(STORE);
    req.onsuccess = () => resolve(req.result);
    req.onerror = () => reject(req.error ?? new Error("IndexedDB did not open"));
  });
}

async function run<T>(mode: IDBTransactionMode, op: (s: IDBObjectStore) => IDBRequest<T>): Promise<T> {
  const db = await open();
  try {
    return await new Promise<T>((resolve, reject) => {
      const req = op(db.transaction(STORE, mode).objectStore(STORE));
      req.onsuccess = () => resolve(req.result);
      req.onerror = () => reject(req.error ?? new Error("IndexedDB request failed"));
    });
  } finally {
    db.close();
  }
}

// Each save or clear moves the version, so the backdrop reads the new
// bytes even when "custom" was already the choice.
let version = 0;
const listeners = new Set<() => void>();
function bump(): void {
  version++;
  for (const l of listeners) l();
}

export function useWallpaperVersion(): number {
  return useSyncExternalStore(
    (l) => {
      listeners.add(l);
      return () => listeners.delete(l);
    },
    () => version,
  );
}

// Keeps a picture the person chose. The error text, or null.
export async function saveWallpaper(file: Blob): Promise<string | null> {
  if (!file.type.startsWith("image/")) return "That file is not a picture.";
  if (file.size > MAX_BYTES) return "That picture is larger than 30 MB.";
  try {
    // A picture the browser cannot decode is refused before it is kept.
    (await createImageBitmap(file)).close();
  } catch {
    return "This picture could not be read.";
  }
  try {
    await run("readwrite", (s) => s.put(file, KEY));
  } catch {
    return "The picture could not be saved.";
  }
  bump();
  return null;
}

export async function loadWallpaper(): Promise<Blob | null> {
  try {
    const v = await run<unknown>("readonly", (s) => s.get(KEY));
    return v instanceof Blob ? v : null;
  } catch {
    return null;
  }
}

export async function clearWallpaper(): Promise<void> {
  try {
    await run("readwrite", (s) => s.delete(KEY));
  } catch {
    // Nothing was kept, or the storage is gone: either way it is clear.
  }
  bump();
}
