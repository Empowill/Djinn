'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const fsp = fs.promises;
const os = require('node:os');
const path = require('node:path');
const { spawn } = require('node:child_process');

const actions = require('../electron/actions.cjs');

async function makeProject({ script = 'node server.cjs', nested = false, dependencies } = {}) {
  const root = await fsp.mkdtemp(path.join(os.tmpdir(), 'djinn-actions-'));
  const directory = nested ? path.join(root, 'apps', 'web') : root;
  await fsp.mkdir(directory, { recursive: true });
  await fsp.writeFile(path.join(directory, 'package.json'), JSON.stringify({
    name: nested ? 'nested-fixture' : 'fixture',
    scripts: { dev: script },
    ...(dependencies ? { dependencies } : {}),
  }));
  return { root, directory };
}

async function writeServer(project, body) {
  await fsp.writeFile(path.join(project.directory, 'server.cjs'), body);
}

async function removeProject(root) {
  await fsp.rm(root, { recursive: true, force: true });
}

function waitForChild(child) {
  if (child.exitCode !== null || child.signalCode !== null) return Promise.resolve();
  return new Promise((resolve) => child.once('close', resolve));
}

let loopbackSupport;
async function supportsLoopback() {
  if (loopbackSupport !== undefined) return loopbackSupport;
  const child = spawn(process.execPath, ['-e', "const s=require('node:http').createServer((_q,r)=>r.end());s.listen(0,'127.0.0.1',()=>s.close());"], {
    stdio: 'ignore',
  });
  loopbackSupport = await new Promise((resolve) => {
    child.once('close', (code) => resolve(code === 0));
    child.once('error', () => resolve(false));
  });
  return loopbackSupport;
}

test('validates action vocabulary and never accepts raw commands', () => {
  assert.deepEqual(actions.validateActionProposal({
    kind: 'server',
    title: 'Run the app',
    script: 'dev',
    directory: 'apps/web',
  }), {
    kind: 'server',
    title: 'Run the app',
    script: 'dev',
    directory: 'apps/web',
  });
  assert.throws(() => actions.validateActionProposal({
    kind: 'server',
    title: 'Danger',
    command: 'rm -rf /',
  }), /command/);
  assert.throws(() => actions.validateActionProposal({
    kind: 'server',
    title: 'Danger',
    script: 'node server.js',
  }), /script/);
  assert.throws(() => actions.validateActionProposal({
    kind: 'link',
    title: 'Bad link',
    url: 'https://user:password@example.com',
  }), /url/i);
});

test('preserves bounded human test guidance while rejecting provider-certified results', () => {
  const proposal = actions.validateActionProposal({
    kind: 'manual',
    title: 'Check the result',
    testInstructions: ['Open the preview', 'Verify the empty state'],
    expectedResult: 'The empty state is visible.',
  });
  assert.deepEqual(proposal.testInstructions, [
    'Open the preview',
    'Verify the empty state',
  ]);
  assert.equal(proposal.expectedResult, 'The empty state is visible.');
  const action = actions.createTaskAction(proposal, 'test-task');
  assert.deepEqual(action.testInstructions, proposal.testInstructions);
  assert.equal(action.expectedResult, proposal.expectedResult);
  assert.throws(() => actions.validateActionProposal({
    kind: 'manual',
    title: 'Certify output',
    testResult: { passed: true },
  }), /testResult/);
});

test('scans only bounded project packages and exposes nested candidates', async () => {
  const project = await makeProject({ nested: true });
  try {
    const candidates = await actions.detectServerCandidates(project.root);
    assert.equal(candidates.length, 1);
    assert.equal(candidates[0].directory, 'apps/web');
    assert.equal(candidates[0].script, 'dev');
    assert.equal(candidates[0].command, process.platform === 'win32' ? 'npm.cmd' : 'npm');
  } finally {
    await removeProject(project.root);
  }
});

