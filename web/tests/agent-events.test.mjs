import assert from "node:assert/strict";
import test from "node:test";
import { activeSubagent, cancelRunningSubagents, reduceSubagentEvent, subagentProgressLabel } from "../src/agent-events.js";

const started = {
  id: 7,
  task: "Label images",
  status: "running",
  started_at: "2026-09-04T10:00:00Z",
  updated_at: "2026-09-04T10:00:00Z",
};

test("reduces a started child run into its completed result", () => {
  let runs = reduceSubagentEvent([], { type: "subagent.started", run: started });
  runs = reduceSubagentEvent(runs, {
    type: "subagent.done",
    run: { ...started, status: "complete", result_summary: "Labeled 4 images", output_refs: ["labels.jsonl"], completed_at: "2026-09-04T10:01:00Z", updated_at: "2026-09-04T10:01:00Z" },
  });

  assert.equal(runs.length, 1);
  assert.equal(runs[0].status, "complete");
  assert.equal(runs[0].result_summary, "Labeled 4 images");
});

test("does not let an older duplicate overwrite a terminal child run", () => {
  const complete = { ...started, status: "complete", completed_at: "2026-09-04T10:01:00Z", updated_at: "2026-09-04T10:01:00Z" };
  let runs = reduceSubagentEvent([complete], { type: "subagent.started", run: started });
  runs = reduceSubagentEvent(runs, {
    type: "subagent.failed",
    run: { ...started, status: "failed", error: "stale failure", completed_at: "2026-09-04T10:00:30Z", updated_at: "2026-09-04T10:00:30Z" },
  });

  assert.strictEqual(runs[0], complete);
  assert.equal(runs[0].status, "complete");
});

test("cancellation stops running and queued children while preserving finished runs", () => {
  const complete = { id: 8, task: "Already done", status: "complete" };
  const queued = { id: 9, task: "Waiting", status: "queued" };
  const runs = cancelRunningSubagents([started, complete, queued], "2026-09-04T10:02:00Z");

  assert.equal(runs[0].status, "cancelled");
  assert.equal(runs[0].completed_at, "2026-09-04T10:02:00Z");
  assert.strictEqual(runs[1], complete);
  assert.equal(runs[2].status, "cancelled");
});

test("child lifecycle exposes waiting, running phases and completion without parent text", () => {
  const message = { status: "streaming", content: "parent answer", subagent_runs: [] };
  message.subagent_runs = reduceSubagentEvent([], { type: "subagent.queued", run: { ...started, status: "queued" } });
  assert.match(subagentProgressLabel(activeSubagent(message)), /Waiting/);
  message.subagent_runs = reduceSubagentEvent(message.subagent_runs, { type: "subagent.started", run: started });
  message.subagent_runs = reduceSubagentEvent(message.subagent_runs, { type: "subagent.progress", run: started, content: JSON.stringify({ phase: "reading", turn: 2 }) });
  assert.equal(subagentProgressLabel(activeSubagent(message)), "Reading files · Turn 2");
  assert.equal(message.content, "parent answer");
  message.subagent_runs = reduceSubagentEvent(message.subagent_runs, { type: "subagent.done", run: { ...started, status: "complete" } });
  assert.equal(activeSubagent(message), null);
  assert.equal(activeSubagent({ ...message, status: "cancelled", subagent_runs: [started] }), null);
});

test("records bounded child progress without adding transcript content", () => {
  const runs = reduceSubagentEvent([started], {
    type: "subagent.progress",
    run: started,
    content: JSON.stringify({ processed: 20, total: 50, failed: 1, needs_review: 3 }),
  });

  assert.deepEqual(runs[0].progress, { processed: 20, total: 50, failed: 1, needs_review: 3 });
  assert.equal(runs[0].result_summary, undefined);
});
