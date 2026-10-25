import type { Store } from "@web/controller";
import type { ChangedFile, Decision, Image, Mode, ModelChoice, Session } from "@web/protocol";
import type { AppState } from "@web/state";
import type { ProjectApi } from "./project";

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
  createSession(title?: string): Promise<Session | null>;
  renameSession(id: string, title: string): Promise<void>;
  deleteSession(id: string): Promise<boolean>;
  send(sessionId: string, text: string, images: Image[]): Promise<boolean>;
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
}
