# Prototype Instructions

Run the local server yourself and open the preview in the browser available to this environment. Do not give the user server-start instructions when you can run it.

Before making substantial visual changes, use the Product Design plugin's `get-context` skill when the visual source is unclear or no longer matches the current goal. When the user gives durable prototype-specific design feedback, preferences, or decisions, record them in `AGENTS.md`.

When implementing from a selected generated mock, treat that image as the source of truth for layout, component anatomy, density, spacing, color, typography, visible content, and hierarchy.

## Selected design

- The user selected Option 1 on 2026-09-02.
- Source visual: `../docs/design/ollama-webui-option-1.png`.
- Preserve its light, restrained three-pane utility layout and blue/orange state palette.
- At narrower widths, conversations and model settings become overlay drawers; the chat remains the persistent surface.
- Untouched model parameters must visibly communicate that Ollama's defaults are inherited.
- Context window is chosen during new-chat setup, before the first message loads the model. It defaults to Ollama's inherited value and becomes read-only once that conversation's model is loaded.

Build app UI in `src/`. Keep `.openai/hosting.json`, `worker/index.js`, `scripts/prepare-sites-build.mjs`, and `tests/sites-worker.test.mjs` intact so the same local prototype can be handed to Sites. Before a Sites handoff, run `npm run build` and `npm run test:sites`; the build must leave `dist/client/index.html`, `dist/server/index.js`, and `dist/.openai/hosting.json`.
