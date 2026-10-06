// Metro config for the monorepo layout: mobile/ depends on @alexmessages/shared
// via a file: symlink into ../shared. watchFolders makes Metro follow the link;
// unstable_enableSymlinks is on by default in Expo SDK 57.
const { getDefaultConfig } = require("expo/metro-config");
const path = require("path");

const projectRoot = __dirname;
const workspaceRoot = path.resolve(projectRoot, "..");

const config = getDefaultConfig(projectRoot);

config.watchFolders = [workspaceRoot];
config.resolver.nodeModulesPaths = [
  path.resolve(projectRoot, "node_modules"),
  path.resolve(workspaceRoot, "node_modules"),
];

module.exports = config;
