import test from "node:test";
import assert from "node:assert/strict";
import { mergeConversationTitles, newestConversationTitle } from "../src/conversation-titles.js";

test("generated names update without replacing newer turn data", () => {
  const current = { id: 1, title: "Prompt", auto_title_pending: true, updated_at: "2026-10-13T12:00:00Z", message_count: 4, model: "new-model" };
  const result = mergeConversationTitles([current], [{ ...current, title: "Generated name", auto_title_pending: false, message_count: 2, model: "original-model" }]);
  assert.deepEqual(result, [{ ...current, title: "Generated name", auto_title_pending: false }]);
});

test("manual names and newer snapshots take priority over pending poll results", () => {
  const manual = { id: 1, title: "Manual", auto_title_pending: false };
  const newer = { id: 2, title: "Latest", auto_title_pending: true, updated_at: "2026-10-13T12:01:00Z" };
  const result = mergeConversationTitles([manual, newer], [
    { id: 1, title: "Generated", auto_title_pending: false },
    { id: 2, title: "Old", auto_title_pending: false, updated_at: "2026-10-13T12:00:00Z" },
  ]);
  assert.equal(result[0], manual);
  assert.equal(result[1], newer);
});

test("pending, filtered, and deleted conversations are not recreated by polling", () => {
  const item = { id: 1, title: "Prompt", auto_title_pending: true };
  assert.equal(mergeConversationTitles([item], [{ ...item, title: "Pending" }])[0], item);
  assert.equal(mergeConversationTitles([item], [])[0], item);
  assert.deepEqual(mergeConversationTitles([], [{ ...item, auto_title_pending: false }]), []);
});

test("delayed details cannot replace newer generated or manual names", () => {
  const old = { title: "Old", updated_at: "2026-10-13T12:00:00.1Z" };
  const generated = { title: "Generated", updated_at: "2026-10-13T12:00:00.1000001Z" };
  const manual = { title: "Manual", updated_at: "2026-10-13T12:00:00.1000002Z" };
  assert.equal(newestConversationTitle(old, generated), generated);
  assert.equal(newestConversationTitle(manual, generated), manual);
  assert.equal(newestConversationTitle(old), old);
});
