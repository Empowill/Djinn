'use strict';
exports.taskFixture = (overrides = {}) => ({
  id: 'task-1', title: 'Brief', brief: 'Inspect', project: '', provider: 'codex', model: '',
  phase: 'brief', status: 'idle', createdAt: '2026-10-06T08:00:00.000Z',
  questions: [], events: [], agents: [], artifacts: [], feedback: [],
  configuration: { prototype: '', review: '', deliverables: [], concurrency: 1 },
  ...overrides,
});
