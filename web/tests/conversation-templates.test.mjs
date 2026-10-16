import assert from "node:assert/strict";
import test from "node:test";
import { templateDraft, templateSettings, insertTemplateMessage } from "../src/conversation-templates.js";
import { DEFAULT_MODEL_SETTINGS } from "../src/model-preferences.js";
import { listTemplates, createTemplate, updateTemplate, deleteTemplate } from "../src/api.js";

const current = Object.freeze({ model: "local-model", webSearchEnabled: true, modelSettings: Object.freeze({ ...DEFAULT_MODEL_SETTINGS, systemPrompt: "Previous instructions", contextWindow: "8192", thinkingMode: "high", temperatureOverride: true, temperature: 1.1 }) });

test("template draft copies explicit settings, inherits omitted ones, and leaves the original setup intact", () => {
  const template = Object.freeze({ system_prompt: "  Review carefully.\n", settings: Object.freeze({ mode: "chat", temperature: 0, web_search_enabled: false }) });
  const result = templateDraft(template, current);
  assert.equal(result.model, "local-model");
  assert.equal(result.modelSettings.contextWindow, "8192");
  assert.equal(result.modelSettings.thinkingMode, "high");
  assert.equal(result.modelSettings.systemPrompt, "  Review carefully.\n");
  assert.equal(result.modelSettings.conversationMode, "chat");
  assert.equal(result.modelSettings.temperature, 0);
  assert.equal(result.modelSettings.temperatureOverride, true);
  assert.equal(result.webSearchEnabled, false);
  assert.equal(current.modelSettings.systemPrompt, "Previous instructions");
  assert.equal(current.modelSettings.temperature, 1.1);
  result.modelSettings.systemPrompt = "Edited independently";
  assert.equal(template.system_prompt, "  Review carefully.\n");
});

test("explicit default overrides and unavailable models survive application for user review", () => {
  const result = templateDraft({ system_prompt: "", settings: { model: "removed-model", context_window: null, thinking_mode: null, temperature: null } }, current);
  assert.equal(result.model, "removed-model");
  assert.equal(result.modelSettings.contextWindow, "default");
  assert.equal(result.modelSettings.thinkingMode, "default");
  assert.equal(result.modelSettings.temperatureOverride, false);
  assert.equal(result.modelSettings.systemPrompt, "");
  assert.equal(result.webSearchEnabled, true);
});

test("template settings roundtrip exact model parameters, including a custom context size", () => {
  const settings = { ...DEFAULT_MODEL_SETTINGS, contextWindow: "12288", temperature: 0.65, temperatureOverride: true };
  const saved = templateSettings("qa-model", settings, false);
  assert.equal(saved.context_window, 12288);
  assert.equal(saved.thinking_mode, null);
  assert.equal(saved.temperature, 0.65);
  const restored = templateDraft({ system_prompt: settings.systemPrompt, settings: saved }, current);
  assert.deepEqual(restored, { model: "qa-model", modelSettings: settings, webSearchEnabled: false });
});

test("message insertion preserves existing whitespace and requires explicit replacement", () => {
  assert.equal(insertTemplateMessage("  My draft\n", " Prompt\n"), "  My draft\n\n\n Prompt\n");
  assert.equal(insertTemplateMessage("My draft", ""), "My draft");
  assert.equal(insertTemplateMessage("My draft", "New", "replace"), "New");
  assert.equal(insertTemplateMessage("", "  Prompt\n"), "  Prompt\n");
});

test("templates can capture setup before any model is installed", () => {
  const settings = templateSettings("", DEFAULT_MODEL_SETTINGS, true);
  assert.equal(Object.hasOwn(settings, "model"), false);
  assert.equal(templateDraft({ system_prompt: "Tutor", settings }, current).model, "local-model");
});

test("template CRUD API requests preserve prompts, expose errors, and never send messages", async (t) => {
  const calls = [];
  const input = { name: "Review", description: "", message: "  Review\n", system_prompt: "", settings: { context_window: null } };
  t.mock.method(globalThis, "fetch", async (path, options = {}) => {
    calls.push([path, options.method || "GET"]);
    if (options.body) assert.deepEqual(JSON.parse(options.body), input);
    if (options.method === "DELETE") return new Response(null, { status: 204 });
    return Response.json(options.method ? { id: 7, ...input } : { items: [{ id: 7, ...input }] });
  });
  assert.equal((await listTemplates())[0].message, input.message);
  assert.equal((await createTemplate(input)).id, 7);
  assert.equal((await updateTemplate(7, input)).id, 7);
  assert.equal(await deleteTemplate(7), null);
  assert.deepEqual(calls, [["/api/templates", "GET"], ["/api/templates", "POST"], ["/api/templates/7", "PUT"], ["/api/templates/7", "DELETE"]]);
  t.mock.method(globalThis, "fetch", async () => Response.json({ error: { message: "Template no longer exists" } }, { status: 404 }));
  await assert.rejects(updateTemplate(7, input), (error) => error.status === 404 && error.message === "Template no longer exists");
});
