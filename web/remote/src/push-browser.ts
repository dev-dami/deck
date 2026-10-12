import { isIos, type PushEnv, type PushPermission } from "./push";
import type { BrowserSub, PushBrowser } from "./push-client";

function wrap(sub: PushSubscription): BrowserSub {
  return {
    endpoint: sub.endpoint,
    json: sub.toJSON(),
    applicationServerKey: sub.options.applicationServerKey,
    unsubscribe: () => sub.unsubscribe(),
  };
}

function isStandalone(): boolean {
  // iOS Safari exposes its own non-standard flag instead of the display-mode query.
  return (
    Reflect.get(navigator, "standalone") === true ||
    window.matchMedia("(display-mode: standalone)").matches
  );
}

function permissionOf(p: NotificationPermission): PushPermission {
  return p;
}

// `ready` never settles when registration failed, which would leave the UI busy forever.
async function registration(): Promise<ServiceWorkerRegistration> {
  let timer: ReturnType<typeof setTimeout> | undefined;
  const timeout = new Promise<never>((_, reject) => {
    timer = setTimeout(
      () => reject(new Error("The background worker is not ready. Reload and retry.")),
      8000,
    );
  });
  try {
    return await Promise.race([navigator.serviceWorker.ready, timeout]);
  } finally {
    clearTimeout(timer);
  }
}

// The real browser behind PushBrowser. Kept thin: all decisions live in push.ts.
export class BrowserPush implements PushBrowser {
  env(): PushEnv {
    return {
      secure: window.isSecureContext,
      serviceWorker: "serviceWorker" in navigator,
      pushManager: "PushManager" in window,
      notification: "Notification" in window,
      ios: isIos(navigator.userAgent, navigator.maxTouchPoints),
      standalone: isStandalone(),
    };
  }

  permission(): PushPermission {
    return "Notification" in window ? permissionOf(Notification.permission) : "denied";
  }

  async requestPermission(): Promise<PushPermission> {
    return permissionOf(await Notification.requestPermission());
  }

  async current(): Promise<BrowserSub | null> {
    const reg = await registration();
    const sub = await reg.pushManager.getSubscription();
    return sub === null ? null : wrap(sub);
  }

  async subscribe(key: Uint8Array<ArrayBuffer>): Promise<BrowserSub> {
    const reg = await registration();
    return wrap(
      await reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: key }),
    );
  }
}
