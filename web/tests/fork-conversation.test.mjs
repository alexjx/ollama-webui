import assert from "node:assert/strict";
import test from "node:test";
import { forkConversation } from "../src/api.js";

test("fork requests a server snapshot without resending or overwriting browser settings", async (t) => {
  t.mock.method(globalThis, "fetch", async (path, options) => {
    assert.equal(path, "/api/conversations/42/fork");
    assert.equal(options.method, "POST");
    assert.equal(options.body, undefined);
    return Response.json({ id: 43, title: "Fork of Original", model: "saved-model", message_count: 8 }, { status: 201 });
  });
  assert.equal((await forkConversation(42)).id, 43);
});

test("fork errors preserve server explanations and status for retry feedback", async (t) => {
  t.mock.method(globalThis, "fetch", async () => Response.json({ error: { message: "Wait for the active response" } }, { status: 409 }));
  await assert.rejects(forkConversation(42), (error) => error.status === 409 && error.message === "Wait for the active response");
});
