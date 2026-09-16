const terminalStatuses = new Set(["complete", "failed", "cancelled"]);
const subagentEventTypes = new Set([
  "subagent.started",
  "subagent.done",
  "subagent.failed",
  "subagent.cancelled",
  "subagent.progress",
]);

function runTimestamp(run) {
  const value = run?.updated_at || run?.completed_at || run?.started_at || run?.created_at;
  const timestamp = value ? Date.parse(value) : Number.NaN;
  return Number.isNaN(timestamp) ? null : timestamp;
}

function shouldReplaceRun(current, incoming) {
  if (!current) return true;

  const currentTerminal = terminalStatuses.has(current.status);
  const incomingTerminal = terminalStatuses.has(incoming.status);
  if (currentTerminal && !incomingTerminal) return false;
  if (!currentTerminal && incomingTerminal) return true;

  const currentTimestamp = runTimestamp(current);
  const incomingTimestamp = runTimestamp(incoming);
  if (currentTimestamp != null && incomingTimestamp != null) {
    return incomingTimestamp >= currentTimestamp;
  }
  return !currentTerminal;
}

export function upsertSubagentRun(runs = [], incoming) {
  if (!incoming || incoming.id == null) return runs;
  const index = runs.findIndex((run) => run.id === incoming.id);
  if (index < 0) return [...runs, incoming];
  if (!shouldReplaceRun(runs[index], incoming)) return runs;

  const next = [...runs];
  next[index] = { ...runs[index], ...incoming };
  return next;
}

export function reduceSubagentEvent(runs = [], event) {
  if (!subagentEventTypes.has(event?.type)) return runs;
  let incoming = event.run;
  if (event.type === "subagent.progress" && incoming) {
    try {
      incoming = { ...incoming, progress: JSON.parse(event.content) };
    } catch {
      return runs;
    }
  }
  return upsertSubagentRun(runs, incoming);
}

export function cancelRunningSubagents(runs = [], completedAt = new Date().toISOString()) {
  return runs.map((run) => run.status === "running"
    ? { ...run, status: "cancelled", error: run.error || "Parent response stopped", completed_at: completedAt, updated_at: completedAt }
    : run);
}