test('resolves an explicitly selected hidden deep worktree package without discovery', async () => {
  const root = await fsp.mkdtemp(path.join(os.tmpdir(), 'djinn-actions-explicit-'));
  const directory = path.join(root, '.worktrees', 'ET-2304', 'app', 'apps', 'console');
  try {
    await fsp.mkdir(directory, { recursive: true });
    await fsp.writeFile(path.join(directory, 'package.json'), JSON.stringify({
      name: 'console',
      scripts: { dev: 'node server.cjs' },
    }));
    const candidate = await actions.resolveCandidate(root, {
      directory: '.worktrees/ET-2304/app/apps/console',
      script: 'dev',
    });
    assert.equal(candidate.cwd, await fsp.realpath(directory));
    assert.equal(candidate.directory, '.worktrees/ET-2304/app/apps/console');
    assert.equal(candidate.script, 'dev');
  } finally {
    await removeProject(root);
  }
});

test('explicit action directories reject missing scripts, traversal, and symlink escapes', async () => {
  const root = await fsp.mkdtemp(path.join(os.tmpdir(), 'djinn-actions-paths-'));
  const outside = await fsp.mkdtemp(path.join(os.tmpdir(), 'djinn-actions-outside-'));
  const directory = path.join(root, '.worktrees', 'deep', 'app');
  try {
    await fsp.mkdir(directory, { recursive: true });
    await fsp.writeFile(path.join(directory, 'package.json'), JSON.stringify({
      scripts: { dev: 'node server.cjs' },
    }));
    await fsp.writeFile(path.join(outside, 'package.json'), JSON.stringify({
      scripts: { dev: 'node server.cjs' },
    }));
    await fsp.symlink(outside, path.join(root, 'escape'), 'dir');

    await assert.rejects(
      actions.resolveCandidate(root, {
        directory: '.worktrees/deep/app',
        script: 'start',
      }),
      (error) => error.code === 'action_unavailable',
    );
    await assert.rejects(
      actions.resolveCandidate(root, { directory: '../outside', script: 'dev' }),
      (error) => error.code === 'invalid_path',
    );
    await assert.rejects(
      actions.resolveCandidate(root, { directory: 'escape', script: 'dev' }),
      (error) => error.code === 'invalid_path',
    );
    await assert.rejects(
      actions.resolveCandidate(root, { directory: '.worktrees/deep/app', script: 'node server.cjs' }),
      (error) => error.code === 'invalid_action',
    );
  } finally {
    await removeProject(root);
    await removeProject(outside);
  }
});

test('does not auto-start during plan and leaves ambiguous execute projects pending', async () => {
  const project = await makeProject();
  const nested = await makeProject();
  try {
    await fsp.mkdir(path.join(project.root, 'apps', 'one'), { recursive: true });
    await fsp.copyFile(path.join(nested.directory, 'package.json'), path.join(project.root, 'apps', 'one', 'package.json'));
    const registry = new actions.ActionRegistry({ startupTimeoutMs: 500, probeTimeoutMs: 100 });
    const skipped = await registry.maybeAutoStart('plan-task', project.root, {
      mode: 'plan', runKind: 'lead', status: 'completed', workerStatuses: [],
    });
    assert.equal(skipped.skipped, true);
    assert.deepEqual(registry.getActions('plan-task'), []);

    const ambiguous = await registry.maybeAutoStart('execute-task', project.root, {
      mode: 'execute', runKind: 'lead', status: 'completed', workerStatuses: [],
    });
    assert.equal(ambiguous.ambiguous, true);
    assert.equal(ambiguous.actions.length, 2);
    assert.ok(ambiguous.actions.every((action) => action.status === 'pending'));
  } finally {
    await removeProject(project.root);
    await removeProject(nested.root);
  }
});

