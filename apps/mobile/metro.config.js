const path = require("node:path");
const { getDefaultConfig } = require("expo/metro-config");

const projectRoot = __dirname;
const workspaceRoot = path.resolve(projectRoot, "../..");
const config = getDefaultConfig(projectRoot);

// Shared modules are plain TypeScript source outside this package. Prefer
// native dependencies here so web/PReact dependencies never shadow React.
config.watchFolders = [
  path.join(workspaceRoot, "pkg/remote/web/src"),
  path.join(workspaceRoot, "pkg/remote/wire/testdata"),
];
config.resolver.nodeModulesPaths = [path.join(projectRoot, "node_modules")];

module.exports = config;
