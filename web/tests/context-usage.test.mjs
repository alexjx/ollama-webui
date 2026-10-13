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

test("uses current-turn context counts while an agent response is still streaming", () => {
  const messages = [
    { role: "assistant", metadata: { prompt_eval_count: 90, eval_count: 10 } },
    { role: "user", content: "Continue the task" },
    { role: "assistant", status: "streaming", metadata: {
      prompt_eval_count: 1800, eval_count: 248,
      agent_prompt_eval_count: 4200, agent_eval_count: 600, agent_turns: 3,
    } },
  ];
  assert.deepEqual(contextUsage(messages, "4096"), { usedTokens: 2048, limit: 4096, percent: 50 });
  assert.deepEqual(contextUsage(messages, "default"), { usedTokens: 2048, limit: null, percent: null });
  messages.at(-1).status = "cancelled";
  assert.equal(latestContextTokens(messages), 2048);
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

const checkpointMessages = () => [
  { id: 1, role: "user", status: "complete", content: "Older question" },
  { id: 2, role: "assistant", status: "complete", content: "Older answer" },
  { id: 3, role: "user", status: "complete", content: "Recent question" },
  { id: 4, role: "assistant", status: "complete", metadata: { prompt_eval_count: 800, eval_count: 224 } },
];
const manualCheckpoint = {
  through_message_id: 2, summary: "Older context summarized", estimated_tokens: 512,
  state: { compacted_after_message_id: 4 },
};

test("shows a saved manual compaction estimate separately from response metrics", () => {
  const messages = checkpointMessages();
  assert.deepEqual(contextUsage(messages, "4096", manualCheckpoint), { usedTokens: 512, limit: 4096, percent: 12.5, estimated: true });
  assert.deepEqual(contextUsage(messages, "default", manualCheckpoint), { usedTokens: 512, limit: null, percent: null, estimated: true });
  assert.equal(latestContextTokens(messages), 1024);
  assert.equal(messages.at(-1).metadata.prompt_eval_count, 800);
});

test("keeps the compaction estimate until a newer assistant has measured counts", () => {
  const messages = [...checkpointMessages(), { id: 5, role: "user", status: "complete" }, { id: 6, role: "assistant", status: "streaming" }];
  assert.equal(contextUsage(messages, "4096", manualCheckpoint).estimated, true);
  messages.at(-1).metadata = { prompt_eval_count: 600, eval_count: 40, agent_prompt_eval_count: 2000 };
  assert.deepEqual(contextUsage(messages, "4096", manualCheckpoint), { usedTokens: 640, limit: 4096, percent: 15.625 });
});

test("ignores automatic, missing, and stale checkpoint estimates after history deletion", () => {
  const messages = checkpointMessages();
  for (const checkpoint of [null, { ...manualCheckpoint, state: {} }, { ...manualCheckpoint, estimated_tokens: "invalid" }]) {
    assert.equal(contextUsage(messages, "4096", checkpoint).usedTokens, 1024);
    assert.equal(contextUsage(messages, "4096", checkpoint).estimated, undefined);
  }
  assert.equal(contextUsage(messages.slice(2), "4096", manualCheckpoint).estimated, undefined);
  assert.equal(contextUsage(messages.slice(0, 2), "4096", manualCheckpoint).estimated, undefined);
});
