// Templates are copied into a draft. No preference storage or saved session is
// touched here, so editing a template later cannot change a conversation.
export function templateDraft(template, current) {
  const settings = template.settings || {};
  const next = { ...current, modelSettings: { ...current.modelSettings, systemPrompt: template.system_prompt || "" } };
  if (Object.hasOwn(settings, "model")) next.model = settings.model;
  if (Object.hasOwn(settings, "mode")) next.modelSettings.conversationMode = settings.mode;
  if (Object.hasOwn(settings, "context_window")) next.modelSettings.contextWindow = settings.context_window == null ? "default" : String(settings.context_window);
  if (Object.hasOwn(settings, "thinking_mode")) next.modelSettings.thinkingMode = settings.thinking_mode ?? "default";
  if (Object.hasOwn(settings, "temperature")) {
    next.modelSettings.temperatureOverride = settings.temperature != null;
    next.modelSettings.temperature = settings.temperature ?? 0.7;
  }
  if (Object.hasOwn(settings, "web_search_enabled")) next.webSearchEnabled = settings.web_search_enabled;
  return next;
}

export function templateSettings(model, settings, webSearchEnabled) {
  return {
    ...(model ? { model } : {}),
    mode: settings.conversationMode,
    context_window: settings.contextWindow === "default" ? null : Number(settings.contextWindow),
    thinking_mode: settings.thinkingMode === "default" ? null : settings.thinkingMode,
    temperature: settings.temperatureOverride ? settings.temperature : null,
    web_search_enabled: webSearchEnabled,
  };
}

export function insertTemplateMessage(draft, message, insertion = "append") {
  if (insertion === "replace") return message;
  if (!message) return draft;
  return draft ? `${draft}\n\n${message}` : message;
}
