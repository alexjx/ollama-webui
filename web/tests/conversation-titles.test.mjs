import test from "node:test";
import assert from "node:assert/strict";
import { applyKnownConversationTitles, mergeConversationTitles, newestConversationTitle } from "../src/conversation-titles.js";

test("generated names update without replacing newer turn data", () => {
  const current = { id: 1, title: "Prompt", auto_title_pending: true, updated_at: "2026-10-13T12:00:00Z", message_count: 4, model: "new-model" };
  const result = mergeConversationTitles([current], [{ ...current, title: "Generated name", auto_title_pending: false, message_count: 2, model: "original-model" }]);
  assert.deepEqual(result, [{ ...current, title: "Generated name", auto_title_pending: false }]);
});

test("manual names stay intact and a completed title resolves a newer pending turn", () => {
  const manual = { id: 1, title: "Manual", auto_title_pending: false };
  const newer = { id: 2, title: "Latest", auto_title_pending: true, updated_at: "2026-10-13T12:01:00Z" };
  const result = mergeConversationTitles([manual, newer], [
    { id: 1, title: "Generated", auto_title_pending: false },
    { id: 2, title: "Old", auto_title_pending: false, updated_at: "2026-10-13T12:00:00Z" },
  ]);
  assert.equal(result[0], manual);
  assert.deepEqual(result[1], { ...newer, title: "Old", auto_title_pending: false });
});

test("a delayed pending list cannot restore the provisional name after naming finishes", () => {
  const completed = { id: 1, title: "Generated", auto_title_pending: false, updated_at: "2026-10-13T12:00:00Z", message_count: 2 };
  const pending = { ...completed, title: "Question", auto_title_pending: true, message_count: 4, model: "new-model", updated_at: "2026-10-13T12:01:00Z" };
  assert.deepEqual(applyKnownConversationTitles([pending], new Map([[1, completed]])), [{ ...pending, title: "Generated", auto_title_pending: false }]);
  assert.equal(newestConversationTitle(completed, pending), completed);
});

test("list reconciliation preserves manual names and does not recreate filtered or deleted rows", () => {
  const manual = { id: 1, title: "Manual", auto_title_pending: false, updated_at: "2026-10-13T12:01:00Z" };
  const old = { ...manual, title: "Generated", updated_at: "2026-10-13T12:00:00Z", message_count: 4 };
  assert.deepEqual(applyKnownConversationTitles([old], new Map([[1, manual]])), [{ ...old, title: "Manual" }]);
  assert.deepEqual(applyKnownConversationTitles([], new Map([[1, manual]])), []);
  const fresh = { ...manual, title: "New manual name", updated_at: "2026-10-13T12:02:00Z" };
  assert.equal(applyKnownConversationTitles([fresh], new Map([[1, manual]]))[0], fresh);
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
