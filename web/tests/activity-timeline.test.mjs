import test from 'node:test';
import assert from 'node:assert/strict';
import { recordActivityEvent, readActivityBlocks, finalizeActivity } from '../src/activity-timeline.js';

function stream(events) {
  let message = { content: '', thinking: '', status: 'streaming', agent_steps: [], subagent_runs: [] };
  for (const [index, event] of events.entries()) {
    message = recordActivityEvent(message, event, event.now ?? index * 10);
    if (event.type === 'thinking.delta') message.thinking += event.content;
    if (event.type === 'delta') message.content += event.content;
    if (event.step) message.agent_steps = [...message.agent_steps.filter((step) => step.id !== event.step.id), event.step];
    if (event.run) message.subagent_runs = [...message.subagent_runs.filter((run) => run.id !== event.run.id), event.run];
  }
  return message;
}
const start = { type: 'activity.started', metadata: { activity_version: 1 } };
const delta = (content, turn = 1) => ({ type: 'delta', content, turn });
const thought = (content, turn = 1) => ({ type: 'thinking.delta', content, turn });
const timing = (elapsed, active, now) => ({ type: 'thinking.timing', metadata: { thinking_duration_ms: elapsed, thinking_active: active }, now });

test('legacy and unmarked streaming replies retain legacy rendering', () => {
  const message = stream([thought('old'), delta('reply')]);
  assert.equal(message.activity, undefined);
  assert.equal(readActivityBlocks(message), null);
});

test('interleaved text and thinking preserves source order and coalesces adjacent deltas', () => {
  const message = stream([start, thought('a'), timing(12, false), thought('b'), delta('c'), thought('d'), delta('e'), { type: 'turn.done' }, delta('f')]);
  assert.deepEqual(readActivityBlocks(message).map((block) => [block.type, block.text]), [['thinking', 'ab'], ['content', 'c'], ['thinking', 'd'], ['content', 'ef']]);
  assert.equal(readActivityBlocks(message).filter((block) => block.live).length, 1);
});

test('turn boundary closes live text and subsequent turns stay separate', () => {
  let message = stream([start, delta('first'), { type: 'turn.started', turn: 2 }]);
  assert.equal(readActivityBlocks(message)[0].live, false);
  message = recordActivityEvent(message, delta('second', 2), 40);
  message.content += 'second';
  assert.deepEqual(readActivityBlocks(message).map((block) => [block.text, block.turn, block.live]), [['first', 1, false], ['second', 2, true]]);
});

test('tool and child updates resolve objects without moving or duplicating blocks', () => {
  const message = stream([start, thought('look'), { type: 'tool.started', step: { id: 3, status: 'running' } }, { type: 'subagent.started', run: { id: 8, status: 'running' } }, delta('wait'), { type: 'subagent.done', run: { id: 8, status: 'complete' } }, { type: 'tool.done', step: { id: 3, status: 'complete' } }, delta('done')]);
  const blocks = readActivityBlocks(message);
  assert.deepEqual(blocks.map((block) => block.type), ['thinking', 'tool', 'subagent', 'content']);
  assert.equal(blocks[1].step.status, 'complete');
  assert.equal(blocks[2].run.status, 'complete');
  assert.equal(blocks[3].text, 'waitdone');
});

test('UTF8 byte ranges support CJK, emoji and split surrogate chunks', () => {
  const message = stream([start, delta('你好'), delta('\ud83d'), delta('\ude03'), thought('思考'), delta('é')]);
  const blocks = readActivityBlocks(message);
  assert.deepEqual(blocks.map((block) => block.text), ['你好😃', '思考', 'é']);
  assert.equal(blocks[0].end, 10);
  assert.equal(blocks[2].start, 10);
});

