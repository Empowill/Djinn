"use strict";

const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const asar = require("@electron/asar");
const root = path.resolve(__dirname, "..");
const pkg = require(path.join(root, "package.json"));
const suffix = process.env.DJINN_PACKAGE_SUFFIX || "";
if (suffix && !/^[a-z][a-z0-9-]{0,63}$/.test(suffix)) {
  throw new Error("Le suffixe du paquet local doit être un nom simple.");
}
const release = path.join(root, "release", `v${pkg.version}${suffix ? `-${suffix}` : ""}`);
function files(directory) {
  return fs.readdirSync(directory, { withFileTypes: true }).flatMap((entry) => {
    const full = path.join(directory, entry.name);
    return entry.isDirectory() ? files(full) : entry.isFile() ? [full] : [];
  });
}
const candidates = files(release).filter(
  (file) => path.basename(file) === "app.asar",
);
assert.equal(candidates.length, 1, "Un unique bundle local doit être présent.");
const archive = candidates[0];
const bundled = JSON.parse(asar.extractFile(archive, "package.json"));
assert.equal(bundled.version, pkg.version);
assert.equal(bundled.name, pkg.name);
let verified = 0;
for (const directory of ["dist", "electron"]) {
  const expected = files(path.join(root, directory));
  const included = asar
    .listPackage(archive)
    .filter(
      (file) =>
        file.startsWith(`/${directory}/`) &&
        !asar.statFile(archive, file.replace(/^\/+/, "")).files,
    );
  assert.equal(
    included.length,
    expected.length,
    `${directory} doit contenir les mêmes fichiers.`,
  );
  for (const file of expected) {
    const relative = path.relative(root, file).split(path.sep).join("/");
    assert.deepEqual(
      asar.extractFile(archive, relative),
      fs.readFileSync(file),
      `${relative} doit correspondre aux sources vérifiées.`,
    );
    verified++;
  }
}
console.log(
  `PASS Djinn ${pkg.version}: ${verified} fichiers identiques dans ${path.relative(root, archive)}.`,
);
