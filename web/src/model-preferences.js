export const MODEL_PREFERENCES_KEY = "ollama-webui.model-preferences.v1";
export const CONTEXT_WINDOW_VALUES = ["default", "4096", "8192", "16384", "32768", "65536", "131072"];
export const DEFAULT_MODEL_SETTINGS = Object.freeze({
  conversationMode: "agent",
  contextWindow: "default",
  thinkingMode: "default",
  systemPrompt: "",
  temperatureOverride: false,
  temperature: 0.7,
});

export function normalizeModelSettings(value) {
  const settings = value && typeof value === "object" ? value : {};
  return {
    conversationMode: settings.conversationMode === "chat" ? "chat" : "agent",
    contextWindow: CONTEXT_WINDOW_VALUES.includes(settings.contextWindow) ? settings.contextWindow : "default",
    thinkingMode: ["default", "off", "on", "low", "medium", "high", "max"].includes(settings.thinkingMode) ? settings.thinkingMode : "default",
    systemPrompt: typeof settings.systemPrompt === "string" ? settings.systemPrompt : "",
    temperatureOverride: settings.temperatureOverride === true,
    temperature: Number.isFinite(settings.temperature) && settings.temperature >= 0 && settings.temperature <= 2 ? settings.temperature : 0.7,
  };
}

export function modelSettingsForCapabilities(settings, capabilities = []) {
  const result = normalizeModelSettings(settings);
  if (!capabilities.includes("tools")) result.conversationMode = "chat";
  if (!capabilities.includes("thinking")) result.thinkingMode = "default";
  return result;
}

// Keep an in-memory copy so blocked or full browser storage cannot break setup.
export function createModelPreferences(storage) {
  const models = new Map();
  let selectedModel = "";
  try {
    if (storage === undefined) storage = globalThis.localStorage;
    const saved = JSON.parse(storage?.getItem(MODEL_PREFERENCES_KEY) || "null");
    if (saved?.version === 1 && saved.models && typeof saved.models === "object" && !Array.isArray(saved.models)) {
      if (typeof saved.selectedModel === "string" && saved.selectedModel.trim()) selectedModel = saved.selectedModel;
      for (const [name, settings] of Object.entries(saved.models)) {
        if (name.trim()) models.set(name, normalizeModelSettings(settings));
      }
    }
  } catch {
    // Corrupt data or a disabled localStorage API falls back to model defaults.
  }

  function persist() {
    try {
      if (models.size || selectedModel) storage?.setItem(MODEL_PREFERENCES_KEY, JSON.stringify({ version: 1, selectedModel, models: Object.fromEntries(models) }));
      else storage?.removeItem(MODEL_PREFERENCES_KEY);
    } catch {
      // Settings remain usable for this page even when persistence is unavailable.
    }
  }

  function save(name, settings) {
    if (typeof name !== "string" || !name.trim()) return;
    models.set(name, normalizeModelSettings(settings));
    persist();
  }

  return {
    getSelectedModel() { return selectedModel; },
    select(name) {
      if (typeof name !== "string" || !name.trim()) return;
      selectedModel = name;
      persist();
    },
    get(name, capabilities) {
      return modelSettingsForCapabilities(models.get(name), capabilities);
    },
    set: save,
    update(name, patch) {
      // Save the chosen field, not sibling values loaded from an older session.
      save(name, { ...models.get(name), ...patch });
    },
    clear(name) {
      if (models.delete(name)) persist();
    },
    prune(installedNames) {
      const installed = new Set(installedNames);
      let changed = false;
      if (selectedModel && !installed.has(selectedModel)) { selectedModel = ""; changed = true; }
      for (const name of models.keys()) {
        if (!installed.has(name)) { models.delete(name); changed = true; }
      }
      if (changed) persist();
    },
  };
}
