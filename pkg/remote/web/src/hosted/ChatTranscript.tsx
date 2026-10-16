import type { Message } from "../protocol";
import { type Chunk, groupParts } from "../transcript";
import { DiffView } from "../ui/DiffView";
import { Markdown } from "../ui/Markdown";

function Truncation({ truncated }: { truncated: boolean }) {
  return truncated ? <p class="muted small-text">Truncated by Deck.</p> : null;
}

function ChunkView({ chunk }: { chunk: Chunk }) {
  switch (chunk.k) {
    case "text":
      return (
        <>
          <Markdown text={chunk.text} />
          <Truncation truncated={chunk.truncated} />
        </>
      );
    case "reasoning":
      return (
        <details class="reasoning">
          <summary>Thinking</summary>
          <p class="user-text">{chunk.text}</p>
          <Truncation truncated={chunk.truncated} />
        </details>
      );
    case "image":
      return (
        <p class="image-chip">
          {chunk.name} {chunk.mime === "" ? "" : `(${chunk.mime})`}
        </p>
      );
    case "tool": {
      const { call, result } = chunk;
      const diff = result?.diff ?? call?.diff ?? "";
      return (
        <details class="tool" open={result?.is_error === true}>
          <summary>
            {call?.name ?? result?.name ?? "Tool"} ·{" "}
            {result === null ? "running" : result.is_error ? "failed" : "done"}
          </summary>
          {call?.summary ? <p class="user-text">{call.summary}</p> : null}
          {diff !== "" ? (
            <DiffView
              diff={diff}
              truncated={result?.diff_truncated === true || call?.diff_truncated === true}
            />
          ) : null}
          {result?.output ? <pre class="tool-output">{result.output}</pre> : null}
          <Truncation truncated={result?.output_truncated === true} />
        </details>
      );
    }
  }
}

// No v2 controller/context dependency: hosted chat renders protocol text as
// text nodes and the shared safe Markdown component, including tool output.
export function ChatTranscript({ messages }: { messages: Message[] }) {
  return (
    <>
      {messages.map((message) => (
        <article key={message.id} class={`msg msg-${message.role}`}>
          <p class="agent-name">
            {message.role === "user" ? "You" : (message.agent ?? message.role)}
          </p>
          {groupParts(message.parts).map((chunk, index) => (
            <ChunkView key={`${message.id}:${index}`} chunk={chunk} />
          ))}
          {message.edited === true ? <span class="msg-edited">edited</span> : null}
          {!message.finished && message.role === "assistant" ? (
            <span role="status">Working…</span>
          ) : null}
        </article>
      ))}
    </>
  );
}
