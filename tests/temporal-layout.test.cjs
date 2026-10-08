'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const Module = require('node:module');
const ts = require('typescript');

// Nested src/ modules (./i18n) are TypeScript too.
Module._extensions['.ts'] = (m, file) =>
  m._compile(
    ts.transpileModule(fs.readFileSync(file, 'utf8'), {
      fileName: file,
      compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true },
    }).outputText,
    file,
  );
const filename = path.resolve(__dirname, '../src/temporal-layout.ts');
const compiled = ts.transpileModule(fs.readFileSync(filename, 'utf8'), {
  fileName: filename,
  compilerOptions: {
    module: ts.ModuleKind.CommonJS,
    target: ts.ScriptTarget.ES2022,
    esModuleInterop: true,
  },
});
const loaded = new Module(filename, module);
loaded.filename = filename;
loaded.paths = Module._nodeModulePaths(path.dirname(filename));
const originalRequire = loaded.require.bind(loaded);
loaded.require = (id) => {
  if (id === './types') return {};
  return originalRequire(id);
};
loaded._compile(compiled.outputText, filename);
const {
  allocateIntervalRows,
  buildTemporalLayout,
  intervalGeometry,
  pointPosition,
  temporalCanvasWidth,
} = loaded.exports;

const iso = (seconds) => new Date(Date.parse('2026-10-05T10:00:00.000Z') + seconds * 1000).toISOString();
const agent = (id, name = id) => ({ id, name, role: 'Worker', model: 'test', status: 'running', summary: '', progress: 0 });
const task = (overrides = {}) => ({
  status: 'done',
  agents: [agent('design', 'Atlas'), agent('build', 'Nova')],
  events: [],
  questions: [],
  feedback: [],
  instructions: [],
  ...overrides,
});
const flight = (id, time, agentId, lifecycle, runId, extra = {}) => ({
  id,
  time: iso(time),
  type: 'agent',
  title: `${agentId} ${lifecycle}`,
  detail: '',
  agentId,
  lifecycle,
  runId,
  ...extra,
});

test('builds parallel per-run intervals and places them on one elapsed axis', () => {
  const result = buildTemporalLayout(task({
    events: [
      flight('design-a-start', 0, 'design', 'started', 'run-a', { worktree: '/tmp/a' }),
      flight('design-b-start', 2, 'design', 'started', 'run-b'),
      flight('build-a-start', 2, 'build', 'started', 'run-a'),
      flight('design-a-end', 6, 'design', 'completed', 'run-a'),
      flight('build-a-end', 8, 'build', 'completed', 'run-a'),
      flight('design-b-end', 10, 'design', 'blocked', 'run-b'),
    ],
  }));

  assert.equal(result.intervals.length, 3);
  assert.deepEqual(result.intervals.map((item) => item.runId), ['run-a', 'run-a', 'run-b']);
  assert.equal(result.intervals.find((item) => item.runId === 'run-a' && item.agentId === 'design').endKind, 'completed');
  assert.equal(result.intervals.find((item) => item.runId === 'run-b').endKind, 'blocked');
  assert.equal(result.intervals.find((item) => item.runId === 'run-a' && item.agentId === 'design').worktree, '/tmp/a');
  assert.equal(result.axis.durationMs, 10000);
  assert.equal(result.axis.ticks[0].elapsedMs, 0);
  assert.equal(result.axis.ticks.at(-1).elapsedMs, 10000);

  const design = result.lanes.find((lane) => lane.agentId === 'design');
  const rows = allocateIntervalRows(design.intervals);
  assert.notEqual(rows.get('interval:design-a-start'), rows.get('interval:design-b-start'), 'overlapping runs need separate visual rows');
});

test('geometry is proportional and long gaps remain scrollable rather than compressed into fake dates', () => {
  const axis = { startMs: 0, endMs: 10000, durationMs: 10000, ticks: [] };
  assert.deepEqual(intervalGeometry({ startMs: 2000, endMs: 8000 }, axis), { left: 20, width: 60 });
  assert.equal(pointPosition(5000, axis), 50);
  assert.equal(temporalCanvasWidth(1000 * 60 * 60 * 8, 1) > 680, true);
  assert.equal(temporalCanvasWidth(0, 1), 680);
});

test('deduplicates referenced human actions while retaining exact human timestamps', () => {
  const instructionTime = iso(3);
  const answerTime = iso(7);
  const feedbackTime = iso(12);
  const result = buildTemporalLayout(task({
    instructions: [{ id: 'instruction-1', text: 'Revois le filtre', time: instructionTime, agentId: 'build' }],
    questions: [{ id: 'Q01', title: 'Choix', answer: 'Par statut', answeredAt: answerTime, agentId: 'design' }],
    feedback: [{ id: 'feedback-1', text: 'Le contraste est faible', resolved: false, createdAt: feedbackTime }],
    events: [
      { id: 'instruction-event', time: instructionTime, type: 'note', title: 'Vous', detail: 'Revois le filtre', actor: 'human', interventionId: 'instruction-1' },
      { id: 'answer-event', time: answerTime, type: 'decision', title: 'Q01 · Par statut', detail: 'Par statut', actor: 'human', interventionId: `answer:Q01:${answerTime}` },
      { id: 'feedback-event', time: feedbackTime, type: 'review', title: 'Commentaire épinglé', detail: 'Le contraste est faible', actor: 'human', interventionId: 'feedback-1' },
      { id: 'pause-event', time: iso(15), type: 'note', title: 'Mission mise en pause', detail: 'Contexte conservé', actor: 'human' },
    ],
  }));

  assert.equal(result.humanInterventions.length, 4);
  assert.deepEqual(result.humanInterventions.map((item) => item.kind), ['instruction', 'decision', 'feedback', 'instruction']);
  assert.deepEqual(result.humanInterventions.map((item) => item.time), [instructionTime, answerTime, feedbackTime, iso(15)]);
  assert.equal(result.activities.length, 0, 'human journal echoes belong to the human row only');
});

test('never fabricates an interval end for a static task, but a running task may end at its supplied clock', () => {
  const start = iso(20);
  const staticResult = buildTemporalLayout(task({
    status: 'done',
    events: [flight('open-static', 20, 'build', 'started', 'run-static')],
  }), { currentTime: iso(90) });
  assert.equal(staticResult.intervals[0].endMs, undefined);
  assert.equal(staticResult.intervals[0].endKind, 'open');
  assert.equal(staticResult.axis.startMs, Date.parse(start));
  assert.equal(staticResult.axis.endMs, Date.parse(start));

  const runningResult = buildTemporalLayout(task({
    status: 'running',
    runId: 'run-live',
    events: [flight('open-live', 20, 'build', 'started', 'run-live')],
  }), { currentTime: iso(90) });
  assert.equal(runningResult.intervals[0].endMs, Date.parse(iso(90)));
  assert.equal(runningResult.intervals[0].endKind, 'running');
  assert.equal(runningResult.axis.endMs, Date.parse(iso(90)));
});

test('legacy events with invalid or missing times become no geometry instead of bogus dates', () => {
  const result = buildTemporalLayout(task({
    events: [
      { id: 'bad', time: 'yesterday-ish', type: 'note', title: 'Bad', detail: '' },
      { id: 'missing', time: '', type: 'note', title: 'Missing', detail: '' },
    ],
  }));
  assert.equal(result.hasRecordedTimes, false);
  assert.equal(result.activities.length, 0);
  assert.equal(result.axis.startMs, undefined);
  assert.equal(result.axis.endMs, undefined);
});
