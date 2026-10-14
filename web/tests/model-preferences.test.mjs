import assert from "node:assert/strict";
import test from "node:test";
import { createModelPreferences, DEFAULT_MODEL_SETTINGS, MODEL_PREFERENCES_KEY, normalizeModelSettings } from "../src/model-preferences.js";
import { listModels } from "../src/api.js";

const capabilities = ["tools", "thinking"];
function memoryStorage(initial) {
  const values = new Map(initial == null ? [] : [[MODEL_PREFERENCES_KEY, initial]]);
  return { getItem: (key) => values.get(key) ?? null, setItem: (key, value) => values.set(key, value), removeItem: (key) => values.delete(key) };
}

test("settings survive reload independently under each exact model name", () => {
  const storage = memoryStorage();
  const preferences = createModelPreferences(storage);
  preferences.set("qwen:small", { ...DEFAULT_MODEL_SETTINGS, contextWindow: "8192", thinkingMode: "high", systemPrompt: "  Use 中文🙂.\n", temperatureOverride: true, temperature: 0 });
  preferences.set("qwen:large", { ...DEFAULT_MODEL_SETTINGS, conversationMode: "chat", thinkingMode: "off", contextWindow: "65536", temperature: 1.3 });
  const reloaded = createModelPreferences(storage);
  assert.equal(reloaded.get("qwen:small", capabilities).contextWindow, "8192");
  assert.equal(reloaded.get("qwen:small", capabilities).systemPrompt, "  Use 中文🙂.\n");
  assert.equal(reloaded.get("qwen:small", capabilities).temperature, 0);
  assert.equal(reloaded.get("qwen:large", capabilities).conversationMode, "chat");
  assert.equal(reloaded.get("qwen:large", capabilities).thinkingMode, "off");
  assert.deepEqual(reloaded.get("unseen", capabilities), DEFAULT_MODEL_SETTINGS);
});

test("only edited fields update preferences rather than copying historical siblings", () => {
  const preferences = createModelPreferences(memoryStorage());
  preferences.set("model", { ...DEFAULT_MODEL_SETTINGS, contextWindow: "8192", systemPrompt: "Preferred prompt", thinkingMode: "high" });
  preferences.update("model", { temperatureOverride: true, temperature: 1.2 });
  assert.deepEqual(preferences.get("model", capabilities), { ...DEFAULT_MODEL_SETTINGS, contextWindow: "8192", systemPrompt: "Preferred prompt", thinkingMode: "high", temperatureOverride: true, temperature: 1.2 });
});

test("deleting an installed model removes only its preferences and reinstall uses defaults", () => {
  const storage = memoryStorage();
  const preferences = createModelPreferences(storage);
  preferences.update("model:1", { contextWindow: "8192" });
  preferences.update("model:2", { thinkingMode: "high" });
  preferences.prune(["model:2"]);
  let saved = JSON.parse(storage.getItem(MODEL_PREFERENCES_KEY)).models;
  assert.equal(Object.hasOwn(saved, "model:1"), false);
  assert.equal(Object.hasOwn(saved, "model:2"), true);
  preferences.prune(["model:1", "model:2"]);
  assert.deepEqual(preferences.get("model:1", capabilities), DEFAULT_MODEL_SETTINGS);
  assert.equal(createModelPreferences(storage).get("model:2", capabilities).thinkingMode, "high");
  preferences.prune([]);
  assert.equal(storage.getItem(MODEL_PREFERENCES_KEY), null);
});

test("reset clears just one model and remains reset after reload", () => {
  const storage = memoryStorage();
  const preferences = createModelPreferences(storage);
  preferences.update("a", { contextWindow: "8192" });
  preferences.update("b", { contextWindow: "32768" });
  preferences.clear("a");
  const reloaded = createModelPreferences(storage);
  assert.deepEqual(reloaded.get("a", capabilities), DEFAULT_MODEL_SETTINGS);
  assert.equal(reloaded.get("b", capabilities).contextWindow, "32768");
});

test("capability fallback does not erase remembered user intent", () => {
  const preferences = createModelPreferences(memoryStorage());
  preferences.update("model", { conversationMode: "agent", thinkingMode: "on" });
  assert.equal(preferences.get("model", []).conversationMode, "chat");
  assert.equal(preferences.get("model", []).thinkingMode, "default");
  preferences.update("model", { contextWindow: "8192" });
  assert.equal(preferences.get("model", capabilities).conversationMode, "agent");
  assert.equal(preferences.get("model", capabilities).thinkingMode, "on");
});

