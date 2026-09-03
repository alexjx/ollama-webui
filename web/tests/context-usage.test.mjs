import assert from "node:assert/strict";
import test from "node:test";
import { contextUsage, latestContextTokens } from "../src/context-usage.js";

test("uses the latest assistant token metadata", () => {
  const messages = [
    { role: "assistant", metadata: { prompt_eval_count: 100, eval_count: 25 } },
    { role: "user", content: "follow up" },
    { role: "assistant", metadata: { prompt_eval_count: 240, eval_count: 60 } },
  ];
  assert.equal(latestContextTokens(messages), 300);
});

test("falls back past messages without usable metadata", () => {
  const messages = [
    { role: "assistant", metadata: { prompt_eval_count: 90, eval_count: 10 } },
    { role: "assistant", status: "cancelled" },
    { role: "assistant", metadata: { prompt_eval_count: "invalid" } },
  ];
  assert.equal(latestContextTokens(messages), 100);
  assert.equal(latestContextTokens([]), 0);
});

test("calculates and bounds explicit context usage", () => {
  const messages = [{ role: "assistant", metadata: { prompt_eval_count: 3072, eval_count: 1024 } }];
  assert.deepEqual(contextUsage(messages, "8192"), { usedTokens: 4096, limit: 8192, percent: 50 });
  assert.deepEqual(contextUsage(messages, "2048"), { usedTokens: 4096, limit: 2048, percent: 100 });
});

test("does not invent a percentage for an inherited limit", () => {
  const messages = [{ role: "assistant", metadata: { prompt_eval_count: 320, eval_count: 80 } }];
  assert.deepEqual(contextUsage(messages, "default"), { usedTokens: 400, limit: null, percent: null });
  assert.deepEqual(contextUsage(messages, "invalid"), { usedTokens: 400, limit: null, percent: null });
});
