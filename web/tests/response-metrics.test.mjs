import assert from "node:assert/strict";
import test from "node:test";
import { formatDuration, formatRate, responseMetrics } from "../src/response-metrics.js";

test("derives aggregate agent-run rates and timings", () => {
  assert.deepEqual(responseMetrics({
    agent_eval_count: 150,
    agent_eval_duration: 3_000_000_000,
    agent_prompt_eval_count: 600,
    agent_prompt_eval_duration: 2_000_000_000,
    agent_total_duration: 7_500_000_000,
    agent_load_duration: 500_000_000,
    agent_turns: 2,
    eval_count: 10,
  }), {
    outputTokens: 150,
    inputTokens: 600,
    generationRate: 50,
    promptRate: 300,
    totalSeconds: 7.5,
    loadSeconds: 0.5,
    turns: 2,
    aggregate: true,
    mode: "agent",
  });
});

test("supports legacy final-call metadata", () => {
  const metrics = responseMetrics({
    eval_count: 40,
    eval_duration: 2_000_000_000,
    prompt_eval_count: 100,
    prompt_eval_duration: 500_000_000,
    total_duration: 3_000_000_000,
  });
  assert.equal(metrics.generationRate, 20);
  assert.equal(metrics.promptRate, 200);
  assert.equal(metrics.aggregate, false);
  assert.equal(metrics.mode, "agent");
});

test("identifies single-call Chat metadata", () => {
  const metrics = responseMetrics({ run_mode: "chat", eval_count: 10, eval_duration: 1_000_000_000 });
  assert.equal(metrics.mode, "chat");
  assert.equal(metrics.aggregate, false);
  assert.equal(metrics.generationRate, 10);
});

test("omits invalid rates instead of emitting non-finite values", () => {
  const metrics = responseMetrics({ eval_count: 20, eval_duration: 0, total_duration: "invalid" });
  assert.equal(metrics.generationRate, null);
  assert.equal(metrics.totalSeconds, null);
  assert.equal(responseMetrics({ eval_count: "bad" }), null);
  assert.equal(responseMetrics(null), null);
});

test("formats rates and durations compactly", () => {
  assert.equal(formatRate(7.125), "7.13 tok/s");
  assert.equal(formatRate(42.34), "42.3 tok/s");
  assert.equal(formatRate(Number.POSITIVE_INFINITY), null);
  assert.equal(formatDuration(0.245), "245 ms");
  assert.equal(formatDuration(7.25), "7.3 s");
  assert.equal(formatDuration(75), "1:15");
});
