import type { LucideIcon } from "lucide-react";
import { cn } from "../../lib/cn";

// An empty list in two short lines: what shows here, and how it gets here.
export function EmptyState({
  icon: Icon,
  title,
  hint,
  className,
}: {
  icon: LucideIcon;
  title: string;
  hint: string;
  className?: string;
}) {
  return (
    <div className={cn("empty flex flex-col items-center gap-1 px-4 py-10 text-center text-faint", className)}>
      <Icon aria-hidden className="mb-1 size-4" />
      <p className="text-sm">{title}</p>
      <p className="text-balance text-xs leading-relaxed">{hint}</p>
    </div>
  );
}
