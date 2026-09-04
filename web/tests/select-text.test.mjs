import assert from "node:assert/strict";
import test from "node:test";
import { selectElementText } from "../src/select-text.js";

test("selects all rendered text inside the requested element", () => {
  const element = { textContent: "Complete assistant response" };
  const calls = [];
  const range = {
    selectNodeContents(target) { calls.push(["select", target]); },
  };
  const selection = {
    removeAllRanges() { calls.push(["clear"]); },
    addRange(value) { calls.push(["add", value]); },
  };
  const documentRoot = {
    createRange: () => range,
    getSelection: () => selection,
  };

  assert.equal(selectElementText(element, documentRoot), true);
  assert.deepEqual(calls, [
    ["select", element],
    ["clear"],
    ["add", range],
  ]);
});

test("returns false without changing selection when the browser API is unavailable", () => {
  assert.equal(selectElementText({ textContent: "Response" }, {}), false);
  assert.equal(selectElementText(null, {
    createRange: () => ({}),
    getSelection: () => ({}),
  }), false);
});

test("returns false when the browser rejects the selection", () => {
  const documentRoot = {
    createRange: () => ({ selectNodeContents() { throw new Error("Detached node"); } }),
    getSelection: () => ({ removeAllRanges() {}, addRange() {} }),
  };

  assert.equal(selectElementText({}, documentRoot), false);
});
