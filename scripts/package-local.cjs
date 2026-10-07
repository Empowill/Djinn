"use strict";

// Package each local version beside the running app, so an ongoing mission
// never observes a half-replaced bundle. The human switches after its passage.
const path = require("node:path");
const { spawnSync } = require("node:child_process");
const root = path.resolve(__dirname, "..");
const { version } = require(path.join(root, "package.json"));
const suffix = process.env.DJINN_PACKAGE_SUFFIX || "";
if (suffix && !/^[a-z][a-z0-9-]{0,63}$/.test(suffix)) {
  throw new Error("Le suffixe du paquet local doit être un nom simple.");
}
if (!/^\d+\.\d+\.\d+(?:-[a-zA-Z0-9.-]+)?$/.test(version)) {
  throw new Error("La version du paquet ne permet pas un dossier local sûr.");
}
const result = spawnSync(
  process.execPath,
  [
    require.resolve("electron-builder/cli.js"),
    "--dir",
    `--config.directories.output=release/v${version}${suffix ? `-${suffix}` : ""}`,
    // The local development dependency already contains the matching runtime.
    // Reuse it instead of downloading another Electron distribution.
    `--config.electronDist=${path.join(path.dirname(require.resolve("electron")), "dist")}`,
    `--config.electronVersion=${require("electron/package.json").version}`,
    ...(process.platform === "darwin" ? ["--config.mac.identity=-"] : []),
  ],
  { cwd: root, stdio: "inherit", env: process.env },
);
if (result.error) throw result.error;
process.exitCode = result.status ?? 1;
