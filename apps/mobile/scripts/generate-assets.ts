import { mkdirSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { renderIcon } from "../../../pkg/remote/web/scripts/icons";

// Reuse Deck's existing vector-like card geometry, rasterized at native icon
// resolution. No screenshots or remotely fetched branding assets.
const output = join(dirname(fileURLToPath(import.meta.url)), "../assets");
mkdirSync(output, { recursive: true });
writeFileSync(join(output, "icon.png"), renderIcon(1024));
writeFileSync(join(output, "adaptive-icon.png"), renderIcon(1024, true));
