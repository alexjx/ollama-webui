function tokenCount(value) {
  return Number.isFinite(value) && value >= 0 ? Math.floor(value) : null;
}

export function latestContextTokens(messages) {
  for (let index = messages.length - 1; index >= 0; index -= 1) {
    const message = messages[index];
    if (message?.role !== "assistant" || !message.metadata) continue;
    const promptTokens = tokenCount(message.metadata.prompt_eval_count);
    const generatedTokens = tokenCount(message.metadata.eval_count);
    if (promptTokens == null && generatedTokens == null) continue;
    return (promptTokens || 0) + (generatedTokens || 0);
  }
  return 0;
}

export function contextUsage(messages, contextWindow, checkpoint = null) {
  let usedTokens = latestContextTokens(messages);
  const compactedAfter = checkpoint?.state?.compacted_after_message_id;
  const estimatedTokens = tokenCount(checkpoint?.estimated_tokens);
  const hasSnapshot = Number.isSafeInteger(compactedAfter) && compactedAfter > 0
    && estimatedTokens != null && checkpoint.summary?.trim()
    && messages.some((message) => message.id === compactedAfter)
    && messages.some((message) => message.id === checkpoint.through_message_id && message.role === "assistant" && message.status === "complete");
  const hasNewerMeasurement = hasSnapshot && messages.some((message) => message.role === "assistant"
    && message.id > compactedAfter && (tokenCount(message.metadata?.prompt_eval_count) != null || tokenCount(message.metadata?.eval_count) != null));
  const estimated = hasSnapshot && !hasNewerMeasurement;
  if (estimated) usedTokens = estimatedTokens;
  const parsedLimit = contextWindow === "default" ? null : Number(contextWindow);
  const limit = Number.isFinite(parsedLimit) && parsedLimit > 0 ? parsedLimit : null;
  return {
    usedTokens,
    limit,
    percent: limit == null ? null : Math.min(100, usedTokens / limit * 100),
    ...(estimated ? { estimated: true } : {}),
  };
}
