import { cn } from "../../lib/cn";

// A file path that never loses its name: when it does not fit, the folder
// gives way first, ending in an ellipsis, and the name keeps the room it
// needs (it is cut too only when it alone is wider than the line). The whole
// path is the title.
export function PathTail({ path, className, folderClassName = "text-faint" }: { path: string; className?: string; folderClassName?: string }) {
  const cut = path.lastIndexOf("/") + 1;
  return (
    <span className={cn("path-tail flex min-w-0 font-mono", className)} title={path}>
      {cut > 0 && <span className={cn("min-w-0 shrink truncate", folderClassName)}>{path.slice(0, cut)}</span>}
      <span className="min-w-0 max-w-full shrink-0 truncate">{path.slice(cut)}</span>
    </span>
  );
}
