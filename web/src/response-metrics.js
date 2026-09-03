function nonNegativeNumber(value) {
  return Number.isFinite(value) && value >= 0 ? value : null;
}

function count(value) {
  const number = nonNegativeNumber(value);
  return number == null ? null : Math.floor(number);
}

function seconds(value) {
  const nanoseconds = nonNegativeNumber(value);
  return nanoseconds == null ? null : nanoseconds / 1e9;
}

function preferred(metadata, aggregateKey, legacyKey, convert) {
  return convert(metadata?.[aggregateKey]) ?? convert(metadata?.[legacyKey]);
}

export function responseMetrics(metadata) {
  if (!metadata || typeof metadata !== "object") return null;
  const outputTokens = preferred(metadata, "agent_eval_count", "eval_count", count);
  const inputTokens = preferred(metadata, "agent_prompt_eval_count", "prompt_eval_count", count);
  const generationSeconds = preferred(metadata, "agent_eval_duration", "eval_duration", seconds);
  const promptSeconds = preferred(metadata, "agent_prompt_eval_duration", "prompt_eval_duration", seconds);
  const totalSeconds = preferred(metadata, "agent_total_duration", "total_duration", seconds);
  const loadSeconds = preferred(metadata, "agent_load_duration", "load_duration", seconds);
  const turns = count(metadata.agent_turns);
  const mode = metadata.run_mode === "chat" ? "chat" : "agent";
  const generationRate = outputTokens != null && generationSeconds > 0 ? outputTokens / generationSeconds : null;
  const promptRate = inputTokens != null && promptSeconds > 0 ? inputTokens / promptSeconds : null;
  const aggregate = ["agent_eval_count", "agent_prompt_eval_count", "agent_total_duration"].some((key) => nonNegativeNumber(metadata[key]) != null);

  if ([outputTokens, inputTokens, generationSeconds, promptSeconds, totalSeconds, loadSeconds].every((value) => value == null)) return null;
  return { outputTokens, inputTokens, generationRate, promptRate, totalSeconds, loadSeconds, turns, aggregate, mode };
}

export function formatRate(value) {
  if (!Number.isFinite(value) || value < 0) return null;
  const digits = value < 10 ? 2 : value < 100 ? 1 : 0;
  return `${value.toFixed(digits)} tok/s`;
}

export function formatDuration(value) {
  if (!Number.isFinite(value) || value < 0) return null;
  if (value < 1) return `${Math.round(value * 1000)} ms`;
  if (value < 60) return `${value < 10 ? value.toFixed(1) : Math.round(value)} s`;
  const minutes = Math.floor(value / 60);
  const remaining = Math.round(value % 60).toString().padStart(2, "0");
  return `${minutes}:${remaining}`;
}
