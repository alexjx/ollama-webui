import test from 'node:test';
import assert from 'node:assert/strict';
import { getConversation, getMessageContext } from '../src/api.js';
import { HISTORY_PAGE_SIZE, prependHistory, captureHistoryAnchor, restoreHistoryAnchor } from '../src/conversation-history.js';

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
