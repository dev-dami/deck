import { useState } from "preact/hooks";
import { canSaveEdit } from "../composer";
import { foldsBody, type Handoff, handoffOf } from "../handoff";
import type { AgentLine, Message, Part, Theme } from "../protocol";
import { formatRoute } from "../route";
import { briefOf } from "../subthreads";
import { kindColour, kindMark } from "../theme";
import { type Chunk, groupParts, messageText } from "../transcript";
import { DiffView } from "./DiffView";
import { useAppState, useController } from "./hooks";
import { Markdown } from "./Markdown";

// An agent's kind by its name: the roster's, else the kind its name starts
// with ("codex-2" is a codex).
function kindOf(name: string, roster: AgentLine[]): string {
  const line = roster.find((a) => a.name === name || a.id === name);
  return line?.kind ?? name.replace(/[-/].*$/, "");
}

// A call that starts a subagent: a CLI's own (Claude Code's Task or Agent)
// or Deck's agent.spawn. It is drawn as the desktop's subagent line.
function startsSubagent(call: Part | null, name: string): boolean {
  if (name === "Task" || name === "Agent") return true;
  return name === "deck" && (call?.summary ?? "").startsWith("agent.spawn");
}

function ToolCard({ call, result, hue }: { call: Part | null; result: Part | null; hue: string }) {
  const name = call?.name ?? result?.name ?? "tool";
  const summary = call?.summary ?? "";
  const running = result === null;
  const failed = result?.is_error === true;
  const status = running ? "running" : failed ? "failed" : "done";
  const output = result?.output ?? "";
  const diff = result?.diff ?? call?.diff ?? "";
  const diffTruncated = result?.diff_truncated === true || call?.diff_truncated === true;
  const sub = startsSubagent(call, name);
  // Tool rows stay collapsed unless they failed; a tap opens them.
  return (
    <details
      class={`tool tool-${status}${sub ? " tool-sub" : ""}`}
      open={failed}
      style={sub ? { "--hue": hue } : undefined}
    >
      <summary>
        {sub ? (
          <span class="tool-glyph" aria-hidden="true">
            ↳
          </span>
        ) : (
          <span class="tool-chevron" aria-hidden="true" />
        )}
        <span class="tool-name">{name}</span>
        <span class="tool-summary ellipsis">{summary}</span>
        <span class={`tool-status tool-status-${status}`}>{status}</span>
      </summary>
      <div class="tool-body">
        {summary !== "" ? <p class="tool-full">{summary}</p> : null}
        {diff !== "" ? <DiffView diff={diff} truncated={diffTruncated} /> : null}
        {output !== "" ? <pre class="tool-output">{output}</pre> : null}
        {result?.output_truncated === true ? (
          <p class="muted small-text">Output truncated by Deck.</p>
        ) : null}
      </div>
    </details>
  );
}

const HANDOFF_STATE = { done: "done", failed: "failed", blocked: "blocked" } as const;

// One agent's message to others, or a task's report to whoever handed it
// over, as the desktop's hand-off card: who it went to, the task and how it
// ended, and the words, folded when long.
function HandoffCard({
  h,
  roster,
  theme,
}: {
  h: Handoff;
  roster: AgentLine[];
  theme: Theme | null;
}) {
  const [all, setAll] = useState(false);
  const body = h.task === null ? h.body : h.task.result;
  const folds = foldsBody(body);
  return (
    <div
      class="card handoff"
      data-handoff={h.sub !== null ? "subthread" : (h.task?.state ?? "message")}
    >
      <p class="handoff-head">
        <span class="faint">
          {h.sub !== null ? "sub-thread answer to" : h.task === null ? "to" : "report to"}
        </span>
        {h.to.map((name) => {
          const kind = kindOf(name, roster);
          return (
            <span key={name} class="handoff-to" style={{ color: kindColour(kind) }}>
              <span aria-hidden="true">{kindMark(theme, kind) || "●"}</span>
              <span class="ellipsis">{name}</span>
            </span>
          );
        })}
        {h.task !== null ? (
          <span class="handoff-task">
            <span class="handoff-id">{h.task.id}</span>
            <span class="ellipsis">{h.task.title}</span>
            <span class={`handoff-state state-${h.task.state}`}>{HANDOFF_STATE[h.task.state]}</span>
          </span>
        ) : null}
      </p>
      {body !== "" ? (
        <div class={`handoff-body${folds && !all ? " folded" : ""}`}>
          <Markdown text={body} />
        </div>
      ) : null}
      {folds || (h.sub !== null && h.sub.id !== "") ? (
        <p class="handoff-foot">
          {folds ? (
            <button
              type="button"
              class="link-btn"
              aria-expanded={all}
              onClick={() => setAll((v) => !v)}
            >
              {all ? "Show less" : "Show all"}
            </button>
          ) : null}
          {h.sub !== null && h.sub.id !== "" ? (
            <a class="link-btn" href={formatRoute({ name: "session", id: h.sub.id })}>
              ↳ Open {h.sub.title !== "" ? h.sub.title : "sub-thread"}
            </a>
          ) : null}
        </p>
      ) : null}
    </div>
  );
}

