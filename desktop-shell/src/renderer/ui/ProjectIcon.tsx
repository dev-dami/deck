import type { CSSProperties } from "react";
import { cn } from "../lib/cn";
import { projectTile, useProjectLogo } from "../projectIcon";
import { Laptop } from "./icons";

// A project's tile (projectIcon.ts): its repo's logo when it has one, else
// its monogram in its own hue; a paired Deck shows a computer. size is in
// pixels; under 20 the monogram is one letter. The look is in styles.css
// (project tiles).
export function ProjectIcon({ source, name, size = 16, className }: { source: string; name: string; size?: number; className?: string }) {
  const logo = useProjectLogo(source);
  const t = projectTile(source, name, size < 20 ? 1 : 2);
  const style = { "--tile-h": t.hue, "--tile-size": `${size}px` } as CSSProperties;
  return (
    <span
      aria-hidden
      className={cn("project-tile", className)}
      style={style}
      data-logo={logo !== null ? "" : undefined}
      data-letters={logo === null && !t.paired ? t.glyph.length : undefined}
    >
      {logo !== null ? <img src={logo} alt="" draggable={false} /> : t.paired ? <Laptop weight="bold" /> : t.glyph}
    </span>
  );
}
