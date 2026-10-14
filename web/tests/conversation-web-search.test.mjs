import assert from "node:assert/strict";
import test from "node:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { changeConversationWebSearch, createConversation } from "../src/api.js";
import ConversationWebSearch, { conversationWebSearchEnabled } from "../src/conversation-web-search.js";

const provider = { provider: "tavily", configured: true, enabled: true };
function render(props = {}) {
  return renderToStaticMarkup(createElement(ConversationWebSearch, { mode: "agent", enabled: true, provider, onChange() {}, ...props }));
}

test("legacy sessions inherit search permission while explicit session choices survive reload", () => {
  assert.equal(conversationWebSearchEnabled({}), true);
  assert.equal(conversationWebSearchEnabled({ web_search_enabled: true }), true);
  assert.equal(conversationWebSearchEnabled({ web_search_enabled: false }), false);
});

test("new sessions send their explicit search choice without touching model defaults", async (t) => {
  t.mock.method(globalThis, "fetch", async (path, options) => {
    assert.equal(path, "/api/conversations");
    const body = JSON.parse(options.body);
    assert.equal(body.web_search_enabled, false);
    assert.equal(body.mode, "agent");
    return new Response(JSON.stringify({ id: 3, ...body }));
  });
  const result = await createConversation({ model: "test", mode: "agent", web_search_enabled: false });
  assert.equal(result.web_search_enabled, false);
});

test("session search saves return the confirmed choice and expose active-session conflicts", async (t) => {
  const mock = t.mock.method(globalThis, "fetch", async (path, options) => {
    assert.equal(path, "/api/conversations/3/web-search");
    assert.equal(options.method, "PUT");
    assert.equal(options.headers["Content-Type"], "application/json");
    assert.deepEqual(JSON.parse(options.body), { enabled: false });
    return new Response(JSON.stringify({ id: 3, mode: "agent", web_search_enabled: false }));
  });
  assert.equal((await changeConversationWebSearch(3, false)).web_search_enabled, false);
  mock.mock.mockImplementation(async () => new Response(JSON.stringify({ error: { message: "Wait for the active response" } }), { status: 409 }));
  await assert.rejects(changeConversationWebSearch(3, true), (error) => error.status === 409 && error.message === "Wait for the active response");
});

test("Agent setting is keyboard-labelled, editable between replies, and disabled while saving", () => {
  const normal = render();
  assert.match(normal, /for="conversation-web-search"/);
  assert.match(normal, />Web search<\/label>/);
  assert.match(normal, /aria-describedby="conversation-web-search-help"/);
  assert.match(normal, /checked=""/);
  assert.doesNotMatch(normal, /disabled=""/);
  const saving = render({ saving: true });
  assert.match(saving, /disabled=""/);
  assert.match(saving, /role="status">Saving web search/);
  assert.match(render({ disabled: true }), /disabled=""/);
});

test("Chat stays unchecked and tool-free even when session and provider permit search", () => {
  const html = render({ mode: "chat" });
  assert.match(html, /disabled=""/);
  assert.doesNotMatch(html, /checked=""/);
  assert.match(html, /Chat talks to the model without tools/);
});

test("provider prerequisites, disabled sessions, and save errors have honest inline feedback", () => {
  assert.match(render({ provider: null }), /Checking Tavily availability/);
  assert.match(render({ provider: { configured: false, enabled: false } }), /Add a Tavily API token/);
  assert.match(render({ provider: { configured: true, enabled: false } }), /Tavily is disabled in System settings/);
  assert.match(render({ providerError: "Offline" }), /Could not refresh Tavily status/);
  const failed = render({ enabled: false, error: "Save failed <try again>" });
  assert.doesNotMatch(failed, /checked=""/);
  assert.match(failed, /Other Agent tools remain available/);
  assert.match(failed, /role="alert">Save failed &lt;try again&gt;/);
});
