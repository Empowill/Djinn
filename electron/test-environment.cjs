"use strict";

const fs = require("node:fs/promises");
const path = require("node:path");
const { execFile } = require("node:child_process");
const { promisify } = require("node:util");
const { normalizeLoopbackUrl, probeUrl } = require("./actions.cjs");
const execute = promisify(execFile);
const cache = new Map();
const CACHE_MS = 15000;

/** Fixed read-only diagnostics avoid asking a provider to escalate routine checks. */
async function inspectTestEnvironment(projectRoot, input = {}, dependencies = {}) {
  if (!input || typeof input !== "object" || Array.isArray(input) || Object.keys(input).some((key) => !["directory", "urls"].includes(key)))
    throw new Error("Le diagnostic accepte uniquement un dossier et des URL locales.");
  const directory = input.directory || "";
  if (typeof directory !== "string" || directory.length > 4096 || /[\0\r\n]/.test(directory) || /^(?:[\\/]|[A-Za-z]:)/.test(directory) || directory.replaceAll("\\", "/").split("/").includes(".."))
    throw new Error("Le dossier du diagnostic doit rester dans le projet.");
  const root = await fs.realpath(projectRoot);
  const cwd = await fs.realpath(path.resolve(root, directory));
  const relative = path.relative(root, cwd);
  if (relative === ".." || relative.startsWith(`..${path.sep}`) || path.isAbsolute(relative))
    throw new Error("Le dossier du diagnostic sort du projet.");
  if (input.urls !== undefined && (!Array.isArray(input.urls) || input.urls.length > 8)) throw new Error("Huit URL locales maximum.");
  const urls = (input.urls || []).map((value) => {
    const url = normalizeLoopbackUrl(value);
    if (!url) throw new Error("Le diagnostic vérifie uniquement des serveurs locaux.");
    const parsed = new URL(url);
    if (parsed.pathname !== "/" || parsed.search || parsed.hash) throw new Error("Le diagnostic vérifie uniquement la racine d’un serveur.");
    return url;
  });
  const now = dependencies.now || Date.now;
  const key = `${cwd}:${JSON.stringify(urls)}`;
  const prior = cache.get(key);
  if (!dependencies.execute && prior && now() - prior.at < CACHE_MS) return { ...prior.result, cached: true };
  let scripts = [];
  try {
    const file = await fs.realpath(path.join(cwd, "package.json"));
    const packageRelative = path.relative(root, file);
    if (packageRelative === ".." || packageRelative.startsWith(`..${path.sep}`) || path.isAbsolute(packageRelative)) throw new Error("Le manifeste sort du projet.");
    const stat = await fs.stat(file);
    if (stat.size > 1000000) throw new Error("Le manifeste est trop volumineux.");
    const pkg = JSON.parse(await fs.readFile(file, "utf8"));
    scripts = Object.keys(pkg.scripts || {}).slice(0, 100);
  } catch (error) {
    if (error.code !== "ENOENT") throw error;
  }
  const checkedAt = new Date(now()).toISOString();
  // Never use a remote Docker context for a routine local preview diagnostic.
  const localEnvironment = { ...process.env };
  delete localEnvironment.DOCKER_CONTEXT;
  delete localEnvironment.DOCKER_TLS_VERIFY;
  delete localEnvironment.DOCKER_CERT_PATH;
  localEnvironment.DOCKER_HOST = process.env.DOCKER_HOST?.startsWith("unix://") ? process.env.DOCKER_HOST : "unix:///var/run/docker.sock";
  const [containers, servers] = await Promise.all([
    (dependencies.execute || execute)("docker", ["ps", "--format", "{{.Names}}\t{{.Ports}}"], {
      cwd, env: localEnvironment, shell: false, timeout: 2000, maxBuffer: 64000, windowsHide: true,
    }).then((result) => ({ available: true, detail: String(result.stdout || "").slice(0, 16000), checkedAt }), (error) => ({ available: false, detail: String(error.code === "ENOENT" ? "Docker indisponible" : error.stderr || error.message).slice(0, 1000), checkedAt })),
    Promise.all(urls.map(async (url) => ({ url, reachable: await (dependencies.probe || probeUrl)(url, 700), checkedAt }))),
  ]);
  const result = { directory: relative || ".", scripts, containers, servers, checkedAt, cached: false };
  if (!dependencies.execute) {
    cache.set(key, { at: now(), result });
    while (cache.size > 64) cache.delete(cache.keys().next().value);
  }
  return result;
}

module.exports = { inspectTestEnvironment, CACHE_MS };
