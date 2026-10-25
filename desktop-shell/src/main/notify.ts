import { app, type BrowserWindow, type NativeImage, nativeImage, Notification } from "electron";
import type { NotifyInput } from "../shared/bridge";

// Native notifications and the dock or taskbar badge. Main shows them
// itself, so the window's permission handlers keep denying everything.

const AMBER: [number, number, number] = [0xf0, 0xb4, 0x3c];

// A filled dot with a soft edge, as premultiplied BGRA pixels.
export function dotBitmap(size: number, [r, g, b]: [number, number, number]): Buffer {
  const out = Buffer.alloc(size * size * 4);
  const c = (size - 1) / 2;
  for (let y = 0; y < size; y++) {
    for (let x = 0; x < size; x++) {
      const a = Math.max(0, Math.min(1, size / 2 - Math.hypot(x - c, y - c)));
      const i = (y * size + x) * 4;
      out[i] = Math.round(b * a);
      out[i + 1] = Math.round(g * a);
      out[i + 2] = Math.round(r * a);
      out[i + 3] = Math.round(255 * a);
    }
  }
  return out;
}

export class Notifier {
  // Shown notifications by tag: a reference keeps the click handler alive,
  // and a new one for the same room replaces the old.
  private readonly shown = new Map<string, Notification>();
  private dot: NativeImage | null = null;

  constructor(
    private readonly win: () => BrowserWindow | null,
    private readonly onClick: (tag: string) => void,
  ) {}

  notify(n: NotifyInput): void {
    if (!Notification.isSupported()) return;
    this.shown.get(n.tag)?.close();
    const note = new Notification({ title: n.title, body: n.body, silent: n.silent });
    const forget = () => {
      if (this.shown.get(n.tag) === note) this.shown.delete(n.tag);
    };
    note.on("click", () => {
      forget();
      this.onClick(n.tag);
    });
    note.on("close", forget);
    this.shown.set(n.tag, note);
    note.show();
  }

  // macOS and Linux show the count on the app icon; Windows has no count,
  // so the taskbar button gets an amber dot.
  badge(count: number): void {
    if (process.platform !== "win32") {
      app.setBadgeCount(count);
      return;
    }
    const w = this.win();
    if (w === null) return;
    this.dot ??= nativeImage.createFromBitmap(dotBitmap(16, AMBER), { width: 16, height: 16 });
    w.setOverlayIcon(count > 0 ? this.dot : null, count > 0 ? `${count} waiting for you` : "");
  }

  focused(): boolean {
    return this.win()?.isFocused() ?? false;
  }
}
