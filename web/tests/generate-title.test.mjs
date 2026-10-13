import test from "node:test";
import assert from "node:assert/strict";
import { generateConversationTitle } from "../src/api.js";

test("explicit title request targets its session without creating a message", async () => {
  const previous = globalThis.fetch;
  const controller = new AbortController();
  globalThis.fetch = async (path, options) => {
    assert.equal(path, "/api/conversations/42/title");
    assert.equal(options.method, "POST");
    assert.equal(options.signal, controller.signal);
    assert.equal(options.body, undefined);
    return Response.json({ id: 42, title: "Latest topic", auto_title_pending: false });
  };
  try { assert.equal((await generateConversationTitle(42, controller.signal)).title, "Latest topic"); }
  finally { globalThis.fetch = previous; }
});

test("naming conflicts and no-completed-turn errors reach the session menu", async () => {
  const previous = globalThis.fetch;
  try {
    for (const status of [409, 422, 502]) {
      globalThis.fetch = async () => Response.json({ error: { message: "Naming could not finish" } }, { status });
      await assert.rejects(generateConversationTitle(42), error => error.status === status && error.message === "Naming could not finish");
    }
  } finally { globalThis.fetch = previous; }
});