test("malformed storage and invalid fields fall back to validated defaults", () => {
  for (const value of ["bad JSON", "null", "[]", JSON.stringify({ version: 2, models: { a: { contextWindow: "8192" } } })]) {
    assert.deepEqual(createModelPreferences(memoryStorage(value)).get("a", capabilities), DEFAULT_MODEL_SETTINGS);
  }
  assert.deepEqual(normalizeModelSettings({ conversationMode: "auto", contextWindow: "-1", thinkingMode: "extreme", systemPrompt: [], temperatureOverride: "true", temperature: 9, serverSecret: "must not persist" }), DEFAULT_MODEL_SETTINGS);
  for (const value of [-1, Infinity, NaN, "0.5"]) assert.equal(normalizeModelSettings({ temperature: value }).temperature, 0.7);
});

test("blocked and full storage keep page settings usable", () => {
  const storage = { getItem() { throw new Error("blocked"); }, setItem() { throw new Error("quota"); }, removeItem() { throw new Error("blocked"); } };
  const preferences = createModelPreferences(storage);
  preferences.update("a", { contextWindow: "8192" });
  assert.equal(preferences.get("a", capabilities).contextWindow, "8192");
  preferences.clear("a");
  assert.deepEqual(preferences.get("a", capabilities), DEFAULT_MODEL_SETTINGS);
});

test("model names cannot access inherited object properties", () => {
  const storage = memoryStorage();
  const preferences = createModelPreferences(storage);
  for (const name of ["__proto__", "constructor", "toString"]) {
    assert.deepEqual(preferences.get(name, capabilities), DEFAULT_MODEL_SETTINGS);
    preferences.update(name, { contextWindow: "8192" });
  }
  const reloaded = createModelPreferences(storage);
  for (const name of ["__proto__", "constructor", "toString"]) assert.equal(reloaded.get(name, capabilities).contextWindow, "8192");
});

test("only a valid model list can confirm deletion, including a valid empty list", async (t) => {
  for (const payload of [{}, { models: null }, { models: {} }, { models: [null] }, { models: [{ name: 1 }] }, { models: [{ name: " " }] }]) {
    t.mock.method(globalThis, "fetch", async () => Response.json(payload));
    await assert.rejects(listModels(), /invalid model list/);
    t.mock.restoreAll();
  }
  for (const models of [[], [{ name: "model:tag", capabilities }]]) {
    t.mock.method(globalThis, "fetch", async () => Response.json({ models }));
    assert.deepEqual(await listModels(), models);
    t.mock.restoreAll();
  }
  t.mock.method(globalThis, "fetch", async () => Response.json({ error: { message: "Ollama offline" } }, { status: 502 }));
  await assert.rejects(listModels(), /Ollama offline/);
});

test("selected model survives reload and settings reset without requiring custom settings", () => {
  const storage = memoryStorage();
  const preferences = createModelPreferences(storage);
  preferences.select("plain:latest");
  assert.equal(createModelPreferences(storage).getSelectedModel(), "plain:latest");
  preferences.update("plain:latest", { contextWindow: "8192" });
  preferences.clear("plain:latest");
  assert.equal(createModelPreferences(storage).getSelectedModel(), "plain:latest");
  preferences.get("historical:model", capabilities);
  preferences.update("another:model", { thinkingMode: "high" });
  assert.equal(preferences.getSelectedModel(), "plain:latest");
});

test("only pruning an unavailable selection clears it; older storage remains compatible", () => {
  const storage = memoryStorage(JSON.stringify({ version: 1, models: { a: { contextWindow: "8192" } } }));
  const preferences = createModelPreferences(storage);
  assert.equal(preferences.getSelectedModel(), "");
  assert.equal(preferences.get("a", capabilities).contextWindow, "8192");
  preferences.select("a");
  preferences.prune(["a", "b"]);
  assert.equal(createModelPreferences(storage).getSelectedModel(), "a");
  preferences.select("b");
  preferences.prune(["a"]);
  assert.equal(createModelPreferences(storage).getSelectedModel(), "");
  assert.equal(preferences.get("a", capabilities).contextWindow, "8192");
});

test("invalid selected models are ignored and blocked storage preserves selection in memory", () => {
  for (const selectedModel of [null, {}, 1, "", "   "]) {
    const preferences = createModelPreferences(memoryStorage(JSON.stringify({ version: 1, selectedModel, models: {} })));
    assert.equal(preferences.getSelectedModel(), "");
    preferences.select(selectedModel);
    assert.equal(preferences.getSelectedModel(), "");
  }
  const preferences = createModelPreferences({ getItem() { throw Error("blocked"); }, setItem() { throw Error("quota"); } });
  preferences.select("a");
  assert.equal(preferences.getSelectedModel(), "a");
});
