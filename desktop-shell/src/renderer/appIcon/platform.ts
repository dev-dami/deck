// Which system the window runs on, for the icon's shape and the sizes main
// wants, and what the icon can and cannot change there. Pure.

import { APP_ICON_SIZES } from "../../shared/bridge";
import type { IconPlatform } from "./geometry";

export type HostOs = "mac" | "windows" | "linux";

// From navigator.platform, or the user agent when that is empty.
export function hostOs(platform: string): HostOs {
  if (/Mac/i.test(platform)) return "mac";
  if (/Win/i.test(platform)) return "windows";
  return "linux";
}

export function currentOs(): HostOs {
  if (typeof navigator === "undefined") return "linux";
  return hostOs(navigator.platform || navigator.userAgent);
}

// A Mac draws the icon as a rounded square inside a margin, with its own
// shadow; Windows and Linux show the image as it is.
export const iconPlatform = (os: HostOs): IconPlatform => (os === "mac" ? "mac" : "other");

export const iconSizes = (os: HostOs): readonly number[] => APP_ICON_SIZES[iconPlatform(os)];

// What the picker says about where the icon shows. Deck changes the
// running app's icon only; the installed app keeps its own.
export const ICON_NOTE: Record<HostOs, string> = {
  mac: "Shows in the Dock while Deck runs. Finder, Launchpad and the Dock before Deck starts keep the standard icon.",
  windows: "Shows on Deck's window and taskbar button while it runs. Pinned shortcuts and the Start menu keep the standard icon.",
  linux: "Shows on Deck's window while it runs. The launcher's icon stays the standard one, and some docks show it even while Deck runs.",
};