test('canonical completed metadata overrides live layout and matches stream ranges', () => {
  let message = stream([start, thought('reason'), delta('answer'), { type: 'turn.started', turn: 2 }, delta('more', 2)]);
  const previous = readActivityBlocks(message).map(({ type, text, start, end, turn }) => ({ type, text, start, end, turn }));
  const blocks = message.activity.blocks.map(({ type, start, end, turn }) => ({ type, start, end, turn }));
  message = recordActivityEvent(message, { type: 'done', metadata: { activity: { version: 1, blocks } } });
  message.status = 'complete';
  assert.equal(message.activity, undefined);
  assert.deepEqual(readActivityBlocks(message).map(({ type, text, start, end, turn }) => ({ type, text, start, end, turn })), previous);
  assert.ok(readActivityBlocks(message).every((block) => !block.live));
});

test('per-block thinking timers exclude tools and freeze on stop or error', () => {
  let message = stream([start, timing(0, true, 100), { ...thought('first'), now: 100 }, timing(50, false, 150), { type: 'tool.started', step: { id: 1 }, now: 200 }, { type: 'turn.started', turn: 2, now: 5000 }, timing(50, true, 5100), { ...thought('second', 2), now: 5100 }]);
  let blocks = readActivityBlocks(message);
  assert.equal(blocks[0].thinking_timing.active, false);
  assert.equal(blocks[0].duration_ms, 50);
  message = finalizeActivity(message, 5150);
  blocks = readActivityBlocks(message);
  assert.equal(blocks[2].duration_ms, 50);
  assert.equal(blocks[2].thinking_timing.active, false);
  assert.ok(blocks.every((block) => !block.live));
  assert.equal(finalizeActivity(message, 99999).activity.blocks[2].duration_ms, 50);
});

test('malformed metadata falls back instead of dropping or duplicating text', () => {
  const base = { content: 'abc', thinking: '', metadata: {} };
  for (const blocks of [[], [{ type: 'content', start: 1, end: 3 }], [{ type: 'content', start: 0, end: 4 }], [{ type: 'content', start: 0, end: 2 }], [{ type: 'content', start: 0, end: 3 }, { type: 'content', start: 0, end: 3 }], [{ type: 'unknown' }]]) {
    assert.equal(readActivityBlocks({ ...base, metadata: { activity: { version: 1, blocks } } }), null);
  }
  assert.equal(readActivityBlocks({ content: '😃', metadata: { activity: { version: 1, blocks: [{ type: 'content', start: 0, end: 1 }, { type: 'content', start: 1, end: 4 }] } } }), null);
  assert.equal(readActivityBlocks({ ...base, metadata: { activity: { version: 2, blocks: [] } } }), null);
});

test('unresolved, omitted or duplicate references fall back', () => {
  for (const blocks of [[], [{ type: 'tool', step_id: 2 }], [{ type: 'tool', step_id: 1 }, { type: 'tool', step_id: 1 }]]) {
    assert.equal(readActivityBlocks({ agent_steps: [{ id: 1 }], metadata: { activity: { version: 1, blocks } } }), null);
  }
});

test('recording does not mutate message text, metadata or existing activity', () => {
  const message = stream([start, delta('a')]);
  const snapshot = structuredClone(message);
  const updated = recordActivityEvent(message, delta('b'));
  assert.deepEqual(message, snapshot);
  assert.equal(updated.content, 'a');
  assert.equal(updated.activity.blocks[0].end, 2);
});

test('done without canonical metadata freezes the recorded chronology', () => {
  let message = stream([start, timing(0, true, 100), { ...thought('working'), now: 100 }]);
  message = recordActivityEvent(message, { type: 'done' }, 150);
  assert.equal(readActivityBlocks(message)[0].duration_ms, 50);
  assert.equal(readActivityBlocks(message)[0].live, false);
});

test('same-turn separate thinking phases get their own cumulative duration shares', () => {
  const message = stream([start, timing(0, true, 100), { ...thought('first'), now: 100 }, timing(40, false, 140), delta('checking'), timing(40, true, 500), { ...thought('second'), now: 500 }, timing(70, false, 530), delta('answer')]);
  const thoughts = readActivityBlocks(message).filter((block) => block.type === 'thinking');
  assert.deepEqual(thoughts.map((block) => block.duration_ms), [40, 30]);
  assert.ok(thoughts.every((block) => !block.thinking_timing.active));
});
