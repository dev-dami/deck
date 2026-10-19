import { contextBridge, ipcRenderer } from "electron";
import type { DeckBridge, HostMethod, RpcStreamEvent, SocketEvent } from "../shared/bridge";

// A sandboxed preload cannot require local files, so the bridge module is
// imported for its types only and the channel names are spelled out here;
// they must match hostChannel, SOCKET_EVENT_CHANNEL and RPC_EVENT_CHANNEL.
const invoke = (m: HostMethod, ...args: unknown[]) => ipcRenderer.invoke(`deck:${m}`, ...args);

function listen<T extends unknown[]>(channel: string, fn: (...args: T) => void): () => void {
  const listener = (_e: unknown, ...args: unknown[]) => fn(...(args as T));
  ipcRenderer.on(channel, listener);
  return () => {
    ipcRenderer.removeListener(channel, listener);
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
  recentFolders: () => invoke("recentFolders"),
  rpcCall: (id, method, request) => invoke("rpcCall", id, method, request),
  rpcSubscribe: (id, epoch, seq) => invoke("rpcSubscribe", id, epoch, seq),
  termSubscribe: (id, term) => invoke("termSubscribe", id, term),
  termUnsubscribe: (id, term) => invoke("termUnsubscribe", id, term),
  onSocket: (fn) => listen<[number, SocketEvent]>("deck:socket", fn),
  onRpc: (fn) => listen<[string, RpcStreamEvent]>("deck:rpc", fn),
};

contextBridge.exposeInMainWorld("deck", bridge);
