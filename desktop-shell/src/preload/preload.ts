import { contextBridge, ipcRenderer } from "electron";
import type { DeckBridge, HostMethod, SocketEvent } from "../shared/bridge";

// A sandboxed preload cannot require local files, so the bridge module is
// imported for its types only and the two channel names are spelled out
// here; they must match hostChannel and SOCKET_EVENT_CHANNEL.
const invoke = (m: HostMethod, ...args: unknown[]) => ipcRenderer.invoke(`deck:${m}`, ...args);

const bridge: DeckBridge = {
  listServers: () => invoke("listServers"),
  pair: (input) => invoke("pair", input),
  forgetServer: (id) => invoke("forgetServer", id),
  findDeck: () => invoke("findDeck"),
  startLocal: (dir) => invoke("startLocal", dir),
  pickFolder: () => invoke("pickFolder"),
  socketOpen: (serverId) => invoke("socketOpen", serverId),
  socketSend: (id, data) => invoke("socketSend", id, data),
  socketClose: (id, code, reason) => invoke("socketClose", id, code, reason),
  onSocket: (fn) => {
    const listener = (_e: unknown, id: number, ev: SocketEvent) => fn(id, ev);
    ipcRenderer.on("deck:socket", listener);
    return () => {
      ipcRenderer.removeListener("deck:socket", listener);
    };
  },
};

contextBridge.exposeInMainWorld("deck", bridge);
