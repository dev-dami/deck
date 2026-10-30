import { cn } from "../../lib/cn";

// Where a path's name begins: after its last slash. A glob whose last part
// is a pattern ("pkg/remote/**", "session/*.go") keeps the folder it is in
// with it, since the pattern alone does not say which files.
export function tailStart(path: string): number {
  const cut = path.lastIndexOf("/") + 1;
  if (cut > 1 && /[*?[{]/.test(path.slice(cut))) return path.lastIndexOf("/", cut - 2) + 1;
  return cut;
}

// A file path that never loses its name: when it does not fit, the folder
// gives way first, ending in an ellipsis, and the name keeps the room it
// needs (it is cut too only when it alone is wider than the line). The whole
// path is the title, unless a title is given.
export function PathTail({
  path,
  className,
  folderClassName = "text-faint",
  title = path,
}: { path: string; className?: string; folderClassName?: string; title?: string }) {
  const cut = tailStart(path);
  return (
    <span className={cn("path-tail flex min-w-0 font-mono", className)} title={title}>
      {cut > 0 && <span className={cn("min-w-0 shrink truncate", folderClassName)}>{path.slice(0, cut)}</span>}
      <span className="min-w-0 max-w-full shrink-0 truncate">{path.slice(cut)}</span>
    </span>
  );
}
