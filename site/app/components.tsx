"use client";

import { useState } from "react";

// A command line with a copy button. The command is selectable text too.
export function Command({ label, cmd }: { label?: string; cmd: string }) {
  const [done, setDone] = useState(false);
  const copy = () => {
    void navigator.clipboard.writeText(cmd).then(() => {
      setDone(true);
      setTimeout(() => setDone(false), 1400);
    });
  };
  return (
    <div className="command">
      {label !== undefined && <span className="command-label">{label}</span>}
      <code>
        <span aria-hidden className="prompt">
          $
        </span>
        {cmd}
      </code>
      <button type="button" onClick={copy} aria-label={`Copy: ${cmd}`}>
        {done ? "Copied" : "Copy"}
      </button>
    </div>
  );
}
