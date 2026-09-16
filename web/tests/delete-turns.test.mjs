import test from 'node:test';
import assert from 'node:assert/strict';
import { deleteTurns } from '../src/api.js';

test('retries deletion while a stopped generation finishes saving', async (t) => {
  let calls = 0;
  t.mock.method(globalThis, 'setTimeout', (callback) => { callback(); });
  t.mock.method(globalThis, 'fetch', async (path, options) => {
    assert.equal(path, '/api/conversations/7/messages/12');
    assert.equal(options.method, 'DELETE');
    calls++;
    return calls === 1
      ? new Response(JSON.stringify({ error: { message: 'Still stopping' } }), { status: 409 })
      : new Response(JSON.stringify({ conversation: { id: 7 }, messages: [] }));
  });
  assert.deepEqual((await deleteTurns(7, 12)).messages, []);
  assert.equal(calls, 2);
});

test('stops retrying and surfaces server errors', async (t) => {
  t.mock.method(globalThis, 'setTimeout', (callback) => { callback(); });
  let calls = 0;
  const fetch = t.mock.method(globalThis, 'fetch', async () => {
    calls++;
    return new Response(JSON.stringify({ error: { message: 'Still stopping' } }), { status: 409 });
  });
  await assert.rejects(deleteTurns(7, 12), /Still stopping/);
  assert.equal(calls, 9);
  fetch.mock.mockImplementation(async () => new Response(JSON.stringify({ error: { message: 'Missing turn' } }), { status: 404 }));
  await assert.rejects(deleteTurns(7, 12), /Missing turn/);
  assert.equal(fetch.mock.callCount(), 10);
});
