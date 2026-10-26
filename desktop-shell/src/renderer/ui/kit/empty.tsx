import type { IconType } from "../icons";
import { cn } from "../../lib/cn";

// An empty list in two short lines: what shows here, and how it gets here.
export function EmptyState({
  icon: Icon,
  title,
  hint,
  className,
}: {
  icon: IconType;
  title: string;
  hint: string;
  className?: string;
}) {
  return (
    <div className={cn("empty flex flex-col items-center gap-1 px-4 py-10 text-center text-faint", className)}>
      <Icon aria-hidden className="mb-1.5 size-6 text-muted-foreground" weight="duotone" />
      <p className="text-foreground text-sm">{title}</p>
      <p className="text-balance text-xs leading-relaxed">{hint}</p>
    </div>
  );
}
