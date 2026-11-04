import test from 'node:test';
import assert from 'node:assert/strict';
import { getConversation, getMessageContext } from '../src/api.js';
import { HISTORY_PAGE_SIZE, HISTORY_RETAIN_MESSAGES, releaseHistory, prependHistory, captureHistoryAnchor, restoreHistoryAnchor } from '../src/conversation-history.js';

const savedMessages = (count) => Array.from({ length: count }, (_, index) => ({ id: index + 1, status: 'complete' }));

test('a long-running conversation releases complete old pages while retaining the live tail', () => {
  let messages = [];
  for (let id = 1; id <= 600; id++) {
    const latest = { id, status: 'streaming', content: 'live reply' };
    messages.push(latest);
    messages = releaseHistory(messages, id);
    assert.ok(messages.length < HISTORY_RETAIN_MESSAGES + HISTORY_PAGE_SIZE);
    assert.equal(messages.at(-1), latest);
    latest.status = 'complete';
  }
  assert.equal(messages.length, HISTORY_RETAIN_MESSAGES);
  assert.equal(messages[0].id, 571);
});

test('reading older history protects the viewport and a page of scroll-back buffer', () => {
  const messages = savedMessages(100);
  assert.equal(releaseHistory(messages, 1), messages);
  assert.equal(releaseHistory(messages, 6), messages);
  const retained = releaseHistory(messages, 21);
  assert.equal(retained[0].id, 16);
  assert.equal(retained.find((message) => message.id === 21), messages[20]);
  assert.equal(releaseHistory(messages, 'missing'), messages);
});

test('focused, selected, streaming and unsaved messages cannot be evicted', () => {
  const messages = savedMessages(80);
  assert.equal(releaseHistory(messages, 80, ['4']), messages);
  assert.equal(releaseHistory(messages, 80, ['12', '24'])[0].id, 11);
  messages[3] = { id: 'pending-user', status: 'complete' };
  assert.equal(releaseHistory(messages, 80), messages);
  messages[3] = { id: 4, status: 'streaming' };
  assert.equal(releaseHistory(messages, 80), messages);
});

test('released pages reload without gaps or replacing the retained messages', () => {
  const saved = savedMessages(100);
  const retained = releaseHistory(saved, 100);
  const before = retained[0].id;
  const older = saved.filter((message) => message.id < before).slice(-HISTORY_PAGE_SIZE);
  const restored = prependHistory(retained, older);
  assert.deepEqual(restored.map((message) => message.id), saved.slice(-35).map((message) => message.id));
  assert.equal(restored.at(-1), retained.at(-1));
});

test('requests bounded history and an exclusive cursor while preserving legacy reads', async (t) => {
  const calls = [];
  const controller = new AbortController();
  t.mock.method(globalThis, 'fetch', async (path, options) => {
    calls.push([path, options.signal]);
    return new Response(JSON.stringify({ messages: [] }));
  });
  await getConversation(7, controller.signal, { limit: HISTORY_PAGE_SIZE });
  await getConversation(7, controller.signal, { limit: HISTORY_PAGE_SIZE, before: 42 });
  await getConversation(7);
  assert.deepEqual(calls.map(([path]) => path), ['/api/conversations/7?limit=5', '/api/conversations/7?limit=5&before=42', '/api/conversations/7']);
  assert.equal(calls[1][1], controller.signal);
});

test('prepends older pages without replacing a live response or duplicating overlaps', () => {
  const live = { id: 4, content: 'streaming response' };
  const current = [{ id: 3 }, live];
  assert.deepEqual(prependHistory(current, [{ id: 1 }, { id: 2 }, { id: 3 }, { id: 4, content: 'old' }]), [{ id: 1 }, { id: 2 }, ...current]);
  assert.equal(current[1], live);
});

test('restores the visible message offset after prepend even if native anchoring has moved it', () => {
  let messageTop = 75;
  const message = { isConnected: true, getBoundingClientRect: () => ({ top: messageTop, bottom: messageTop + 200 }) };
  const transcript = {
    scrollTop: 30,
    getBoundingClientRect: () => ({ top: 100 }),
    querySelectorAll: () => [message],
    scrollTo({ top, behavior }) { this.scrollTop = top; assert.equal(behavior, 'instant'); },
  };
  const anchor = captureHistoryAnchor(transcript);
  messageTop += 500;
  restoreHistoryAnchor(transcript, anchor);
  assert.equal(transcript.scrollTop, 530);
  messageTop = 75;
  restoreHistoryAnchor(transcript, anchor);
  assert.equal(transcript.scrollTop, 530);
});

 test('fetches inspection context separately with cancellation', async (t) => {
  const controller = new AbortController();
  t.mock.method(globalThis, 'fetch', async (path, options) => {
    assert.equal(path, '/api/conversations/7/messages/42/context');
    assert.equal(options.signal, controller.signal);
    return new Response(JSON.stringify({ model_exchange: { status: 'complete' } }));
  });
  assert.deepEqual(await getMessageContext(7, 42, controller.signal), { model_exchange: { status: 'complete' } });
});
