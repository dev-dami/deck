// Which system the window runs on, for the icon's shape and the sizes main
// wants, and what the icon can and cannot change there. Pure.

import { APP_ICON_SIZES, type LauncherState } from "../../shared/bridge";
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

// What the picker says about where the icon shows. On a Mac only the
// running app's icon changes; elsewhere the installed app's launchers
// follow (main/launcherIcon.ts), and Classic puts their own back.
export const ICON_NOTE: Record<HostOs, string> = {
  mac: "Shows in the Dock while Deck runs. Finder, Launchpad and the Dock before Deck starts keep the standard icon.",
  windows:
    "Shows on Deck's window and taskbar, and on the desktop, Start menu and pinned shortcuts Deck's installer made. Classic puts back the standard icon.",
  linux: "Shows on Deck's window and on the launcher Deck's package installed. Classic puts back the standard icon.",
};

// What the picker adds when the launchers could not take the icon.
export const LAUNCHER_NOTE: Partial<Record<LauncherState, string>> = {
  denied:
    "Deck is installed for everyone on this computer, so its desktop and Start menu shortcuts belong to Windows and need an administrator to change. Deck's window and taskbar show the icon.",
  failed: "Deck could not change the icon of its shortcuts. Deck's window shows the icon; the log has the details.",
};
