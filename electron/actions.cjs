'use strict';

const fs = require('node:fs');
const fsp = fs.promises;
const path = require('node:path');
const os = require('node:os');
const crypto = require('node:crypto');
const { spawn } = require('node:child_process');
const http = require('node:http');
const https = require('node:https');

const ACTION_KINDS = Object.freeze(['server', 'link', 'manual']);
const ACTION_STATUSES = Object.freeze(['pending', 'running', 'ready', 'done', 'error', 'stopped']);
const PACKAGE_MANAGERS = Object.freeze(['npm', 'pnpm', 'yarn', 'bun']);
const RECOGNIZED_SCRIPT = /^(?:dev|start|serve|preview)(?:[:._-][A-Za-z0-9][A-Za-z0-9._-]{0,63})?$/;
const MAX_ACTIONS_PER_TASK = 128;
const MAX_ACTION_TITLE_LENGTH = 1_000;
const MAX_ACTION_DETAIL_LENGTH = 100_000;
const MAX_ACTION_ERROR_LENGTH = 2_000;
const MAX_ACTION_URL_LENGTH = 2_048;
const MAX_ACTION_DIRECTORY_LENGTH = 1_024;
const MAX_ACTION_TEST_INSTRUCTIONS = 24;
const MAX_ACTION_TEST_TEXT_LENGTH = 4_000;
const MAX_LOG_LENGTH = 64_000;
const DEFAULT_STARTUP_TIMEOUT_MS = 20_000;
const MAX_STARTUP_TIMEOUT_MS = 60_000;
const DEFAULT_PROBE_TIMEOUT_MS = 800;
const STOP_GRACE_MS = 1_500;
const MAX_PROJECT_DIRECTORIES = 200;
const MAX_PROJECT_DEPTH = 2;
const SKIPPED_DIRECTORIES = new Set([
  '.git',
  '.hg',
  '.svn',
  'node_modules',
  'dist',
  'build',
  '.next',
  '.nuxt',
  '.turbo',
  '.cache',
  'coverage',
]);
const COMMON_RUNTIME_BIN_DIRS = Object.freeze([
  '/opt/homebrew/bin',
  '/usr/local/bin',
  path.join(os.homedir(), '.local', 'bin'),
  path.join(os.homedir(), '.npm-global', 'bin'),
  path.join(os.homedir(), '.bun', 'bin'),
  path.join(os.homedir(), '.volta', 'bin'),
  path.join(os.homedir(), '.asdf', 'shims'),
  path.join(os.homedir(), '.nvm', 'current', 'bin'),
]);

class ActionError extends Error {
  constructor(code, message, details) {
    super(message);
    this.name = 'ActionError';
    this.code = code;
    if (details !== undefined) this.details = details;
  }
}

function actionError(code, message, details) {
  return new ActionError(code, message, details);
}

function isRecord(value) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return false;
  const prototype = Object.getPrototypeOf(value);
  return prototype === Object.prototype || prototype === null;
}

function boundedString(value, name, max, { allowEmpty = true } = {}) {
  if (typeof value !== 'string') throw actionError('invalid_action', `${name} must be a string`);
  if (value.length > max) throw actionError('input_too_large', `${name} exceeds the ${max} character limit`);
  if (value.includes('\u0000')) throw actionError('invalid_action', `${name} contains a NUL character`);
  if (!allowEmpty && value.trim().length === 0) throw actionError('invalid_action', `${name} must not be empty`);
  return value;
}

function boundedIdentifier(value, name, max = 256) {
  return boundedString(value, name, max, { allowEmpty: false }).trim();
}

function clone(value) {
  if (typeof structuredClone === 'function') return structuredClone(value);
  return JSON.parse(JSON.stringify(value));
}

function validateHttpUrl(value, name = 'url') {
  const raw = boundedString(value, name, MAX_ACTION_URL_LENGTH, { allowEmpty: false });
  let parsed;
  try {
    parsed = new URL(raw);
  } catch {
    throw actionError('invalid_url', `${name} is invalid`);
  }
  if (!['http:', 'https:'].includes(parsed.protocol) || !parsed.hostname || parsed.username || parsed.password) {
    throw actionError('invalid_url', `${name} must be HTTP(S) without credentials`);
  }
  return parsed.toString();
}

function normalizeDirectory(value, name = 'directory') {
  if (value === undefined || value === null || value === '') return undefined;
  const directory = boundedString(value, name, MAX_ACTION_DIRECTORY_LENGTH, { allowEmpty: false });
  if (path.isAbsolute(directory) || /^[A-Za-z]:[\\/]/.test(directory)) {
    throw actionError('invalid_path', `${name} must be relative to the project`);
  }
  const normalized = directory.replaceAll('\\', '/');
  const pieces = normalized.split('/').filter(Boolean);
  if (pieces.some((piece) => piece === '..' || piece === '.')) {
    throw actionError('invalid_path', `${name} must not contain dot segments`);
  }
  return pieces.join('/');
}

function validateScriptName(value, name = 'script') {
  if (value === undefined || value === null) return undefined;
  const script = boundedString(value, name, 128, { allowEmpty: false }).trim();
  if (!RECOGNIZED_SCRIPT.test(script)) {
    throw actionError('invalid_action', `${name} must name a recognized development script`);
  }
  return script;
}

function validateTestInstructions(value) {
  if (value === undefined) return undefined;
  if (!Array.isArray(value) || value.length > MAX_ACTION_TEST_INSTRUCTIONS) {
    throw actionError('invalid_action', `action.testInstructions must contain at most ${MAX_ACTION_TEST_INSTRUCTIONS} entries`);
  }
  return value.map((instruction) => boundedString(
    instruction,
    'action.testInstructions',
    MAX_ACTION_TEST_TEXT_LENGTH,
    { allowEmpty: false },
  ));
}