function Truncated({ on }: { on: boolean }) {
  return on ? <p class="muted small-text">Text truncated by Deck.</p> : null;
}

function ChunkView({
  chunk,
  user,
  roster,
  hue,
}: {
  chunk: Chunk;
  user: boolean;
  roster: AgentLine[];
  hue: string;
}) {
  const { theme } = useAppState();
  switch (chunk.k) {
    case "text": {
      const h = user ? null : handoffOf(chunk.text);
      if (h !== null) return <HandoffCard h={h} roster={roster} theme={theme} />;
      return (
        <>
          {user ? <p class="user-text">{chunk.text}</p> : <Markdown text={chunk.text} />}
          <Truncated on={chunk.truncated} />
        </>
      );
    }
    case "reasoning":
      return (
        <details class="reasoning">
          <summary>Thinking</summary>
          <p class="user-text">{chunk.text}</p>
          <Truncated on={chunk.truncated} />
        </details>
      );
    case "tool":
      return <ToolCard call={chunk.call} result={chunk.result} hue={hue} />;
    case "image":
      // v1 has no way to fetch an image's bytes back (§3.7): show the
      // label the person gave it, then its name.
      return (
        <p class="image-chip" data-kind={chunk.mime.startsWith("video/") ? "video" : "image"}>
          <span class="image-glyph" aria-hidden="true">
            {chunk.mime.startsWith("video/") ? "▶" : "▣"}
          </span>
          {chunk.label !== undefined ? <span class="image-label">{chunk.label} </span> : null}
          <span class={chunk.label !== undefined ? "muted" : ""}>{chunk.name}</span>
          {chunk.mime !== "" && chunk.label === undefined ? (
            <span class="muted"> ({chunk.mime})</span>
          ) : null}
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
        <button type="button" class="btn small" disabled={saving} onClick={onDone}>
          Cancel
        </button>
        <button
          type="submit"
          class="btn small primary"
          disabled={!canSaveEdit(original, draft, online, saving)}
        >
          {saving ? "Saving..." : "Save"}
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
  const kind = line?.kind ?? kindOf(agentId, roster);
  const mark = kindMark(theme, kind);
  const Tag = inline ? "span" : "p";
  return (
    <Tag class="agent-name" style={{ color: kindColour(kind) }}>
      {mark !== "" ? (
        <span class="agent-mark" aria-hidden="true">
          {mark}{" "}
        </span>
      ) : null}
      <span class="agent-who">{line?.name ?? agentId}</span>
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
          <ChunkView key={i} chunk={c} user={true} roster={roster} hue="" />
        ))}
      </div>
    );
  }
  // A parent agent's brief to this sub-thread: the agent's words in a card,
  // not the person's bubble, under who sent them from which thread.
  const brief = briefOf(message);
  if (brief !== null) {
    return (
      <article class="msg msg-brief card">
        <p class="brief-from">
          <span class="faint" aria-hidden="true">
            ↳
          </span>
          <span class="faint">From</span>
          <AgentName agentId={brief.from} roster={roster} inline />
          {brief.thread !== "" ? (
            <span class="brief-thread ellipsis">
              <span class="faint" aria-hidden="true">
                ·{" "}
              </span>
              {brief.thread}
            </span>
          ) : null}
          <span class="brief-kind faint">{brief.first ? "brief" : "follow-up"}</span>
        </p>
        {brief.body !== "" ? <Markdown text={brief.body} /> : null}
      </article>
    );
  }
  const user = message.role === "user";
  const canEdit = user && message.editable === true;
  if (editing && canEdit) {
    return (
      <article class="msg msg-editing">
        <EditBox message={message} online={online} onDone={() => setEditing(false)} />
      </article>
    );
  }
  if (user) {
    // The person's turn: a bubble tinted with the accent, on the right.
    return (
      <article class="msg msg-turn">
        <div class="msg-user">
          {chunks.map((c, i) => (
            <ChunkView key={i} chunk={c} user={true} roster={roster} hue="" />
          ))}
        </div>
        {message.edited === true || canEdit ? (
          <footer class="msg-foot">
            {message.edited === true ? <span class="msg-edited">edited</span> : null}
            {canEdit ? (
              <button
                type="button"
                class="msg-edit"
                aria-label="Edit message"
                title="Edit message"
                disabled={!online}
                onClick={() => setEditing(true)}
              >
                Edit
              </button>
            ) : null}
          </footer>
        ) : null}
      </article>
    );
  }
  const hue = kindColour(roster.find((a) => a.id === message.agent)?.kind ?? "");
  return (
    <article class="msg msg-assistant">
      {message.agent !== undefined ? <AgentName agentId={message.agent} roster={roster} /> : null}
      {chunks.map((c, i) => (
        <ChunkView key={i} chunk={c} user={false} roster={roster} hue={hue} />
      ))}
      {message.edited === true ? <span class="msg-edited">edited</span> : null}
      {!message.finished ? (
        <span class="typing" role="status" aria-label="Working">
          <span />
          <span />
          <span />
        </span>
      ) : null}
    </article>
  );
}
