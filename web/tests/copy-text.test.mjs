import assert from "node:assert/strict";
import test from "node:test";
import { copyText } from "../src/copy-text.js";

const text = '  世界🙂\n\tconsole.log("<&");\n';
function legacyDocument(result = true) {
  const calls = [];
  const range = { cloneRange: () => range };
  const selection = {
    rangeCount: 1, getRangeAt: () => range,
    removeAllRanges() { calls.push("clear ranges"); },
    addRange(value) { assert.equal(value, range); calls.push("restore range"); },
  };
  const original = {
    value: "unsent draft", selectionStart: 2, selectionEnd: 5, selectionDirection: "backward",
    focus() { calls.push("restore focus"); },
    setSelectionRange(...values) { assert.deepEqual(values, [2, 5, "backward"]); calls.push("restore caret"); },
  };
  const textarea = {
    style: {}, focus() { calls.push("focus temporary"); },
    select() { calls.push("select temporary"); },
    remove() { calls.push("remove temporary"); },
  };
  return { calls, original, textarea, documentRoot: {
    activeElement: original, getSelection: () => selection,
    body: { appendChild(node) { assert.equal(node, textarea); calls.push("append"); } },
    createElement: (tag) => { assert.equal(tag, "textarea"); return textarea; },
    execCommand(command) {
      assert.equal(command, "copy"); assert.equal(textarea.value, text); calls.push("copy");
      if (result instanceof Error) throw result;
      return result;
    },
  } };
}

test("modern copy preserves text without creating a legacy input", async () => {
  let copied;
  assert.equal(await copyText(text, { navigatorRoot: { clipboard: { async writeText(value) { copied = value; } } }, documentRoot: {} }), true);
  assert.equal(copied, text);
});

test("HTTP fallback runs synchronously and restores focus, caret and selection", async () => {
  const fixture = legacyDocument();
  const result = copyText(text, { navigatorRoot: {}, documentRoot: fixture.documentRoot });
  assert.deepEqual(fixture.calls, ["append", "focus temporary", "select temporary", "copy", "remove temporary", "restore focus", "restore caret", "clear ranges", "restore range"]);
  assert.equal(await result, true);
  assert.equal(fixture.original.value, "unsent draft");
});

test("falls back when the modern clipboard rejects permission", async () => {
  const fixture = legacyDocument();
  assert.equal(await copyText(text, { navigatorRoot: { clipboard: { async writeText() { throw new Error("denied"); } } }, documentRoot: fixture.documentRoot }), true);
  assert.ok(fixture.calls.includes("copy"));
});

for (const result of [false, new Error("copy denied")]) {
  test(`reports legacy failure and cleans up when ${result}`, async () => {
    const fixture = legacyDocument(result);
    assert.equal(await copyText(text, { navigatorRoot: {}, documentRoot: fixture.documentRoot }), false);
    assert.ok(fixture.calls.includes("remove temporary"));
    assert.ok(fixture.calls.includes("restore caret"));
  });
}

test("reports unavailable copying without an unhandled exception", async () => {
  assert.equal(await copyText(text, { navigatorRoot: {}, documentRoot: {} }), false);
  assert.equal(await copyText(text, { navigatorRoot: {}, documentRoot: { body: {}, execCommand() {}, createElement() { throw new Error("unavailable"); } } }), false);
});