function validateExpectedResult(value) {
  if (value === undefined) return undefined;
  return boundedString(value, 'action.expectedResult', MAX_ACTION_TEST_TEXT_LENGTH, { allowEmpty: false });
}

/**
 * Validate the data a model may propose. The model can name a package script,
 * but it can never supply a command, argv, shell string, environment, or cwd.
 */
function validateActionProposal(value) {
  if (!isRecord(value)) throw actionError('invalid_action', 'Action data must be an object');
  const forbidden = ['command', 'args', 'cwd', 'shell', 'env', 'operation', 'status', 'createdAt', 'updatedAt', 'error', 'testResult'];
  for (const field of forbidden) {
    if (Object.prototype.hasOwnProperty.call(value, field)) {
      throw actionError('invalid_action', `Action field ${field} is not accepted`);
    }
  }
  const kind = value.kind;
  if (!ACTION_KINDS.includes(kind)) throw actionError('invalid_action', `Unsupported action kind: ${String(kind)}`);
  const result = {
    kind,
    title: boundedString(value.title, 'action.title', MAX_ACTION_TITLE_LENGTH, { allowEmpty: false }),
  };
  if (value.id !== undefined) result.id = boundedIdentifier(value.id, 'action.id');
  if (value.detail !== undefined) {
    result.detail = boundedString(value.detail, 'action.detail', MAX_ACTION_DETAIL_LENGTH);
  }
  if (value.agentId !== undefined && value.agentId !== null) {
    result.agentId = boundedIdentifier(value.agentId, 'action.agentId');
  }
  if (value.workItemId !== undefined) result.workItemId = boundedIdentifier(value.workItemId, 'action.workItemId');
  if (value.target !== undefined) result.target = boundedString(value.target, 'action.target', MAX_ACTION_TITLE_LENGTH, { allowEmpty: false });
  if (value.directory !== undefined) result.directory = normalizeDirectory(value.directory);
  if (value.script !== undefined) result.script = validateScriptName(value.script, 'action.script');
  if (value.url !== undefined) result.url = validateHttpUrl(value.url, 'action.url');
  const testInstructions = validateTestInstructions(value.testInstructions);
  if (testInstructions !== undefined) result.testInstructions = testInstructions;
  const expectedResult = validateExpectedResult(value.expectedResult);
  if (expectedResult !== undefined) result.expectedResult = expectedResult;
  if (kind === 'link' && !result.url) throw actionError('invalid_action', 'Link actions require a URL');
  if (kind !== 'server' && result.script !== undefined) {
    throw actionError('invalid_action', 'Only server actions may name a script');
  }
  if (kind === 'manual' && result.url !== undefined) {
    throw actionError('invalid_action', 'Manual actions cannot carry a URL');
  }
  return result;
}

function actionNow() {
  return new Date().toISOString();
}

function createTaskAction(value, taskId, now = actionNow()) {
  const proposal = validateActionProposal(value);
  const id = proposal.id || `action-${crypto.randomUUID()}`;
  return {
    id,
    kind: proposal.kind,
    title: proposal.title,
    ...(proposal.detail !== undefined ? { detail: proposal.detail } : {}),
    status: 'pending',
    createdAt: now,
    updatedAt: now,
    ...(proposal.agentId ? { agentId: proposal.agentId } : {}),
    ...(proposal.workItemId ? { workItemId: proposal.workItemId } : {}),
    ...(proposal.target ? { target: proposal.target } : {}),
    ...(proposal.directory ? { directory: proposal.directory } : {}),
    ...(proposal.url ? { url: proposal.url } : {}),
    ...(proposal.script ? { script: proposal.script } : {}),
    ...(proposal.testInstructions ? { testInstructions: proposal.testInstructions } : {}),
    ...(proposal.expectedResult ? { expectedResult: proposal.expectedResult } : {}),
  };
}

function validateTaskId(value) {
  const result = boundedString(value, 'taskId', 256, { allowEmpty: false }).trim();
  if (!result) throw actionError('invalid_action', 'taskId must not be empty');
  return result;
}

function isLoopbackHost(hostname) {
  const normalized = String(hostname || '').toLowerCase().replace(/^\[|\]$/g, '');
  return normalized === 'localhost' || normalized === '127.0.0.1' || normalized === '::1';
}

function normalizeLoopbackUrl(rawUrl) {
  if (typeof rawUrl !== 'string' || rawUrl.length === 0 || rawUrl.length > MAX_ACTION_URL_LENGTH) return null;
  let parsed;
  try {
    parsed = new URL(rawUrl);
  } catch {
    return null;
  }
  const port = Number(parsed.port || (parsed.protocol === 'https:' ? 443 : 80));
  if (!['http:', 'https:'].includes(parsed.protocol) || !isLoopbackHost(parsed.hostname) || !Number.isInteger(port) || port < 1 || port > 65535) {
    return null;
  }
  parsed.hostname = '127.0.0.1';
  if (!parsed.port && port !== (parsed.protocol === 'https:' ? 443 : 80)) parsed.port = String(port);
  return parsed.toString();
}

