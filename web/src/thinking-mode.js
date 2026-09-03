const explicitModes = new Set(["off", "on", "low", "medium", "high", "max"]);

export function thinkingModeFromConversation(conversation) {
  if (explicitModes.has(conversation?.thinking_mode)) return conversation.thinking_mode;
  if (conversation?.thinking_enabled === true) return "on";
  if (conversation?.thinking_enabled === false) return "off";
  return "default";
}

export function thinkingModeForRequest(mode) {
  return explicitModes.has(mode) ? mode : null;
}
