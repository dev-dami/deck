import { useState } from "preact/hooks";
import type { ChangedFile } from "../protocol";
import { formatRoute } from "../route";
import { type AppState, sessionTitle } from "../state";
import { DiffView } from "./DiffView";
import { Header } from "./Header";
import { useController, useLoad } from "./hooks";

function FileCard({ file }: { file: ChangedFile }) {
  const [open, setOpen] = useState(false);
  const hasDiff = file.diff !== undefined && file.diff !== "";
  return (
    <li class="file-card">
      <button
        type="button"
        class="row option"
        aria-expanded={open}
        disabled={!hasDiff}
        onClick={() => setOpen((o) => !o)}
      >
        <span class="row-main">
          <span class="row-title path">{file.path}</span>
        </span>
        <span class="row-meta">
          <span class="added">+{file.added}</span>
          <span class="removed">-{file.removed}</span>
        </span>
      </button>
      {open && hasDiff ? (
        <DiffView
          diff={file.diff ?? ""}
          {...(file.truncated === undefined ? {} : { truncated: file.truncated })}
        />
      ) : null}
    </li>
  );
}

export function ChangesScreen({ state, id }: { state: AppState; id: string }) {
  const controller = useController();
  const online = state.link === "online";
  const changes = useLoad<ChangedFile[]>(
    () => (online ? controller.changes(id) : Promise.resolve(null)),
    `${id}:${online}`,
  );
  const session = state.sessions.find((s) => s.id === id);
  const files = changes.data ?? [];
  const added = files.reduce((n, f) => n + f.added, 0);
  const removed = files.reduce((n, f) => n + f.removed, 0);

  return (
    <div class="screen">
      <Header
        title="Changes"
        subtitle={sessionTitle(session)}
        back={formatRoute({ name: "session", id })}
        state={state}
        actions={
          <button type="button" class="btn ghost" onClick={changes.reload}>
            Refresh
          </button>
        }
      />
      <main class="content">
        {changes.loading ? <p class="muted empty">Loading changes...</p> : null}
        {!changes.loading && files.length === 0 ? (
          <p class="muted empty">No file changes in this session.</p>
        ) : null}
        {files.length > 0 ? (
          <>
            <p class="muted">
              {files.length} {files.length === 1 ? "file" : "files"}{" "}
              <span class="added">+{added}</span> <span class="removed">-{removed}</span>
            </p>
            <ul class="list">
              {files.map((f) => (
                <FileCard key={f.path} file={f} />
              ))}
            </ul>
          </>
        ) : null}
      </main>
    </div>
  );
}
