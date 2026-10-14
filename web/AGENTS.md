# Prototype Instructions

Run the local server yourself and open the preview in the browser available to this environment. Do not give the user server-start instructions when you can run it.

Before making substantial visual changes, use the Product Design plugin's `get-context` skill when the visual source is unclear or no longer matches the current goal. When the user gives durable prototype-specific design feedback, preferences, or decisions, record them in `AGENTS.md`.

When implementing from a selected generated mock, treat that image as the source of truth for layout, component anatomy, density, spacing, color, typography, visible content, and hierarchy.

## Selected design

- Assistant answers render Markdown with readable headings, emphasis, quotes, lists, code, and tables, including while streaming. Keep wide code and tables scrollable within the response.
- Preserve a native Select Markdown copy path for HTTP deployments using hosts-file names: select the original source for Ctrl+C/Cmd+C without depending on the Clipboard API.
- Copy Markdown and rendered code copying must work on http://ollama-webui.local: try the modern Clipboard API when available, then a user-initiated legacy copy fallback on HTTP. Preserve exact whitespace and Unicode, drafts, focus and selections; show Copied only when copying succeeds. Keep native Select actions for browsers that reject both copy methods.
- Rendered code blocks have floating Copy and Select buttons in the upper-right corner. Copy retains the descriptive accessible name Copy code. Select highlights only the code for native Ctrl+C/Cmd+C copying on HTTP deployments. Keep the buttons clear of code text and outside its horizontal scrolling region.
- In both Chat and Agent modes, pasting text larger than 16 KiB creates a visible, removable UTF-8 .txt attachment in the browser before sending. Preserve the exact pasted bytes and existing composer instructions. Text attachments work without vision capability. Server-side inline messages must not be silently converted to workspace files; Agent reads uploaded text in bounded ranges, while Chat receives its contents as text.
- Message inputs have a visible Drag to resize input bar above the field in new and existing Chat/Agent conversations. Drag up to enlarge and down to shrink, with pointer capture and Up/Down/Home/End keyboard controls; native corner resizing also remains available. Keep width fixed, retain a useful minimum height, and limit the input to half the current viewport height. Long drafts scroll inside the input; launch controls remain reachable on short screens.

- When a new user turn starts, scroll the transcript to the bottom after its new messages render.
- Existing sessions can switch models between responses using the header or conversation settings selector, including recovery from removed models. Preserve history and Chat/Agent mode, validate replacement capabilities, and reset thinking to the replacement model's default.

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
- Thinking sections show a live Thinking duration while reasoning streams and Thought for duration when it stops, in both modes. Persist measured time across reloads, accumulate main-agent thinking across model turns, and exclude model loading, prompt processing and tools. Stop/error freezes the timer; older replies without timing keep Model thinking rather than an invented duration. Keep the timer subtle, keyboard-accessible and readable on narrow screens.
- Conversation rename uses a focused modal with preselected current text, Save/Cancel/Escape behavior, inline errors, and focus restoration to the conversation menu.
- After the first successful reply in a new Chat or Agent conversation, the same model summarizes the first user input and reply into a short title in the user's language. Generate it in the background, update the browser tab and session history automatically, preserve manual names and existing sessions, and keep naming failures independent of reply success.
- Conversation deletion requires a focused confirmation modal with the affected title, initial focus on Cancel, explicit irreversible wording, inline errors, and one restrained danger-colored action.
- Conversation rename and deletion belong to each history row and must work without opening that conversation first; inactive-row actions must not disturb the active chat.
- Each history-row menu includes Generate title in both modes. It uses the latest completed user/final-answer interaction rather than agent/tool iterations, supports inactive sessions and manually named sessions, shows per-session progress/errors, and preserves names/history on errors. New responses save exact final-answer boundaries and their model; older replies rely on the naming prompt to focus on the final answer.
- Keep watching pending automatic titles until the server finishes, including slow first answers and model calls. Completed naming state and remembered manual names take priority over delayed pending list/detail responses without replacing newer turn data or recreating filtered/deleted sessions.
- Keep the active conversation's header menu as a shortcut for model settings, rename, and delete in addition to the row-level history menus.
- Context usage updates after each completed model turn during an agent run, using the latest Ollama prompt/output token counts. Keep the latest measured usage visible during tools and later model calls; persist it with the final or interrupted response. Use current-turn counts for context and aggregate counts for run performance. Show a percentage only for an explicitly known context limit; inherited limits show used tokens without false precision.
- Both Chat and Agent conversations offer a Compact context button next to context usage. Manual compaction can run below the automatic threshold, summarizes only older completed exchanges, keeps four recent completed messages and original transcript/files, and blocks conflicting operations while running. Show progress, no-op and error feedback; require a smaller persisted summary for success. Label post-compaction token counts as estimates until a later model turn reports measured counts, including after reload.
- Completed assistant replies show a compact response-performance summary (generation rate, output tokens, and model time) with prompt/load/turn details in an expandable disclosure; multi-turn agents aggregate all model calls.
- Every new conversation chooses Chat or Agent mode before the first message. Chat sends no tools and supports any completion model; Agent keeps shell access and requires an advertised tools capability. Non-tool models fall back to Chat automatically, and mode is locked with the conversation history.
- The empty conversation screen is a launch surface, not a stacked settings form: make Model the primary decision, present Chat and Agent as descriptive paired choices, and keep Context plus Thinking in a compact secondary runtime section.
- The user selected ImageGen direction 3 on 2026-09-03. New conversations use one composer-led launch dock: model and Chat/Agent form its header, the first prompt is the visual focus, and attachment, Context, Thinking, context status, and Send share its footer. After the first message, this same data flow collapses to the compact conversation composer.

Build app UI in `src/`. Keep `.openai/hosting.json`, `worker/index.js`, `scripts/prepare-sites-build.mjs`, and `tests/sites-worker.test.mjs` intact so the same local prototype can be handed to Sites. Before a Sites handoff, run `npm run build` and `npm run test:sites`; the build must leave `dist/client/index.html`, `dist/server/index.js`, and `dist/.openai/hosting.json`.
