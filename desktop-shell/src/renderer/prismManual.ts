// Prism reads this before it loads: in manual mode it never scans the page
// on its own, so it only tokenizes what highlight.ts hands it.
(globalThis as { Prism?: unknown }).Prism = { manual: true };
