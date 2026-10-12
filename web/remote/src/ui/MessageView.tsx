import { useState } from "preact/hooks";
import { canSaveEdit } from "../composer";
import type { Message, Part } from "../protocol";
import { type Chunk, groupParts, messageText } from "../transcript";
import { DiffView } from "./DiffView";
import { useController } from "./hooks";
import { Markdown } from "./Markdown";

function ToolCard({ call, result }: { call: Part | null; result: Part | null }) {
  const name = call?.name ?? result?.name ?? "tool";
  const summary = call?.summary ?? "";
  const running = result === null;
  const failed = result?.is_error === true;
  const status = running ? "running" : failed ? "failed" : "done";
  const output = result?.output ?? "";
  const diff = result?.diff ?? call?.diff ?? "";
  return (
    <details class={`tool tool-${status}`} open={failed || diff !== ""}>
      <summary>
        <span class="tool-name">{name}</span>
        <span class="tool-summary ellipsis">{summary}</span>
        <span class={`tool-status tool-status-${status}`}>{status}</span>
      </summary>
      {summary !== "" ? <p class="tool-full">{summary}</p> : null}
      {diff !== "" ? <DiffView diff={diff} /> : null}
      {output !== "" ? <pre class="tool-output">{output}</pre> : null}
      {result?.truncated === true ? (
        <p class="muted small-text">Output truncated by Deck.</p>
      ) : null}
    </details>
  );
}

function ChunkView({ chunk, user }: { chunk: Chunk; user: boolean }) {
  switch (chunk.k) {
    case "text":
      return user ? <p class="user-text">{chunk.text}</p> : <Markdown text={chunk.text} />;
    case "reasoning":
      return (
        <details class="reasoning">
          <summary>Thinking</summary>
          <p class="user-text">{chunk.text}</p>
        </details>
      );
    case "tool":
      return <ToolCard call={chunk.call} result={chunk.result} />;
    case "image":
      return (
        <p class="image-chip">
          Image: {chunk.name}
          {chunk.mime !== "" ? ` (${chunk.mime})` : ""}
        </p>
      );
  }
}

function EditBox({
  message,
  online,
  onDone,
}: {
  message: Message;
  online: boolean;
  onDone: () => void;
}) {
  const controller = useController();
  const original = messageText(message);
  const [draft, setDraft] = useState(original);
  const [saving, setSaving] = useState(false);

  const save = async () => {
    if (!canSaveEdit(original, draft, online, saving)) return;
    setSaving(true);
    const ok = await controller.editMessage(message.session_id, message.id, draft.trim());
    setSaving(false);
    if (ok) onDone();
  };

  return (
    <form
      class="edit-box"
      onSubmit={(e) => {
        e.preventDefault();
        void save();
      }}
    >
      <textarea
        rows={3}
        value={draft}
        aria-label="Edit message"
        onInput={(e) => setDraft(e.currentTarget.value)}
      />
      <div class="btn-row">
        <button
          type="submit"
          class="btn small primary"
          disabled={!canSaveEdit(original, draft, online, saving)}
        >
          {saving ? "Saving..." : "Save"}
        </button>
        <button type="button" class="btn small" disabled={saving} onClick={onDone}>
          Cancel
        </button>
      </div>
    </form>
  );
}

export function MessageView({ message, online }: { message: Message; online: boolean }) {
  const [editing, setEditing] = useState(false);
  const chunks = groupParts(message.parts);
  if (message.role === "system") {
    return (
      <div class="msg msg-system">
        {chunks.map((c, i) => (
          <ChunkView key={i} chunk={c} user={true} />
        ))}
      </div>
    );
  }
  const user = message.role === "user";
  const canEdit = user && message.editable === true;
  if (editing && canEdit) {
    return (
      <article class="msg msg-user msg-editing">
        <EditBox message={message} online={online} onDone={() => setEditing(false)} />
      </article>
    );
  }
  return (
    <article class={`msg ${user ? "msg-user" : "msg-assistant"}`}>
      {!user && message.agent !== undefined ? <p class="agent-name">{message.agent}</p> : null}
      {chunks.map((c, i) => (
        <ChunkView key={i} chunk={c} user={user} />
      ))}
      {message.edited === true ? <span class="msg-edited">edited</span> : null}
      {!user && !message.finished ? (
        <span class="typing" role="status" aria-label="Working" />
      ) : null}
      {canEdit ? (
        <button
          type="button"
          class="msg-edit"
          aria-label="Edit message"
          title="Edit message"
          onClick={() => setEditing(true)}
        >
          <span aria-hidden="true">&#9998;</span>
        </button>
      ) : null}
    </article>
  );
}
