import assert from "node:assert/strict";
import test from "node:test";
import { compactConversationContext } from "../src/api.js";

test("requests manual compaction without creating a chat message", async (t) => {
  const controller = new AbortController();
  const result = { compacted: true, before_tokens: 2000, estimated_tokens: 1000, context_checkpoint: { through_message_id: 2 } };
  t.mock.method(globalThis, "fetch", async (path, options) => {
    assert.equal(path, "/api/conversations/7/compact");
    assert.equal(options.method, "POST");
    assert.equal(options.signal, controller.signal);
    assert.equal(options.body, undefined);
    return new Response(JSON.stringify(result));
  });
  assert.deepEqual(await compactConversationContext(7, controller.signal), result);
});

test("surfaces generation conflicts and summarization failures for retry", async (t) => {
  const mock = t.mock.method(globalThis, "fetch", async () => new Response(JSON.stringify({ error: { message: "Wait for the active response" } }), { status: 409 }));
  await assert.rejects(compactConversationContext(7), error => error.status === 409 && error.message === "Wait for the active response");
  mock.mock.mockImplementation(async () => new Response(JSON.stringify({ error: { message: "Summarizer unavailable" } }), { status: 502 }));
  await assert.rejects(compactConversationContext(7), /Summarizer unavailable/);
});
