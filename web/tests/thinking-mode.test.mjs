import assert from "node:assert/strict";
import test from "node:test";
import { thinkingModeForRequest, thinkingModeFromConversation } from "../src/thinking-mode.js";

test("loads explicit reasoning levels and inherited defaults", () => {
  assert.equal(thinkingModeFromConversation({ thinking_mode: "high" }), "high");
  assert.equal(thinkingModeFromConversation({ thinking_mode: null, thinking_enabled: null }), "default");
});

test("loads legacy boolean thinking values", () => {
  assert.equal(thinkingModeFromConversation({ thinking_enabled: true }), "on");
  assert.equal(thinkingModeFromConversation({ thinking_enabled: false }), "off");
});

test("only sends validated explicit modes", () => {
  assert.equal(thinkingModeForRequest("default"), null);
  assert.equal(thinkingModeForRequest("medium"), "medium");
  assert.equal(thinkingModeForRequest("unexpected"), null);
});
