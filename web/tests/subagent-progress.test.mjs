import assert from 'node:assert/strict';
import test from 'node:test';
import { reduceSubagentEvent } from '../src/agent-events.js';
import { currentSubagentActivity, formatSubagentElapsed, subagentActivities } from '../src/subagent-progress.js';

const read = { id: 1, title: 'Read notes.txt', detail: 'Bytes 0–4096', status: 'running', started_at: '2026-11-05T10:00:00Z' };

test('live child activity identifies the current file, then preserves its completed result on reload', () => {
  const run = { id: 7, status: 'running' };
  let runs = reduceSubagentEvent([], { type: 'subagent.progress', run, content: JSON.stringify({ phase: 'reading', turn: 2, activities: [read] }) });
  assert.equal(currentSubagentActivity(runs[0]).title, 'Read notes.txt');
  assert.equal(currentSubagentActivity(runs[0]).detail, 'Bytes 0–4096');
  const progress = { phase: 'reviewing', turn: 2, activities: [{ ...read, status: 'complete', output: '3 relevant sections found', completed_at: '2026-11-05T10:00:02Z' }] };
  runs = reduceSubagentEvent(runs, { type: 'subagent.done', run: { ...run, status: 'complete', progress } });
  const reloaded = JSON.parse(JSON.stringify(runs[0]));
  assert.equal(subagentActivities(reloaded)[0].output, '3 relevant sections found');
  assert.equal(subagentActivities(reloaded)[0].status, 'complete');
});

test('stopped and failed child runs do not leave actions spinning', () => {
  for (const [status, expected] of [['cancelled', 'cancelled'], ['failed', 'failed']]) {
    const run = { status, completed_at: '2026-11-05T10:00:03Z', progress: { activities: [read] } };
    const activity = subagentActivities(run)[0];
    assert.equal(activity.status, expected);
    assert.equal(activity.completed_at, run.completed_at);
    assert.equal(read.status, 'running');
  }
});

test('activity is bounded and legacy runs show only known phases', () => {
  const run = { status: 'running', progress: { phase: 'thinking', activities: Array.from({ length: 100 }, (_, id) => ({ ...read, id, status: 'complete' })) } };
  assert.equal(subagentActivities(run).length, 12);
  assert.equal(subagentActivities(run)[0].id, 88);
  assert.equal(currentSubagentActivity(run).title, 'Thinking');
  assert.deepEqual(subagentActivities({}), []);
  assert.equal(currentSubagentActivity({ status: 'queued' }).title, 'Waiting for the current subagent to finish');
  assert.deepEqual(currentSubagentActivity({ status: 'running', progress: { total: 50, processed: 20 } }), { title: 'Processing images', detail: '20 of 50 images processed' });
});

test('elapsed times use observed timestamps rather than an invented completion estimate', () => {
  assert.equal(formatSubagentElapsed(read.started_at, '2026-11-05T10:01:04Z'), '1m 04s');
  assert.equal(formatSubagentElapsed(read.started_at, '2026-11-05T10:00:02Z'), '2s');
  assert.equal(formatSubagentElapsed(read.started_at, '2026-11-05T10:00:00.100Z'), '<1s');
  assert.equal(formatSubagentElapsed(undefined, Date.now()), null);
  assert.equal(formatSubagentElapsed(read.started_at, '2026-11-05T09:59:59Z'), null);
});
