import type { ComponentProps, ElementType } from "react";
import { cn } from "../../lib/cn";

// A card: a rounded box of the first surface with a hairline ring. Nothing
// inside runs out of it: the .card rule in styles.css wraps any unbroken
// text (paths, URLs, hashes, commands) where it must, keeps flex children
// from growing past it, and gives code and tables a scroll of their own.
// Every card in the app takes CARD or this component, so the guarantee is
// one rule, checked by test/e2e/overflow.spec.ts.
export const CARD = "card rounded-xl bg-surface-1 ring-1 ring-hairline";

type CardProps<T extends ElementType> = { as?: T } & ComponentProps<T>;

export function Card<T extends ElementType = "div">({ as, className, ...props }: CardProps<T>) {
  const Tag: ElementType = as ?? "div";
  return <Tag data-slot="card" className={cn(CARD, className)} {...props} />;
}
