import assert from "node:assert/strict";
import test from "node:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { saveWebSearchSettings, testWebSearchSettings } from "../src/api.js";
import { createSettingsRequestGuard, parseWebSearchResults, webSearchSettingsInput, webSearchStatus } from "../src/web-search.js";
import WebSearchResults from "../src/web-search-results.js";

test("public settings distinguish absence, disabled saved token, and enabled search", () => {
  assert.equal(webSearchStatus(null), "Loading…");
  assert.equal(webSearchStatus({ configured: false, enabled: false }), "Not configured");
  assert.equal(webSearchStatus({ configured: true, enabled: false }), "Disabled");
  assert.equal(webSearchStatus({ configured: true, enabled: true }), "Enabled");
});

test("blank token saves only the toggle and never clears the saved credential", () => {
  assert.deepEqual(webSearchSettingsInput(false, "  "), { enabled: false });
  assert.deepEqual(webSearchSettingsInput(true, "  tvly-test  "), { enabled: true, api_token: "tvly-test" });
});

test("overlapping runtime reads cannot undo a saved token or leave loading owned by old reads", () => {
  const guard = createSettingsRequestGuard();
  const old = guard.read();
  const latest = guard.read();
  assert.equal(guard.current(old), false);
  assert.equal(guard.current(latest), true);
  guard.startWrite();
  assert.equal(guard.current(latest), false);
  const during = guard.read();
  assert.equal(guard.current(during), false);
  assert.equal(guard.latest(during), true);
  guard.finishWrite();
  assert.equal(guard.current(during), false);
  const fresh = guard.read();
  assert.equal(guard.current(fresh), true);
  assert.equal(guard.latest(during), false);
});

test("a delayed save response cannot replace the public status from a newer save", () => {
  const guard = createSettingsRequestGuard();
  const first = guard.startWrite();
  const second = guard.startWrite();
  assert.equal(guard.currentWrite(first), false);
  assert.equal(guard.currentWrite(second), true);
  guard.finishWrite();
  assert.equal(guard.current(guard.read()), false);
  guard.finishWrite();
  assert.equal(guard.current(guard.read()), true);
});

test("save and remove send token changes only to the settings endpoint", async (t) => {
  const signal = new AbortController().signal;
  const status = { provider: "tavily", configured: true, enabled: true };
  let body;
  t.mock.method(globalThis, "fetch", async (path, options) => {
    assert.equal(path, "/api/settings/web-search");
    assert.equal(options.method, "PUT");
    assert.equal(options.headers["Content-Type"], "application/json");
    assert.equal(options.signal, signal);
    body = JSON.parse(options.body);
    return new Response(JSON.stringify(status));
  });
  assert.deepEqual(await saveWebSearchSettings(webSearchSettingsInput(true, "tvly-test"), signal), status);
  assert.deepEqual(body, { enabled: true, api_token: "tvly-test" });
  await saveWebSearchSettings({ enabled: false, remove_token: true }, signal);
  assert.deepEqual(body, { enabled: false, remove_token: true });
});

test("test search posts empty JSON using the saved token and surfaces provider failures", async (t) => {
  const signal = new AbortController().signal;
  const mock = t.mock.method(globalThis, "fetch", async (path, options) => {
    assert.equal(path, "/api/settings/web-search/test");
    assert.equal(options.method, "POST");
    assert.equal(options.signal, signal);
    assert.equal(options.headers["Content-Type"], "application/json");
    assert.equal(options.body, "{}");
    return new Response(JSON.stringify({ ok: true, result_count: 3 }));
  });
  assert.deepEqual(await testWebSearchSettings(signal), { ok: true, result_count: 3 });
  mock.mock.mockImplementation(async () => new Response(JSON.stringify({ error: { message: "Tavily token was rejected" } }), { status: 502 }));
  await assert.rejects(testWebSearchSettings(signal), /Tavily token was rejected/);
});

test("search result links allow only absolute HTTP(S) sources", () => {
  const output = JSON.stringify({ query: "test", results: [
    { title: "Secure", url: "https://example.com/a", content: "A snippet" },
    { title: "Local", url: "http://example.com/b" },
    { title: "Script", url: "javascript:alert(1)" },
    { title: "Credential URL", url: "https://user:secret@example.com" },
    { title: "Data", url: "data:text/html,hello" },
    { title: "Relative", url: "/hello" },
    null,
  ] });
  const parsed = parseWebSearchResults(output);
  assert.equal(parsed.query, "test");
  assert.deepEqual(parsed.results.map(({ url }) => url), ["https://example.com/a", "http://example.com/b", null, null, null, null]);
  assert.equal(parseWebSearchResults("Tavily unavailable"), null);
  assert.equal(parseWebSearchResults('{"error":"Failed"}'), null);
});

test("rendered search results escape titles/snippets and expose useful external sources", () => {
  const html = renderToStaticMarkup(createElement(WebSearchResults, { output: JSON.stringify({ query: "test", results: [
    { title: '<script>alert("bad")</script>', url: "https://example.com", content: '<img src=x onerror="bad">' },
    { title: "Unsafe source", url: "javascript:alert(1)", content: "Plain snippet" },
  ] }) }));
  assert.match(html, /href="https:\/\/example.com\/"/);
  assert.match(html, /rel="noopener noreferrer"/);
  assert.match(html, /opens in a new tab/);
  assert.match(html, /&lt;script&gt;/);
  assert.match(html, /&lt;img/);
  assert.doesNotMatch(html, /javascript:|<script>|<img/);
  assert.match(html, /Unsafe source/);
});

test("empty search and sanitized provider errors keep honest readable feedback", () => {
  assert.match(renderToStaticMarkup(createElement(WebSearchResults, { output: '{"query":"test","results":[]}' })), /No search results found/);
  assert.equal(renderToStaticMarkup(createElement(WebSearchResults, { output: "Search failed: <unavailable>" })), "<pre>Search failed: &lt;unavailable&gt;</pre>");
});
