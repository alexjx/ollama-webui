# Prototype Instructions

Run the local server yourself and open the preview in the browser available to this environment. Do not give the user server-start instructions when you can run it.

Before making substantial visual changes, use the Product Design plugin's `get-context` skill when the visual source is unclear or no longer matches the current goal. When the user gives durable prototype-specific design feedback, preferences, or decisions, record them in `AGENTS.md`.

When implementing from a selected generated mock, treat that image as the source of truth for layout, component anatomy, density, spacing, color, typography, visible content, and hierarchy.

## Selected design

- When a new user turn starts, scroll the transcript to the bottom after its new messages render.

- The user selected Option 1 on 2026-09-02.
- Source visual: `../docs/design/ollama-webui-option-1.png`.
- Preserve its light, restrained three-pane utility layout and blue/orange state palette.
- At narrower widths, conversations and model settings become overlay drawers; the chat remains the persistent surface.
- Untouched model parameters must visibly communicate that Ollama's defaults are inherited.
- Context window is chosen during new-chat setup, before the first message loads the model. It defaults to Ollama's inherited value and becomes read-only once that conversation's model is loaded.
- Thinking is visible in new-chat setup as Ollama default, Off, On, Low, Medium, High, or Max for capable models. It defaults to inherited Ollama behavior, explains that exact effort-level support is model-dependent, shows unsupported models clearly, and becomes read-only once the conversation starts.
- Agent mode is the primary interaction: use compact expandable shell traces, keep Stop prominent while work is active, and avoid making the transcript look like a full terminal emulator.
- Present the product as a general-purpose agent. Shell and workspace access are optional capabilities, not the default task or the primary visual identity.
- Agent progress uses observable phases rather than an invented percentage. Stream thinking in an expandable, visually secondary activity area above the final answer; retain chronological tool steps and clear finished/stopped states.
- Conversation rename uses a focused modal with preselected current text, Save/Cancel/Escape behavior, inline errors, and focus restoration to the conversation menu.
- Conversation deletion requires a focused confirmation modal with the affected title, initial focus on Cancel, explicit irreversible wording, inline errors, and one restrained danger-colored action.
- Conversation rename and deletion belong to each history row and must work without opening that conversation first; inactive-row actions must not disturb the active chat.
- Keep the active conversation's header menu as a shortcut for model settings, rename, and delete in addition to the row-level history menus.
- Context usage must come from the latest persisted Ollama prompt/output token counts. Show a percentage only for an explicitly known context limit; inherited limits show used tokens without false precision.
- Completed assistant replies show a compact response-performance summary (generation rate, output tokens, and model time) with prompt/load/turn details in an expandable disclosure; multi-turn agents aggregate all model calls.
- Every new conversation chooses Chat or Agent mode before the first message. Chat sends no tools and supports any completion model; Agent keeps shell access and requires an advertised tools capability. Non-tool models fall back to Chat automatically, and mode is locked with the conversation history.
- The empty conversation screen is a launch surface, not a stacked settings form: make Model the primary decision, present Chat and Agent as descriptive paired choices, and keep Context plus Thinking in a compact secondary runtime section.
- The user selected ImageGen direction 3 on 2026-09-03. New conversations use one composer-led launch dock: model and Chat/Agent form its header, the first prompt is the visual focus, and attachment, Context, Thinking, context status, and Send share its footer. After the first message, this same data flow collapses to the compact conversation composer.

Build app UI in `src/`. Keep `.openai/hosting.json`, `worker/index.js`, `scripts/prepare-sites-build.mjs`, and `tests/sites-worker.test.mjs` intact so the same local prototype can be handed to Sites. Before a Sites handoff, run `npm run build` and `npm run test:sites`; the build must leave `dist/client/index.html`, `dist/server/index.js`, and `dist/.openai/hosting.json`.
