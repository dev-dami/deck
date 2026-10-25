// The composer's unsent text, per project and thread. It lives in this
// window's localStorage, so a draft survives a restart of the app. project
// is the project's source ("folder:<dir>", "paired:<id>"), which stays the
// same from one launch to the next; thread "" is the new-thread screen.

const PREFIX = "deck.desktop.draft:";

function keyOf(project: string, thread: string): string {
  return PREFIX + JSON.stringify([project, thread]);
}

export function loadDraft(project: string, thread: string): string {
  try {
    return window.localStorage.getItem(keyOf(project, thread)) ?? "";
  } catch {
    return "";
  }
}

// An empty draft removes the entry.
export function saveDraft(project: string, thread: string, text: string): void {
  try {
    if (text === "") window.localStorage.removeItem(keyOf(project, thread));
    else window.localStorage.setItem(keyOf(project, thread), text);
  } catch {
    // A private, full or locked storage keeps the draft for this run only.
  }
}
