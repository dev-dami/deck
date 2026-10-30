import { useState } from "preact/hooks";
import { canSaveEdit } from "../composer";
import type { AgentLine, Message, Part } from "../protocol";
import { briefOf } from "../subthreads";
import { kindColour, kindMark } from "../theme";
import { type Chunk, groupParts, messageText } from "../transcript";
import { DiffView } from "./DiffView";
import { useAppState, useController } from "./hooks";
import { Markdown } from "./Markdown";

function ToolCard({ call, result }: { call: Part | null; result: Part | null }) {
  const name = call?.name ?? result?.name ?? "tool";
  const summary = call?.summary ?? "";
  const running = result === null;
  const failed = result?.is_error === true;
  const status = running ? "running" : failed ? "failed" : "done";
  const output = result?.output ?? "";
  const diff = result?.diff ?? call?.diff ?? "";
  const diffTruncated = result?.diff_truncated === true || call?.diff_truncated === true;
  // Tool rows stay collapsed unless they failed; a tap opens them.
  return (
    <details class={`tool tool-${status}`} open={failed}>
      <summary>
        <span class="tool-name">{name}</span>
        <span class="tool-summary ellipsis">{summary}</span>
        <span class={`tool-status tool-status-${status}`}>{status}</span>
      </summary>
      {summary !== "" ? <p class="tool-full">{summary}</p> : null}
      {diff !== "" ? <DiffView diff={diff} truncated={diffTruncated} /> : null}
      {output !== "" ? <pre class="tool-output">{output}</pre> : null}
      {result?.output_truncated === true ? (
        <p class="muted small-text">Output truncated by Deck.</p>
      ) : null}
    </details>
  );
}

function Truncated({ on }: { on: boolean }) {
  return on ? <p class="muted small-text">Text truncated by Deck.</p> : null;
}

function ChunkView({ chunk, user }: { chunk: Chunk; user: boolean }) {
  switch (chunk.k) {
    case "text":
      return (
        <>
          {user ? <p class="user-text">{chunk.text}</p> : <Markdown text={chunk.text} />}
          <Truncated on={chunk.truncated} />
        </>
      );
    case "reasoning":
      return (
        <details class="reasoning">
          <summary>Thinking</summary>
          <p class="user-text">{chunk.text}</p>
          <Truncated on={chunk.truncated} />
        </details>
      );
    case "tool":
      return <ToolCard call={chunk.call} result={chunk.result} />;
    case "image":
      // v1 has no way to fetch an image's bytes back (§3.7): show its name.
      return (
        <p class="image-chip">
          <span aria-hidden="true">&#9635; </span>
          {chunk.name}
          {chunk.mime !== "" ? <span class="muted"> ({chunk.mime})</span> : null}
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

function AgentName({
  agentId,
  roster,
  inline = false,
}: {
  agentId: string;
  roster: AgentLine[];
  inline?: boolean;
}) {
  const { theme } = useAppState();
  const line = roster.find((a) => a.id === agentId);
  const kind = line?.kind ?? "";
  const mark = kindMark(theme, kind);
  const Tag = inline ? "span" : "p";
  return (
    <Tag class="agent-name" style={{ color: kindColour(kind) }}>
      {mark !== "" ? (
        <span class="agent-mark" aria-hidden="true">
          {mark}{" "}
        </span>
      ) : null}
      {line?.name ?? agentId}
      {line?.role !== undefined && line.role !== "" ? (
        <span class="agent-role"> {line.role}</span>
      ) : null}
    </Tag>
  );
}

interface MessageViewProps {
  message: Message;
  roster: AgentLine[];
  online: boolean;
}

export function MessageView({ message, roster, online }: MessageViewProps) {
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
  // A parent agent's brief to this sub-thread: the agent's words, not the
  // person's.
  const brief = briefOf(message);
  if (brief !== null) {
    return (
      <article class="msg msg-assistant msg-brief">
        <p class="brief-from">
          From <AgentName agentId={brief.from} roster={roster} inline />
          {brief.thread !== "" ? <span class="brief-thread"> · {brief.thread}</span> : null}
        </p>
        <Markdown text={brief.body} />
      </article>
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
      {!user && message.agent !== undefined ? (
        <AgentName agentId={message.agent} roster={roster} />
      ) : null}
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
          disabled={!online}
          onClick={() => setEditing(true)}
        >
          <span aria-hidden="true">&#9998;</span>
        </button>
      ) : null}
    </article>
  );
}