test('starts a real package server, waits for loopback readiness, and stops only its process group', async (t) => {
  if (!(await supportsLoopback())) return t.skip('loopback sockets are unavailable in this sandbox');
  const project = await makeProject({ script: 'PORT=45739 node server.cjs' });
  await writeServer(project, `
    const http = require('node:http');
    const server = http.createServer((_req, res) => { res.end('ok'); });
    server.listen(Number(process.env.PORT || 3000), '127.0.0.1', () => {
      process.stdout.write('ready at http://localhost:' + server.address().port + '/\\n');
    });
  `);
  try {
    const registry = new actions.ActionRegistry({ startupTimeoutMs: 5_000, probeTimeoutMs: 250, stopGraceMs: 100 });
    const result = await registry.maybeAutoStart('run-task', project.root, {
      mode: 'execute', runKind: 'lead', status: 'completed', workerStatuses: [], runId: 'lead-1',
    });
    assert.equal(result.started, true);
    assert.equal(result.action.status, 'ready');
    assert.match(result.action.url, /^http:\/\/127\.0\.0\.1:45739\//);
    assert.equal(registry.getActions('run-task')[0].status, 'ready');

    const stopped = await registry.perform({
      taskId: 'run-task', cwd: project.root, action: result.action, operation: 'stop',
    });
    assert.equal(stopped.status, 'stopped');
    assert.equal(await registry.probe(result.action.url, 250), false);
  } finally {
    await removeProject(project.root);
  }
});

test('rediscovers the actual port after restarting a managed server', async (t) => {
  if (!(await supportsLoopback())) return t.skip('loopback sockets are unavailable in this sandbox');
  const project = await makeProject();
  const registry = new actions.ActionRegistry({ startupTimeoutMs: 5_000, probeTimeoutMs: 250, stopGraceMs: 100 });
  await writeServer(project, `
    const server = require('node:http').createServer((_q, r) => r.end('restart-ok'));
    server.listen(0, '127.0.0.1', () => console.log('http://127.0.0.1:' + server.address().port + '/'));
  `);
  try {
    const proposal = registry.register('restart-task', { kind: 'server', title: 'Preview', script: 'dev' }, { projectRoot: project.root });
    const ready = await registry.perform({ taskId: 'restart-task', cwd: project.root, action: proposal, operation: 'run' });
    assert.equal(ready.status, 'ready');
    const stopped = await registry.perform({ taskId: 'restart-task', cwd: project.root, action: ready, operation: 'stop' });
    assert.equal(await registry.probe(ready.url, 250), false);
    const restarted = await registry.perform({ taskId: 'restart-task', cwd: project.root, action: stopped, operation: 'run' });
    assert.equal(restarted.status, 'ready', restarted.error);
    assert.equal(await registry.probe(restarted.url, 250), true);
    await registry.perform({ taskId: 'restart-task', cwd: project.root, action: restarted, operation: 'stop' });
  } finally {
    registry.killAllImmediately();
    await removeProject(project.root);
  }
});

test('reuses an explicitly selected responding server without taking ownership', async (t) => {
  if (!(await supportsLoopback())) return t.skip('loopback sockets are unavailable in this sandbox');
  const project = await makeProject({ script: 'PORT=45740 node server.cjs' });
  const server = spawn(process.execPath, ['-e', [
    "require('node:http').createServer((_req,res)=>res.end('external')).listen(45740,'127.0.0.1')",
  ].join('')], { stdio: 'ignore' });
  try {
    await new Promise((resolve) => setTimeout(resolve, 150));
    const registry = new actions.ActionRegistry({ startupTimeoutMs: 1_000, probeTimeoutMs: 250, stopGraceMs: 100 });
    const proposed = registry.register('external-task', {
      kind: 'server',
      title: 'Reuse the selected preview',
      script: 'dev',
      url: 'http://127.0.0.1:45740/',
    }, { projectRoot: project.root });
    const result = await registry.perform({
      taskId: 'external-task', cwd: project.root, action: proposed, operation: 'run',
    });
    assert.equal(result.status, 'ready');
    assert.equal(result.url, 'http://127.0.0.1:45740/');
    const stopped = await registry.perform({ taskId: 'external-task', cwd: project.root, action: result, operation: 'stop' });
    assert.equal(stopped.status, 'stopped');
    assert.equal(await registry.probe(result.url, 250), true);
  } finally {
    server.kill('SIGTERM');
    await waitForChild(server);
    await removeProject(project.root);
  }
});

test('opens links once and keeps ready server actions stoppable after opening', async () => {
  const project = await makeProject({ script: 'PORT=45741 node server.cjs' });
  const opened = [];
  const registry = new actions.ActionRegistry({
    probe: async () => true,
    openExternal: async (url) => opened.push(url),
    stopGraceMs: 100,
  });
  try {
    const link = registry.register('open-task', {
      kind: 'link', title: 'Read the guide', url: 'https://example.com/guide',
    }, { projectRoot: project.root });
    const openedLink = await registry.perform({
      taskId: 'open-task', cwd: project.root, action: link, operation: 'open',
    });
    assert.equal(openedLink.status, 'done');
    assert.deepEqual(opened, ['https://example.com/guide']);

    await writeServer(project, "require('node:http').createServer((_q,r)=>r.end('ok')).listen(45741,'127.0.0.1')");
    const serverAction = registry.register('open-task', {
      kind: 'server', title: 'Open preview', script: 'dev', url: 'http://127.0.0.1:45741/',
    }, { projectRoot: project.root });
    const ready = await registry.perform({
      taskId: 'open-task', cwd: project.root, action: serverAction, operation: 'run',
    });
    assert.equal(ready.status, 'ready');
    const openedServer = await registry.perform({
      taskId: 'open-task', cwd: project.root, action: ready, operation: 'open',
    });
    assert.equal(openedServer.status, 'ready');
    const stopped = await registry.perform({
      taskId: 'open-task', cwd: project.root, action: openedServer, operation: 'stop',
    });
    assert.equal(stopped.status, 'stopped');
  } finally {
    await removeProject(project.root);
  }
});

test('re-registers saved link and manual actions after a native restart', async () => {
  const project = await makeProject();
  const opened = [];
  try {
    const registry = new actions.ActionRegistry({
      openExternal: async (url) => opened.push(url),
      probe: async () => true,
    });
    const savedLink = {
      id: 'saved-link',
      kind: 'link',
      title: 'Open docs',
      url: 'https://example.com/docs',
      status: 'pending',
      createdAt: new Date().toISOString(),
      updatedAt: new Date().toISOString(),
    };
    const openedLink = await registry.perform({
      taskId: 'restored-task', cwd: project.root, action: savedLink, operation: 'open',
    });
    assert.equal(openedLink.status, 'done');
    assert.deepEqual(opened, ['https://example.com/docs']);

    const savedManual = {
      id: 'saved-manual',
      kind: 'manual',
      title: 'Review output',
      status: 'pending',
      createdAt: new Date().toISOString(),
      updatedAt: new Date().toISOString(),
    };
    const completed = await registry.perform({
      taskId: 'restored-task', cwd: project.root, action: savedManual, operation: 'complete',
    });
    assert.equal(completed.status, 'done');
  } finally {
    await removeProject(project.root);
  }
});

test('reports startup errors and never fabricates readiness', async () => {
  const project = await makeProject({ script: 'node -e "setTimeout(() => process.exit(1), 40)"' });
  const silent = await makeProject({ script: 'node -e "setTimeout(() => {}, 3000)"' });
  try {
    // Leave room for npm's startup under a parallel test load; this scenario
    // verifies process exit, while the separate silent fixture verifies timeout.
    const failedRegistry = new actions.ActionRegistry({ startupTimeoutMs: 5_000, probeTimeoutMs: 100, stopGraceMs: 100 });
    const failed = await failedRegistry.maybeAutoStart('failed-task', project.root, {
      mode: 'execute', runKind: 'lead', status: 'completed', workerStatuses: [],
    });
    assert.equal(failed.action.status, 'error');
    assert.match(failed.action.error, /exited|code/i);

    const silentRegistry = new actions.ActionRegistry({ startupTimeoutMs: 500, probeTimeoutMs: 100, stopGraceMs: 100 });
    const silentResult = await silentRegistry.maybeAutoStart('silent-task', silent.root, {
      mode: 'execute', runKind: 'lead', status: 'completed', workerStatuses: [],
    });
    assert.equal(silentResult.action.status, 'error');
    assert.match(silentResult.action.error, /respond|within/i);
    assert.equal(silentResult.action.url, undefined);
  } finally {
    await removeProject(project.root);
    await removeProject(silent.root);
  }
});
