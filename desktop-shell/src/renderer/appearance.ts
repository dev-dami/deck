import { useEffect } from "react";
import type { ThemePref } from "./prefs";

// Picks light or dark for the window and writes it to <html data-mode>.
// "deck" follows Deck's server theme, which the shared theme.ts writes as
// data-scheme; "system" follows the operating system.
export function useAppearance(pref: ThemePref): void {
  useEffect(() => {
    const root = document.documentElement;
    const media = window.matchMedia("(prefers-color-scheme: light)");
    const apply = () => {
      const light =
        pref === "light" ||
        (pref === "system" && media.matches) ||
        (pref === "deck" && root.getAttribute("data-scheme") === "light");
      root.setAttribute("data-mode", light ? "light" : "dark");
    };
    apply();
    const watch = new MutationObserver(apply);
    watch.observe(root, { attributes: true, attributeFilter: ["data-scheme"] });
    media.addEventListener("change", apply);
    return () => {
      watch.disconnect();
      media.removeEventListener("change", apply);
    };
  }, [pref]);
}
