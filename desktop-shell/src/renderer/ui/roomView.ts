// How the room shows its thread: the conversation, or the work as a graph
// (docs/design-glass.md, section 7). The diff stays in the side panel.
export type RoomView = "conversation" | "graph";

export const ROOM_VIEWS: ReadonlyArray<{ view: RoomView; label: string }> = [
  { view: "conversation", label: "Conversation" },
  { view: "graph", label: "Graph" },
];
