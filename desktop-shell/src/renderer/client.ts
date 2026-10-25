import type { Store } from "@web/controller";
import type { ChangedFile, Decision, Image, Mode, ModelChoice, Session } from "@web/protocol";
import type { AppState } from "@web/state";
import type { ProjectApi } from "./project";
import type { RoomMetaStore } from "./roomMeta";
import type { FileList, ImageBytes, SearchHit } from "./rpcMap";

// What the window needs from a Deck, whatever the transport. The phone
// client's Controller (protocol v2 through main's socket) has this shape;
// RpcDeckClient (deck serve --rpc through main's gRPC client) implements it
// too. Both keep the same AppState, so the UI renders one state.
export interface DeckClient {
  readonly store: Store;
  readonly state: AppState;
  // Git, usage and terminals: a local Deck only.
  readonly project?: ProjectApi;
  openSession(id: string): Promise<void>;
  closeSession(): void;
  loadOlder(id: string): Promise<boolean>;
  // isolate starts the thread on a worktree and branch of its own (a local
  // Deck that serves IsolateSession).
  createSession(title?: string, isolate?: boolean): Promise<Session | null>;
  renameSession(id: string, title: string): Promise<void>;
  deleteSession(id: string): Promise<boolean>;
  send(sessionId: string, text: string, images: Image[]): Promise<boolean>;
  // Replaces the person's message and runs the thread again from it.
  editMessage(sessionId: string, messageId: string, text: string): Promise<boolean>;
  // Asks the agents to go on from where they stopped.
  continueRun(sessionId: string): Promise<void>;
  cancel(sessionId: string): Promise<void>;
  interrupt(sessionId: string, agentId: string): Promise<void>;
  steer(sessionId: string, itemId: string): Promise<void>;
  cancelQueued(sessionId: string, itemId: string): Promise<void>;
  answerApproval(sessionId: string, id: string, decision: Decision): Promise<void>;
  setMode(sessionId: string, mode: Mode): Promise<boolean>;
  listModels(sessionId: string, kind: string): Promise<ModelChoice[] | null>;
  setModel(sessionId: string, kind: string, model: string, effort?: string): Promise<void>;
  changes(sessionId: string): Promise<ChangedFile[] | null>;
  // Stops the client and resets its state to "unpaired".
  forget(message?: string | null): void;
  // A local Deck only: starts its stopped engine again and resyncs.
  restart?(): Promise<void>;

  // A local Deck only: what it says of each room beyond the shared Session.
  readonly rooms?: RoomMetaStore;
  // The optional deck.v1 methods the Deck serves (Info.capabilities). Each
  // call below is a no-op that answers null or false, without a toast, when
  // its method is not listed, so the shell works against an older engine.
  capabilities?(): readonly string[];
  isolateSession?(sessionId: string): Promise<boolean>;
  // Writes what the isolated thread changed into the shared tree; the files.
  applyIsolated?(sessionId: string): Promise<string[] | null>;
  // Takes the shared tree back to before the last apply; the files.
  undoApply?(sessionId: string): Promise<string[] | null>;
  // Moves the thread back to the shared tree and discards its worktree.
  unisolate?(sessionId: string): Promise<boolean>;
  // Puts paths back as they were before the first message at or after since
  // (Unix ms; 0 or none: the checkpoint changes() measures from).
  revertFiles?(sessionId: string, paths: string[], since?: number): Promise<string[] | null>;
  undoRevert?(sessionId: string): Promise<boolean>;
  archiveSession?(sessionId: string): Promise<boolean>;
  unarchiveSession?(sessionId: string): Promise<boolean>;
  // count agents of kind do brief; with judge, one more ranks them. The race's task id.
  startRace?(sessionId: string, brief: string, kind: string, count: number, judge?: boolean): Promise<string | null>;
  listFiles?(sessionId: string, query: string, limit?: number): Promise<FileList | null>;
  // The bytes of a message's index'th image part. Quiet: no toast.
  readImage?(sessionId: string, messageId: string, index: number): Promise<ImageBytes | null>;
  // Messages of every room that is not archived that hold query, newest
  // first. Quiet: no toast.
  searchMessages?(query: string, limit?: number): Promise<SearchHit[] | null>;
}

// Whether the Deck serves the optional method (Info.capabilities).
export const serves = (c: Pick<DeckClient, "capabilities">, method: string): boolean => c.capabilities?.().includes(method) === true;