function extractLoopbackUrl(text) {
  if (typeof text !== 'string' || text.length === 0) return null;
  const matches = text.match(/https?:\/\/(?:localhost|127\.0\.0\.1|\[?::1\]?)(?::\d{1,5})(?:\/[^\s<>'"`]*)?/gi) || [];
  for (const raw of matches) {
    const candidate = normalizeLoopbackUrl(raw.replace(/[),.;]+$/, ''));
    if (candidate) return candidate;
  }
  return null;
}

function portFromUrl(url) {
  try {
    const parsed = new URL(url);
    return Number(parsed.port || (parsed.protocol === 'https:' ? 443 : 80));
  } catch {
    return null;
  }
}

function frameworkForPackage(pkg) {
  const dependencies = {
    ...(isRecord(pkg.dependencies) ? pkg.dependencies : {}),
    ...(isRecord(pkg.devDependencies) ? pkg.devDependencies : {}),
    ...(isRecord(pkg.peerDependencies) ? pkg.peerDependencies : {}),
    ...(isRecord(pkg.optionalDependencies) ? pkg.optionalDependencies : {}),
  };
  const names = Object.keys(dependencies).map((name) => name.toLowerCase());
  if (names.some((name) => name === 'vite' || name === '@vitejs/plugin-react' || name === '@vitejs/plugin-vue')) return 'vite';
  if (names.some((name) => name === 'next')) return 'next';
  if (names.some((name) => name === 'nuxt' || name === 'nuxt3')) return 'nuxt';
  if (names.some((name) => name === 'astro')) return 'astro';
  if (names.some((name) => name === '@angular/cli' || name === '@angular/core')) return 'angular';
  if (names.some((name) => name === 'react-scripts')) return 'react-scripts';
  if (names.some((name) => name === 'gatsby')) return 'gatsby';
  if (names.some((name) => name === 'parcel' || name === '@parcel/core')) return 'parcel';
  if (names.some((name) => name === 'webpack' || name === 'webpack-dev-server')) return 'webpack';
  if (names.some((name) => name === 'svelte' || name === '@sveltejs/kit')) return 'svelte';
  if (names.some((name) => name === 'remix' || name === '@remix-run/dev')) return 'remix';
  return 'unknown';
}

function defaultFrameworkPort(framework) {
  return {
    vite: 5173,
    svelte: 5173,
    next: 3000,
    nuxt: 3000,
    remix: 3000,
    'react-scripts': 3000,
    astro: 4321,
    angular: 4200,
    gatsby: 8000,
    parcel: 1234,
    webpack: 8080,
    unknown: 3000,
  }[framework] || 3000;
}

function scriptPort(script, fallback) {
  if (typeof script === 'string') {
    const match = script.match(/(?:--port|-p|\bPORT\s*=)\s*(?:=\s*)?(\d{1,5})\b/i);
    const port = Number(match?.[1]);
    if (Number.isInteger(port) && port >= 1 && port <= 65535) return port;
  }
  return fallback;
}

function packageManagerFor(pkg, directory, projectRoot, lockFiles = new Set()) {
  const declared = typeof pkg.packageManager === 'string' ? pkg.packageManager.split('@')[0].toLowerCase() : '';
  if (PACKAGE_MANAGERS.includes(declared)) return declared;
  const localLocks = new Set(lockFiles);
  try {
    for (const name of fs.readdirSync(directory)) localLocks.add(name);
  } catch {
    // npm is the safe fallback when lockfile inspection is unavailable.
  }
  if (localLocks.has('pnpm-lock.yaml')) return 'pnpm';
  if (localLocks.has('yarn.lock')) return 'yarn';
  if (localLocks.has('bun.lockb') || localLocks.has('bun.lock')) return 'bun';
  try {
    for (const name of fs.readdirSync(projectRoot)) localLocks.add(name);
  } catch {
    // npm remains the fallback.
  }
  if (localLocks.has('pnpm-lock.yaml')) return 'pnpm';
  if (localLocks.has('yarn.lock')) return 'yarn';
  if (localLocks.has('bun.lockb') || localLocks.has('bun.lock')) return 'bun';
  return 'npm';
}

function packageInvocation(manager, script) {
  const command = process.platform === 'win32' && manager === 'npm' ? 'npm.cmd' : manager;
  return { command, args: ['run', script] };
}

function actionEnvironment(base = process.env) {
  const inherited = typeof base.PATH === 'string' ? base.PATH.split(path.delimiter) : [];
  const nvmBins = [];
  try {
    const nvmVersions = fs.readdirSync(path.join(os.homedir(), '.nvm', 'versions', 'node'));
    for (const version of nvmVersions.slice(-8)) {
      nvmBins.push(path.join(os.homedir(), '.nvm', 'versions', 'node', version, 'bin'));
    }
  } catch {
    // Finder-launched apps may not have nvm installed; inherited/PATH bins remain.
  }
  const environment = {
    ...base,
    PATH: [...new Set([...inherited, ...COMMON_RUNTIME_BIN_DIRS, ...nvmBins].filter(Boolean))].join(path.delimiter),
    NO_COLOR: '1',
  };
  // A packaged Electron process can carry this marker. Passing it into a
  // project's Node child makes Node behave like Electron's embedded runtime.
  delete environment.ELECTRON_RUN_AS_NODE;
  return environment;
}

async function collectProjectDirectories(root, maxDepth = MAX_PROJECT_DEPTH, maxDirectories = MAX_PROJECT_DIRECTORIES) {
  const result = [];
  const queue = [{ directory: root, depth: 0 }];
  while (queue.length > 0 && result.length < maxDirectories) {
    const current = queue.shift();
    result.push(current.directory);
    if (current.depth >= maxDepth) continue;
    let entries;
    try {
      entries = await fsp.readdir(current.directory, { withFileTypes: true });
    } catch {
      continue;
    }
    for (const entry of entries) {
      if (!entry.isDirectory() || SKIPPED_DIRECTORIES.has(entry.name) || entry.name.startsWith('.')) continue;
      queue.push({ directory: path.join(current.directory, entry.name), depth: current.depth + 1 });
      if (result.length + queue.length >= maxDirectories) break;
    }
  }
  return result;
}

async function readPackage(directory) {
  const packagePath = path.join(directory, 'package.json');
  try {
    const text = await fsp.readFile(packagePath, 'utf8');
    const pkg = JSON.parse(text);
    if (!isRecord(pkg) || !isRecord(pkg.scripts)) return null;
    return { pkg, packagePath };
  } catch {
    return null;
  }
}

function recognizedScriptNames(scripts) {
  if (!isRecord(scripts)) return [];
  const preferred = ['dev', 'start', 'serve', 'preview'];
  const exact = preferred.filter((name) => typeof scripts[name] === 'string' && scripts[name].trim());
  if (exact.length > 0) return exact;
  return Object.keys(scripts)
    .filter((name) => RECOGNIZED_SCRIPT.test(name) && typeof scripts[name] === 'string' && scripts[name].trim())
    .sort((left, right) => left.localeCompare(right));
}

async function detectServerCandidates(projectRoot, options = {}) {
  const root = path.resolve(projectRoot);
  const directories = await collectProjectDirectories(
    root,
    Number.isInteger(options.maxDepth) ? Math.min(Math.max(options.maxDepth, 0), MAX_PROJECT_DEPTH) : MAX_PROJECT_DEPTH,
    Number.isInteger(options.maxDirectories)
      ? Math.min(Math.max(options.maxDirectories, 1), MAX_PROJECT_DIRECTORIES)
      : MAX_PROJECT_DIRECTORIES,
  );
  const candidates = [];
  for (const directory of directories) {
    const loaded = await readPackage(directory);
    if (!loaded) continue;
    const scripts = loaded.pkg.scripts;
    const scriptNames = recognizedScriptNames(scripts);
    if (scriptNames.length === 0) continue;
    const script = scriptNames[0];
    const framework = frameworkForPackage(loaded.pkg);
    const port = scriptPort(scripts[script], defaultFrameworkPort(framework));
    const manager = packageManagerFor(loaded.pkg, directory, root);
    const invocation = packageInvocation(manager, script);
    const directoryName = path.relative(root, directory).split(path.sep).filter(Boolean).join('/');
    candidates.push({
      cwd: directory,
      directory: directoryName || undefined,
      packagePath: loaded.packagePath,
      packageName: typeof loaded.pkg.name === 'string'
        ? loaded.pkg.name.slice(0, 256)
        : path.basename(directory).slice(0, 256),
      framework,
      manager,
      script,
      scriptText: scripts[script],
      port,
      ...invocation,
    });
  }
  return candidates;
}

function resolveProjectPath(projectRoot, directory) {
  const root = path.resolve(projectRoot);
  const target = path.resolve(root, directory || '.');
  if (target !== root && !target.startsWith(`${root}${path.sep}`)) {
    throw actionError('invalid_path', 'Action directory escapes the project');
  }
  return target;
}

function candidateMatchesAction(candidate, action) {
  if (action.script && candidate.script !== action.script) return false;
  if (action.directory && candidate.directory !== action.directory.replaceAll('\\', '/')) return false;
  return true;
}

function candidateFromPackage(projectRoot, directory, loaded, script) {
  const scripts = loaded.pkg.scripts;
  const framework = frameworkForPackage(loaded.pkg);
  const port = scriptPort(scripts[script], defaultFrameworkPort(framework));
  const manager = packageManagerFor(loaded.pkg, directory, projectRoot);
  const invocation = packageInvocation(manager, script);
  const directoryName = path.relative(projectRoot, directory).split(path.sep).filter(Boolean).join('/');
  return {
    cwd: directory,
    directory: directoryName || undefined,
    packagePath: loaded.packagePath,
    packageName: typeof loaded.pkg.name === 'string'
      ? loaded.pkg.name.slice(0, 256)
      : path.basename(directory).slice(0, 256),
    framework,
    manager,
    script,
    scriptText: scripts[script],
    port,
    ...invocation,
  };
}

async function resolveExplicitDirectory(projectRoot, action) {
  const root = path.resolve(projectRoot);
  const rootReal = await fsp.realpath(root).catch(() => {
    throw actionError('action_unavailable', 'The project directory does not exist');
  });
  const requestedDirectory = normalizeDirectory(action.directory, 'action.directory');
  if (!requestedDirectory) {
    throw actionError('invalid_path', 'An explicit server directory is required');
  }
  const lexicalDirectory = resolveProjectPath(root, requestedDirectory);
  let directory;
  try {
    directory = await fsp.realpath(lexicalDirectory);
  } catch {
    throw actionError('action_unavailable', 'The selected project directory does not exist');
  }
  if (directory !== rootReal && !directory.startsWith(`${rootReal}${path.sep}`)) {
    throw actionError('invalid_path', 'Action directory escapes the project');
  }
  let stats;
  try {
    stats = await fsp.stat(directory);
  } catch {
    throw actionError('action_unavailable', 'The selected project directory does not exist');
  }
  if (!stats.isDirectory()) {
    throw actionError('invalid_path', 'The selected action path is not a directory');
  }
  const loaded = await readPackage(directory);
  if (!loaded) {
    throw actionError('action_unavailable', 'The selected project directory has no valid package.json');
  }
  let packagePath;
  try {
    packagePath = await fsp.realpath(loaded.packagePath);
  } catch {
    throw actionError('action_unavailable', 'The selected project package.json is unavailable');
  }
  if (packagePath !== rootReal && !packagePath.startsWith(`${rootReal}${path.sep}`)) {
    throw actionError('invalid_path', 'The selected package escapes the project');
  }
  const requestedScript = validateScriptName(action.script, 'action.script');
  const script = requestedScript || recognizedScriptNames(loaded.pkg.scripts)[0];
  if (!script || typeof loaded.pkg.scripts[script] !== 'string' || !loaded.pkg.scripts[script].trim()) {
    throw actionError('action_unavailable', requestedScript
      ? 'The selected development script is not present in the project'
      : 'No recognized project development script was found in the selected directory');
  }
  return candidateFromPackage(rootReal, directory, { ...loaded, packagePath }, script);
}

async function resolveCandidate(projectRoot, action, options = {}) {
  if (action?.directory !== undefined) {
    return resolveExplicitDirectory(projectRoot, action);
  }
  const candidates = await detectServerCandidates(projectRoot, options);
  const matches = candidates.filter((candidate) => candidateMatchesAction(candidate, action));
  if (matches.length === 1) return matches[0];
  if (matches.length > 1) throw actionError('ambiguous_action', 'More than one project development server matches this action');
  if (action.script || action.directory) {
    throw actionError('action_unavailable', 'The selected development script is not present in the project');
  }
  if (candidates.length === 1) return candidates[0];
  if (candidates.length > 1) throw actionError('ambiguous_action', 'Choose which project development server to start');
  throw actionError('action_unavailable', 'No recognized project development script was found');
}

function wait(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function probeUrl(rawUrl, timeoutMs = DEFAULT_PROBE_TIMEOUT_MS) {
  const url = normalizeLoopbackUrl(rawUrl);
  if (!url) return Promise.resolve(false);
  const parsed = new URL(url);
  const client = parsed.protocol === 'https:' ? https : http;
  return new Promise((resolve) => {
    let settled = false;
    const finish = (value) => {
      if (settled) return;
      settled = true;
      resolve(Boolean(value));
    };
    const request = client.request({
      protocol: parsed.protocol,
      hostname: '127.0.0.1',
      port: parsed.port || (parsed.protocol === 'https:' ? 443 : 80),
      path: `${parsed.pathname || '/'}${parsed.search || ''}`,
      method: 'GET',
      timeout: timeoutMs,
      rejectUnauthorized: false,
      headers: { connection: 'close', 'user-agent': 'djinn-action-probe' },
    }, (response) => {
      response.resume();
      response.once('end', () => finish(true));
      response.once('error', () => finish(false));
    });
    request.once('timeout', () => {
      request.destroy();
      finish(false);
    });
    request.once('error', () => finish(false));
    request.end();
  });
}

function clampTimeout(value, fallback) {
  if (!Number.isFinite(value)) return fallback;
  return Math.min(Math.max(Math.floor(value), 100), MAX_STARTUP_TIMEOUT_MS);
}

function killProcessGroup(child, force = false) {
  if (!child?.pid || child.pid === process.pid) return false;
  if (process.platform === 'win32') {
    if (!force) {
      try {
        child.kill('SIGTERM');
      } catch {
        return false;
      }
      return true;
    }
    try {
      const killer = spawn('taskkill', ['/pid', String(child.pid), '/T', '/F'], {
        shell: false,
        windowsHide: true,
        stdio: 'ignore',
      });
      killer.once('error', () => undefined);
      return true;
    } catch {
      return false;
    }
  }
  const signal = force ? 'SIGKILL' : 'SIGTERM';
  try {
    process.kill(-child.pid, signal);
    return true;
  } catch {
    try {
      child.kill(signal);
      return true;
    } catch {
      return false;
    }
  }
}

function safeActionError(error) {
  const message = error instanceof Error ? error.message : String(error);
  return message.slice(0, MAX_ACTION_ERROR_LENGTH);
}

function actionErrorWithLog(record, message) {
  const clean = String(record?.log || '')
    .replace(/\u001b\[[0-?]*[ -\/]*[@-~]/g, '')
    .trim()
    .slice(-1_000);
  if (!clean) return String(message).slice(0, MAX_ACTION_ERROR_LENGTH);
  return `${String(message)}: ${clean}`.slice(0, MAX_ACTION_ERROR_LENGTH);
}

class ActionRegistry {
  constructor(options = {}) {
    this.onUpdate = typeof options.onUpdate === 'function' ? options.onUpdate : () => undefined;
    this.openExternal = typeof options.openExternal === 'function' ? options.openExternal : null;
    this.spawn = typeof options.spawn === 'function' ? options.spawn : spawn;
    this.probe = typeof options.probe === 'function' ? options.probe : probeUrl;
    this.environment = actionEnvironment(options.environment || process.env);
    this.startupTimeoutMs = clampTimeout(options.startupTimeoutMs, DEFAULT_STARTUP_TIMEOUT_MS);
    this.probeTimeoutMs = clampTimeout(options.probeTimeoutMs, DEFAULT_PROBE_TIMEOUT_MS);
    this.stopGraceMs = Math.min(Math.max(Number(options.stopGraceMs) || STOP_GRACE_MS, 100), 10_000);
    this.tasks = new Map();
    this.serverByKey = new Map();
  }

  _taskMap(taskId) {
    const key = validateTaskId(taskId);
    let map = this.tasks.get(key);
    if (!map) {
      map = new Map();
      this.tasks.set(key, map);
    }
    return map;
  }

  _emit(record, meta = {}) {
    const action = clone(record.action);
    delete action.taskId;
    try {
      this.onUpdate(action, record.taskId, meta);
    } catch {
      // Renderer delivery must never break process supervision.
    }
    return action;
  }

  _update(record, patch, meta = {}) {
    if (!record) return null;
    record.action = {
      ...record.action,
      ...patch,
      updatedAt: actionNow(),
    };
    const result = this._emit(record, meta);
    for (const linked of record.links || []) {
      linked.action = {
        ...linked.action,
        ...patch,
        updatedAt: record.action.updatedAt,
      };
      this._emit(linked, meta);
    }
    return result;
  }

  register(taskId, proposal, options = {}) {
    const normalizedTaskId = validateTaskId(taskId);
    const map = this._taskMap(normalizedTaskId);
    const candidate = validateActionProposal(proposal);
    const id = candidate.id;
    if (id && map.has(id)) return clone(map.get(id).action);
    if (map.size >= MAX_ACTIONS_PER_TASK) throw actionError('too_many_actions', 'A task has too many actions');
    const action = createTaskAction(candidate, normalizedTaskId);
    const projectRoot = options.projectRoot ? path.resolve(boundedString(options.projectRoot, 'cwd', 4_096, { allowEmpty: false })) : null;
    const directory = action.directory;
    const cwd = projectRoot ? resolveProjectPath(projectRoot, directory) : null;
    const record = {
      taskId: normalizedTaskId,
      action,
      projectRoot,
      cwd,
      process: null,
      candidate: null,
      managed: false,
      external: false,
      shared: null,
      log: '',
      stopRequested: false,
      startupTimer: null,
      startupPromise: null,
      starting: false,
      links: new Set(),
      runId: options.runId || null,
    };
    map.set(action.id, record);
    this._emit(record, { runId: options.runId || null });
    return clone(action);
  }

  getActions(taskId) {
    const normalizedTaskId = validateTaskId(taskId);
    const map = this.tasks.get(normalizedTaskId);
    if (!map) return [];
    return [...map.values()].map((record) => clone(record.action));
  }

  _lookup(taskId, action) {
    const normalizedTaskId = validateTaskId(taskId);
    if (!isRecord(action)) throw actionError('invalid_action', 'Action must be an object');
    const id = boundedIdentifier(action.id, 'action.id');
    const record = this.tasks.get(normalizedTaskId)?.get(id);
    if (!record) throw actionError('action_not_found', 'The requested action is no longer registered');
    return record;
  }

  _setProjectRoot(record, rawCwd) {
    const cwd = boundedString(rawCwd, 'cwd', 4_096, { allowEmpty: false });
    if (!path.isAbsolute(cwd)) throw actionError('invalid_path', 'cwd must be an absolute path');
    const root = path.resolve(cwd);
    if (record.projectRoot && path.resolve(record.projectRoot) !== root) {
      throw actionError('invalid_path', 'Action cwd does not match its registered project');
    }
    record.projectRoot = record.projectRoot || root;
    record.cwd = resolveProjectPath(record.projectRoot, record.action.directory);
    return record.projectRoot;
  }

  async perform(input) {
    if (!isRecord(input)) throw actionError('invalid_action', 'Action operation input must be an object');
    const taskId = validateTaskId(input.taskId);
    const operation = input.operation;
    if (!['run', 'stop', 'complete', 'open'].includes(operation)) {
      throw actionError('invalid_action', `Unsupported action operation: ${String(operation)}`);
    }
    let record;
    try {
      record = this._lookup(taskId, input.action);
    } catch (error) {
      // A renderer may be restoring a saved link/manual action after an app
      // restart. Re-register only the bounded action vocabulary supplied by
      // the renderer; server execution still resolves package.json below.
      if (error?.code !== 'action_not_found') throw error;
      const restored = input.action;
      const proposal = {
        id: restored.id,
        kind: restored.kind,
        title: restored.title,
        ...(restored.detail !== undefined ? { detail: restored.detail } : {}),
        ...(restored.agentId !== undefined ? { agentId: restored.agentId } : {}),
        ...(restored.workItemId !== undefined ? { workItemId: restored.workItemId } : {}),
        ...(restored.target !== undefined ? { target: restored.target } : {}),
        ...(restored.directory !== undefined ? { directory: restored.directory } : {}),
        ...(restored.url !== undefined ? { url: restored.url } : {}),
        ...(restored.script !== undefined ? { script: restored.script } : {}),
        ...(restored.testInstructions !== undefined ? { testInstructions: restored.testInstructions } : {}),
        ...(restored.expectedResult !== undefined ? { expectedResult: restored.expectedResult } : {}),
      };
      this.register(taskId, proposal, { projectRoot: input.cwd, runId: input.runId || null });
      record = this._lookup(taskId, input.action);
    }
    this._setProjectRoot(record, input.cwd);
    if (record.action.kind === 'server') {
      if (operation === 'run') return this._start(record, { runId: input.runId || null });
      if (operation === 'stop') return this._stop(record, { runId: input.runId || null });
      if (operation === 'complete') {
        if (record.process && !record.stopRequested) await this._stop(record, { runId: input.runId || null });
        return this._update(record, { status: 'done', error: undefined }, { runId: input.runId || null });
      }
      if (operation === 'open') {
        return this._open(record, { runId: input.runId || null });
      }
    }
    if (record.action.kind === 'link') {
      if (operation === 'open') return this._open(record, { runId: input.runId || null });
      if (operation === 'complete') return this._update(record, { status: 'done', error: undefined }, { runId: input.runId || null });
      throw actionError('action_manual', 'Link actions require an explicit open or complete operation');
    }
    if (operation === 'complete') return this._update(record, { status: 'done', error: undefined }, { runId: input.runId || null });
    throw actionError('action_manual', 'This action requires a manual user step');
  }

  async performAction(input) {
    return this.perform(input);
  }

  async _open(record, meta = {}) {
    if (!record.action.url) throw actionError('invalid_action', 'This action has no URL to open');
    const loopbackUrl = normalizeLoopbackUrl(record.action.url);
    if (loopbackUrl && !(await this.probe(loopbackUrl, this.probeTimeoutMs))) {
      throw actionError('server_unavailable', 'The server URL did not respond');
    }
    if (!this.openExternal) {
      throw actionError('open_unavailable', 'External links are unavailable in this runtime');
    }
    await this.openExternal(validateHttpUrl(record.action.url));
    return this._update(record, {
      status: record.action.kind === 'server' ? 'ready' : 'done',
      error: undefined,
    }, meta);
  }

  _serverKey(candidate) {
    return `${path.resolve(candidate.cwd)}::${candidate.manager}::${candidate.script}`;
  }

  async _start(record, meta = {}) {
    if (record.action.status === 'ready' && (record.external || record.process)) {
      if (record.action.url && (await this.probe(record.action.url, this.probeTimeoutMs))) return clone(record.action);
      if (record.external) return this._update(record, { status: 'error', error: 'The existing server stopped responding' }, meta);
      if (record.process) await this._stop(record, meta);
    }
    if (record.action.status === 'running' && record.process) return clone(record.action);
    if (record.startupPromise) return record.startupPromise;
    record.startupPromise = this._startImpl(record, meta).finally(() => {
      record.startupPromise = null;
    });
    return record.startupPromise;
  }

  async _startImpl(record, meta) {
    const projectRoot = record.projectRoot;
    if (!projectRoot) throw actionError('invalid_path', 'A project directory is required');
    let directoryStats;
    try {
      directoryStats = await fsp.stat(record.cwd || projectRoot);
    } catch {
      this._update(record, { status: 'error', error: 'The action project directory does not exist' }, meta);
      return clone(record.action);
    }
    if (!directoryStats.isDirectory()) {
      this._update(record, { status: 'error', error: 'The action project path is not a directory' }, meta);
      return clone(record.action);
    }
    let candidate;
    try {
      candidate = await resolveCandidate(projectRoot, record.action);
    } catch (error) {
      this._update(record, { status: 'error', error: safeActionError(error) }, meta);
      return clone(record.action);
    }
    record.candidate = candidate;
    record.cwd = candidate.cwd;
    const key = this._serverKey(candidate);
    const existing = this.serverByKey.get(key);
    if (existing && existing !== record) {
      if (existing.process || existing.external || existing.action.status === 'ready' || existing.action.status === 'running') {
        record.shared = existing;
        existing.links.add(record);
        record.managed = false;
        record.external = existing.external;
        this._update(record, {
          status: existing.action.status,
          ...(existing.action.url ? { url: existing.action.url } : {}),
          ...(existing.action.error ? { error: existing.action.error } : {}),
        }, meta);
        return clone(record.action);
      }
      this.serverByKey.delete(key);
    }
    // A cold registry cannot claim a random process listening on a framework's
    // conventional port. Reuse is limited to a URL that was already attached
    // to this native action (for example after renderer reconciliation).
    const explicitUrl = record.action.url && normalizeLoopbackUrl(record.action.url);
    const preexistingFallbackPort = !explicitUrl
      && (await this.probe(`http://127.0.0.1:${candidate.port}/`, this.probeTimeoutMs));
    const existingUrl = explicitUrl
      ? (await this.probe(explicitUrl, this.probeTimeoutMs) ? explicitUrl : null)
      : null;
    if (existingUrl) {
      record.external = true;
      record.managed = false;
      this.serverByKey.set(key, record);
      return this._update(record, {
        status: 'ready',
        url: existingUrl,
        detail: `${record.action.detail || 'Un serveur de développement'} répond déjà à ${existingUrl}.`,
        error: undefined,
      }, meta);
    }
    // A restarted server may choose a different port. Only its new output can
    // establish readiness; a previous run's URL must not mask that output.
    record.log = '';
    record.readyUrl = undefined;
    this._update(record, { status: 'running', url: undefined, error: undefined, detail: record.action.detail || `Démarrage de ${candidate.manager} ${candidate.script}…` }, meta);
    let child;
    try {
      const env = {
        ...this.environment,
        HOST: '127.0.0.1',
        HOSTNAME: '127.0.0.1',
        VITE_HOST: '127.0.0.1',
        BROWSER: 'none',
      };
      child = this.spawn(candidate.command, candidate.args.slice(), {
        cwd: candidate.cwd,
        shell: false,
        detached: process.platform !== 'win32',
        windowsHide: true,
        stdio: ['ignore', 'pipe', 'pipe'],
        env,
      });
    } catch (error) {
      const message = error?.code === 'ENOENT'
        ? `${candidate.manager} is unavailable on PATH; install it separately, then retry.`
        : safeActionError(error);
      this._update(record, { status: 'error', error: message }, meta);
      return clone(record.action);
    }
    record.process = child;
    record.managed = true;
    record.preexistingFallbackPort = preexistingFallbackPort;
    record.stopRequested = false;
    record.starting = true;
    this.serverByKey.set(key, record);
    const append = (chunk) => {
      const text = String(chunk ?? '');
      record.log = `${record.log}${text}`.slice(-MAX_LOG_LENGTH);
    };
    child.stdout?.setEncoding?.('utf8');
    child.stderr?.setEncoding?.('utf8');
    child.stdout?.on?.('data', append);
    child.stderr?.on?.('data', append);
    let closed = false;
    const closedPromise = new Promise((resolve) => {
      const onError = (error) => {
        if (closed) return;
        closed = true;
        resolve({ error });
      };
      const onClose = (code, signal) => {
        if (closed) return;
        closed = true;
        resolve({ code, signal });
      };
      child.once?.('error', onError);
      child.once?.('close', onClose);
    });
    // Keep a live registry entry after startup resolves. A dev server that
    // exits later must become retryable/error instead of staying falsely
    // marked ready until the next renderer reload.
    child.on?.('close', (code, signal) => {
      if (record.starting || record.stopRequested || record.process !== child) return;
      record.process = null;
      this._removeServerKey(record, key);
      this._update(record, {
        status: 'error',
        error: `Server exited with code ${code ?? 'unknown'}${signal ? ` (${signal})` : ''}`,
      }, meta);
    });
    child.on?.('error', (error) => {
      if (record.starting || record.stopRequested || record.process !== child) return;
      record.process = null;
      this._removeServerKey(record, key);
      this._update(record, { status: 'error', error: safeActionError(error) }, meta);
    });
    const timeout = clampTimeout(this.startupTimeoutMs, DEFAULT_STARTUP_TIMEOUT_MS);
    const startedAt = Date.now();
    const startup = (async () => {
      while (Date.now() - startedAt < timeout) {
        const outputUrl = extractLoopbackUrl(record.log);
        const portUrl = `http://127.0.0.1:${candidate.port}/`;
        const probeCandidates = outputUrl
          ? [outputUrl, ...(record.preexistingFallbackPort ? [] : [portUrl])]
          : record.preexistingFallbackPort ? [] : [portUrl];
        for (const url of probeCandidates) {
          if (await this.probe(url, this.probeTimeoutMs)) {
            const ready = this._update(record, {
              status: 'ready',
              url,
              error: undefined,
              detail: `${record.action.detail || 'Serveur de développement'} prêt à ${url}.`,
            }, meta);
            record.readyUrl = url;
            return ready;
          }
        }
        const result = await Promise.race([closedPromise, wait(200).then(() => null)]);
        if (result && (result.error || result.code !== undefined || result.signal !== undefined)) {
          const error = result.error
            ? result.error.code === 'ENOENT'
              ? `${candidate.manager} is unavailable on PATH; install it separately, then retry.`
              : safeActionError(result.error)
            : `Server exited with code ${result.code ?? 'unknown'}`;
          this._removeServerKey(record, key);
          this._update(record, {
            status: record.stopRequested ? 'stopped' : 'error',
            error: record.stopRequested ? undefined : actionErrorWithLog(record, error),
          }, meta);
          return clone(record.action);
        }
      }
      this._removeServerKey(record, key);
      killProcessGroup(child);
      await wait(Math.min(this.stopGraceMs, 500));
      if (!closed) killProcessGroup(child, true);
      this._update(record, {
        status: 'error',
        error: actionErrorWithLog(record, `Server did not respond within ${timeout}ms`),
      }, meta);
      return clone(record.action);
    })().finally(() => {
      record.starting = false;
    });
    record.startupTimer = startup;
    return startup;
  }

  _removeServerKey(record, key) {
    if (this.serverByKey.get(key) === record) this.serverByKey.delete(key);
  }

  async _stop(record, meta = {}) {
    if (record.shared) {
      record.shared.links.delete(record);
      record.shared = null;
      return this._update(record, { status: 'stopped', error: undefined }, meta);
    }
    if (!record.process) {
      return this._update(record, { status: 'stopped', error: undefined }, meta);
    }
    record.stopRequested = true;
    const child = record.process;
    killProcessGroup(child);
    await wait(this.stopGraceMs);
    if (record.process === child && child.exitCode === null && child.signalCode === null) killProcessGroup(child, true);
    record.process = null;
    if (record.candidate) this._removeServerKey(record, this._serverKey(record.candidate));
    return this._update(record, { status: 'stopped', error: undefined }, meta);
  }

  async maybeAutoStart(taskId, projectRoot, context = {}) {
    const normalizedTaskId = validateTaskId(taskId);
    if (
      context.mode !== 'execute' ||
      context.runKind !== 'lead' ||
      context.status !== 'completed' ||
      context.hasBlockingQuestion ||
      context.cancelRequested ||
      (Array.isArray(context.workerStatuses) && context.workerStatuses.some((status) => !['completed', 'done'].includes(status)))
    ) {
      return { skipped: true, reason: 'run_not_successful_execute_lead' };
    }
    let root;
    try {
      root = path.resolve(boundedString(projectRoot, 'cwd', 4_096, { allowEmpty: false }));
      const stats = await fsp.stat(root);
      if (!stats.isDirectory()) return { skipped: true, reason: 'invalid_project' };
    } catch {
      return { skipped: true, reason: 'invalid_project' };
    }
    let candidates;
    try {
      candidates = await detectServerCandidates(root);
    } catch {
      return { skipped: true, reason: 'project_scan_failed' };
    }
    if (candidates.length === 0) return { skipped: true, reason: 'no_server_script' };
    const map = this._taskMap(normalizedTaskId);
    const serverRecords = [...map.values()].filter((record) => record.action.kind === 'server');
    const matching = (candidate) => serverRecords.find(
      (record) => record.action.script === candidate.script && (record.action.directory || '') === (candidate.directory || ''),
    );
    if (candidates.length > 1) {
      const pending = [];
      for (const candidate of candidates) {
        const existing = matching(candidate);
        if (existing) {
          pending.push(clone(existing.action));
          continue;
        }
        pending.push(this.register(normalizedTaskId, {
          kind: 'server',
          title: `Démarrer ${candidate.packageName}`,
          detail: `Choisissez cette application pour lancer ${candidate.manager} ${candidate.script}.`,
          script: candidate.script,
          ...(candidate.directory ? { directory: candidate.directory } : {}),
        }, { projectRoot: root, runId: context.runId || null }));
      }
      return { ambiguous: true, actions: pending };
    }
    const candidate = candidates[0];
    let record = matching(candidate);
    if (!record) {
      const action = this.register(normalizedTaskId, {
        kind: 'server',
        title: `Aperçu de ${candidate.packageName}`,
        detail: `Démarrage de ${candidate.manager} ${candidate.script}.`,
        script: candidate.script,
        ...(candidate.directory ? { directory: candidate.directory } : {}),
      }, { projectRoot: root, runId: context.runId || null });
      record = this._lookup(normalizedTaskId, action);
    }
    const action = await this._start(record, { runId: context.runId || null });
    return { started: true, action };
  }

  async stopAll() {
    const records = [];
    for (const map of this.tasks.values()) {
      for (const record of map.values()) {
        if (record.process && !record.stopRequested) records.push(record);
      }
    }
    await Promise.all(records.map((record) => this._stop(record).catch(() => undefined)));
  }

  killAllImmediately() {
    for (const map of this.tasks.values()) {
      for (const record of map.values()) {
        if (record.process && !record.stopRequested) {
          record.stopRequested = true;
          killProcessGroup(record.process);
        }
      }
    }
  }
}

function createActionRegistry(options) {
  return new ActionRegistry(options);
}

module.exports = {
  ActionError,
  ACTION_KINDS,
  ACTION_STATUSES,
  MAX_ACTIONS_PER_TASK,
  MAX_ACTION_TITLE_LENGTH,
  MAX_ACTION_DETAIL_LENGTH,
  MAX_ACTION_URL_LENGTH,
  DEFAULT_STARTUP_TIMEOUT_MS,
  MAX_STARTUP_TIMEOUT_MS,
  RECOGNIZED_SCRIPT,
  validateActionProposal,
  createTaskAction,
  normalizeLoopbackUrl,
  extractLoopbackUrl,
  extractPort: portFromUrl,
  frameworkForPackage,
  defaultFrameworkPort,
  detectServerCandidates,
  resolveCandidate,
  actionEnvironment,
  probeUrl,
  killProcessGroup,
  ActionRegistry,
  createActionRegistry,
};
