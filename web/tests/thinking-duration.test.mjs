import assert from "node:assert/strict";
import test from "node:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { applyThinkingTiming, finishThinkingTiming, formatThinkingDuration, thinkingDuration } from "../src/thinking-duration.js";
import ThinkingLabel from "../src/thinking-label.js";

test("live thinking counts from event receipt without relying on the server clock", () => {
  const message = applyThinkingTiming({ status: "streaming", metadata: { agent_turns: 2 } }, { thinking_active: true, thinking_duration_ms: 2500 }, 10000);
  assert.deepEqual(thinkingDuration(message, 12000), { milliseconds: 4500, live: true });
  assert.equal(message.metadata.agent_turns, 2);
  assert.deepEqual(thinkingDuration(message, 9000), { milliseconds: 2500, live: true });
});

test("server completion freezes exact duration and excludes tool pauses before the next phase", () => {
  let message = applyThinkingTiming({ status: "streaming" }, { thinking_active: true, thinking_duration_ms: 0 }, 1000);
  message = applyThinkingTiming(message, { thinking_active: false, thinking_duration_ms: 2800 }, 4000);
  assert.deepEqual(thinkingDuration(message, 20000), { milliseconds: 2800, live: false });
  message = applyThinkingTiming(message, { thinking_active: true, thinking_duration_ms: 2800 }, 20000);
  assert.deepEqual(thinkingDuration(message, 21500), { milliseconds: 4300, live: true });
});

test("completion prefers measured server time and preserves response metadata", () => {
  const live = applyThinkingTiming({ status: "streaming", metadata: { agent_turns: 2 } }, { thinking_active: true, thinking_duration_ms: 1000 }, 5000);
  const done = { ...finishThinkingTiming(live, { thinking_duration_ms: 3200, eval_count: 100 }, 9000), status: "complete" };
  assert.deepEqual(thinkingDuration(done, 999999), { milliseconds: 3200, live: false });
  assert.equal(done.metadata.eval_count, 100);
  assert.equal(done.metadata.agent_turns, 2);
  assert.equal(done.thinking_timing.active, false);
});

test("Stop and errors freeze the elapsed live phase even if its final event cannot arrive", () => {
  const live = applyThinkingTiming({ status: "streaming" }, { thinking_active: true, thinking_duration_ms: 1000 }, 5000);
  for (const status of ["cancelled", "error"]) {
    const stopped = { ...finishThinkingTiming(live, undefined, 9000), status };
    assert.deepEqual(thinkingDuration(stopped, 999999), { milliseconds: 5000, live: false });
  }
});

test("reloaded messages use saved durations while legacy and invalid data stay unknown", () => {
  const saved = { status: "complete", thinking: "Reasoning", metadata: { thinking_duration_ms: 12500 } };
  assert.deepEqual(thinkingDuration(saved, 999999), { milliseconds: 12500, live: false });
  for (const value of [undefined, -1, Infinity, "1000"]) {
    const message = { thinking: "Reasoning", metadata: { thinking_duration_ms: value, eval_duration: 999999999 } };
    assert.equal(thinkingDuration(message), null);
    assert.equal(applyThinkingTiming(message, { thinking_duration_ms: value }), message);
  }
});

test("duration labels use whole seconds and correct minute boundaries", () => {
  for (const [milliseconds, label] of [[0, "<1 s"], [999, "<1 s"], [1000, "1 s"], [59999, "59 s"], [60000, "1 min 0 s"], [125000, "2 min 5 s"], [-1, null]]) {
    assert.equal(formatThinkingDuration(milliseconds), label);
  }
});

test("the thinking summary displays stored duration without inventing one for older replies", () => {
  const render = (metadata) => renderToStaticMarkup(createElement(ThinkingLabel, { message: { status: "complete", thinking: "Reasoning", metadata } }));
  assert.match(render({ thinking_duration_ms: 16350 }), /Thought for 16 s/);
  assert.match(render({ thinking_duration_ms: 0 }), /Thought for &lt;1 s/);
  assert.match(render({}), /Model thinking/);
});
