import { contextBridge, ipcRenderer, webUtils } from "electron";
import type { DeckBridge, HostMethod, LinkEvent, MenuCommand, RpcStreamEvent, SocketEvent, UpdateEvent } from "../shared/bridge";

// A sandboxed preload cannot require local files, so the bridge module is
// imported for its types only and the channel names are spelled out here;
// they must match hostChannel and the *_EVENT_CHANNEL names.
const invoke = (m: HostMethod, ...args: unknown[]) => ipcRenderer.invoke(`deck:${m}`, ...args);

function listen<T extends unknown[]>(channel: string, fn: (...args: T) => void): () => void {
  const listener = (_e: unknown, ...args: unknown[]) => fn(...(args as T));
  ipcRenderer.on(channel, listener);
  return () => {
    ipcRenderer.removeListener(channel, listener);
  };
}

// A link can arrive before React subscribes (the app was opened by it), and
// a downloaded update before the project view mounts. Links wait for the
// first listener; the last update is handed to each new one.
function held<T>(channel: string, replay: (ev: T) => boolean): (fn: (ev: T) => void) => () => void {
  const fns = new Set<(ev: T) => void>();
  let waiting: T[] = [];
  let last: T | null = null;
  ipcRenderer.on(channel, (_e, ev: T) => {
    if (replay(ev)) last = ev;
    else if (fns.size === 0) waiting.push(ev);
    for (const fn of fns) fn(ev);
  });
  return (fn) => {
    fns.add(fn);
    const queued = waiting;
    waiting = [];
    for (const ev of queued) fn(ev);
    if (last !== null) fn(last);
    return () => {
      fns.delete(fn);
    };
  };
}

const bridge: DeckBridge = {
  listServers: () => invoke("listServers"),
  pair: (input) => invoke("pair", input),
  forgetServer: (id) => invoke("forgetServer", id),
  findDeck: () => invoke("findDeck"),
  pickFolder: () => invoke("pickFolder"),
  socketOpen: (serverId) => invoke("socketOpen", serverId),
  socketSend: (id, data) => invoke("socketSend", id, data),
  socketClose: (id, code, reason) => invoke("socketClose", id, code, reason),
  openFolder: (dir) => invoke("openFolder", dir),
  closeFolder: (id) => invoke("closeFolder", id),
  restartFolder: (id) => invoke("restartFolder", id),
  recentFolders: () => invoke("recentFolders"),
  rpcCall: (id, method, request) => invoke("rpcCall", id, method, request),
  rpcSubscribe: (id, epoch, seq) => invoke("rpcSubscribe", id, epoch, seq),
  termSubscribe: (id, term) => invoke("termSubscribe", id, term),
  termUnsubscribe: (id, term) => invoke("termUnsubscribe", id, term),
  phoneStatus: () => invoke("phoneStatus"),
  phoneLink: (folder, relayDir) => invoke("phoneLink", folder, relayDir),
  phoneNewCode: () => invoke("phoneNewCode"),
  phoneRevoke: (deviceId) => invoke("phoneRevoke", deviceId),
  phoneStop: () => invoke("phoneStop"),
  appInfo: () => invoke("appInfo"),
  openLogs: () => invoke("openLogs"),
  logFromRenderer: (level, message) => invoke("logFromRenderer", level, message),
  installUpdate: () => invoke("installUpdate"),
  threadMenu: (thread) => invoke("threadMenu", thread),
  onSocket: (fn) => listen<[number, SocketEvent]>("deck:socket", fn),
  onRpc: (fn) => listen<[string, RpcStreamEvent]>("deck:rpc", fn),
  onMenu: (fn) => listen<[MenuCommand]>("deck:menu", fn),
  onLink: held<LinkEvent>("deck:link", () => false),
  onUpdate: held<UpdateEvent>("deck:update", (ev) => ev.type === "ready"),
  folderPath: (file) => webUtils.getPathForFile(file),
};

contextBridge.exposeInMainWorld("deck", bridge);
