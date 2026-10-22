import { mkdirSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { renderIcon } from "../../pkg/remote/web/scripts/icons";

// electron-builder takes the app and installer icon from build/icon.png; it
// is the same icon the phone apps draw. Run with bun after changing icons.ts.
const output = join(dirname(fileURLToPath(import.meta.url)), "../build");
mkdirSync(output, { recursive: true });
writeFileSync(join(output, "icon.png"), renderIcon(1024));
