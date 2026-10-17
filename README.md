# Ollama WebUI

A lightweight Chat and Agent interface for Ollama. Chat mode works with any
completion-capable model and never sends tools. Agent mode can use a shell for
calculation, search, inspection, execution, or verification when useful and
continues until the task is complete. The Go service streams both modes to a
responsive React interface and stores chats, model thinking, image and text attachments,
and tool traces in SQLite.

New replies display thinking, response text, tool calls and child tasks in the
order they occur, with round markers when the agent continues. Thinking and tool
details remain expandable. Each text segment has its own copy and selection
actions after completion. The view follows new output while you are near the
bottom and lets you scroll up to read earlier output. The sequence persists
across reloads and forks, including stopped replies. Older replies retain their
existing grouped display; historical boundaries are not reconstructed.

Thinking sections show a live duration and retain **Thought for …** after the
reply finishes or stops. Time is measured from the first thinking output to the
answer or end of each reasoning phase, shown per block for new replies;
model loading, prompt processing and tool execution are excluded. Older saved
replies without a measured duration keep their existing thinking label.

New sessions reuse your browser's saved settings for each exact model name:
Chat/Agent mode, context window, thinking, system prompt and temperature. These
preferences stay in this browser; existing sessions retain their saved settings.
**Reset to model defaults** clears the selected model's preferences, and a
successful model-list refresh removes preferences for models deleted from Ollama.

Open **Templates** in the sidebar or **Choose template** beside the message input
to create, search, preview, edit, duplicate, or delete reusable conversation
templates. Each template has a name, optional description, message and/or system
instructions, and optional new-chat settings. **Conversation menu → Save as
template** starts an editable template from the current settings and lets you
choose a saved user message. Responses, attachments, and tool history are excluded.

**Use in new chat** loads a template into an editable draft, including its system
instructions and any saved settings. Unspecified settings inherit the current
setup; blank system instructions use the model's default. **Insert message** adds
only the template's message and preserves the current settings and conversation.
If a draft already exists, choose **Append** (the default) or **Replace draft
text**; attachments are kept. Nothing is sent until you press **Send**. Missing or
incompatible models require choosing a suitable model or adjusting the draft
settings before sending. Applying a template does not change remembered browser
defaults. Templates are stored in the server database and share the same access
scope as conversations. Editing or deleting a template does not change existing
conversations, and clearing conversation history preserves the template library.

To enable web search, open **Settings → Web search**, enter your **Tavily API
token**, enable **Web search in Agent mode**, and choose **Save**. **Test search**
checks the saved token with one basic search. Agent models can then call
`web_search` and use result titles, links and snippets to cite their sources.
The integration uses only Tavily's [Search API](https://docs.tavily.com/documentation/api-reference/endpoint/search)
with Bearer authentication; generated answers, raw page content, images and
automatic advanced search are disabled. Chat stays tool-free.

**Conversation settings → Web search** controls search separately for each Agent
session, including between replies. The switch is saved with the session and
also applies to delegated agents. It defaults to on; search still requires the
enabled Tavily configuration above. Turning it off preserves existing results.

The token and enabled setting persist in the local server database independently
of conversations. Settings responses expose only whether a token is configured,
and the token is kept out of browser preferences, model requests and tool traces.
**Remove token** disables search; clearing conversation history keeps the search
configuration. Search calls use the latest saved configuration and respect Stop.

After the first turn finishes successfully in a new Chat or Agent conversation, the same
model creates a short title from the first user input and its reply. Naming runs
in the background and updates the browser tab and session history automatically.
Manual renames take priority; existing sessions keep their names, and a naming
failure keeps the initial title without affecting the saved reply.
Title requests ask the same model to turn thinking off and allow reasoning to
finish on models that still require it, so reasoning cannot consume a short
title-only token allowance before an answer appears.

Use **Copy** on a rendered code block or **Copy Markdown** below an answer
to copy text on HTTP deployments such as `http://ollama-webui.local`. These actions
fall back when the modern Clipboard API is unavailable. **Select** actions remain
available for native Ctrl+C/Cmd+C copying when browser copying is denied.

Use **Generate title** in any session's history-row menu to
replace its name using the latest completed user question and final answer.
This works in both modes without opening the session. New Agent replies keep an
exact final-answer boundary so progress narration is excluded; older saved
replies ask the title model to focus on the final answer. Failed or stopped turns
are skipped, and naming errors keep the current name and saved conversation.
Automatic names keep updating in the browser until generation finishes; slow
model calls and older list responses do not leave the provisional name behind.

To protect short local-model context windows, Agent mode automatically replaces
an immutable prefix of long conversations with a durable checkpoint summary while
keeping recent messages verbatim. Original messages remain unchanged in SQLite.
Large captured shell outputs are stored under the managed context directory and
represented in later model turns by a bounded excerpt plus a conversation-scoped
artifact ID; the Agent can list and page through those artifacts when needed.
Pasted text above 16 KiB becomes a UTF-8 text attachment in the browser before
sending. Existing composer text stays inline. Chat mode sends labelled file text
to the model; Agent mode receives file references and reads relevant ranges with
conversation-scoped attachment tools. Attachment reads have no fixed byte cap:
the Agent chooses a range to fit its available context, or omits the limit to
read the remaining file. Requested text reaches the model without tool-feedback
clipping. Raw inline messages are never staged to
workspace files. Prior thinking is never sent back to the model. Artifacts preserve the output
captured by the shell executor; output beyond `SHELL_MAX_OUTPUT_BYTES` is still
discarded at capture time and is marked as source-truncated.

Use **Compact** next to context usage in Chat or Agent mode to summarize older
completed exchanges before the automatic threshold is reached. It keeps the
four most recent completed messages and the original saved transcript and files. The next reply
uses the saved summary; context usage shows an estimate until the next model call
reports measured tokens. Short histories need no compaction, and summarization
errors keep the previous context available.

Agent mode can delegate focused work to child agents. Each child starts with a
private system-and-task context instead of inheriting the full conversation, and
only a bounded summary plus verified workspace output paths returns to the parent.
For directory image labeling, the dedicated batch tool performs one stateless
vision request per image and atomically writes JSONL under the workspace, so prior
images and raw per-image reasoning never accumulate in the main prompt.

New-chat setup offers **Ollama default**, **Off**, **On**, and the native
**Low**, **Medium**, **High**, and **Max** effort levels for models that advertise
Ollama's `thinking` capability. Ollama default omits the `think` field; On and Off
send booleans, while effort levels pass through as strings. The choice is stored
with the conversation, and live reasoning appears in an expandable panel separate
from the final answer. Ollama does not publish each model's exact supported effort
levels, so unsupported combinations may still be rejected by the model. Models
without the broad capability show Thinking as unsupported.

Existing sessions can switch models from the header or **Conversation settings**
between responses, including when the previous model has been removed. History,
Chat/Agent mode, and other settings are preserved; thinking resets to the new
model's default. Agent sessions require a tool-capable replacement, and sessions
with images require a model that supports those images.

Use **Fork conversation** in the conversation menu or a history row's menu to
continue from a copy of the entire saved session. The fork keeps the model and
settings, messages, attachments, reasoning/tool history, and compacted Agent
context, while giving the copied records and stored context files independent
ownership. Deleting either session leaves the other intact. Wait for an active
response or compaction to finish before forking. Shared workspace files remain
shared; forking does not snapshot or undo shell side effects.

Open **Workspace** in the top bar to browse the shared Agent workspace from the
web. Navigate folders, preview text and raster images, refresh after Agent changes,
and download files. The browser is read-only; text previews show at most 256 KiB.
Links and special files cannot be opened, and files outside the configured
workspace are not exposed. Workspace files are shared across sessions.

Assistant replies recognize explicit workspace paths (`./reports/result.csv` or an
absolute path inside the configured workspace), plus paths in inline code and
local Markdown links. Click the subtly underlined path to open its folder or file
preview in Workspace. Paths containing spaces should use inline code or a Markdown
link. Message source and Markdown/code copying stay unchanged; files are checked
by the server when opened. Workspace links can also be opened in a new tab. Code
blocks and external links are left unchanged.

## Optional shell access: safety boundary

The agent can execute arbitrary shell commands. Commands may read paths visible to
the WebUI process when normal operating-system permissions allow it, but Linux
Landlock restricts file-content and directory-structure writes to
`AGENT_WORKSPACE`. The command is rejected before execution when Landlock ABI 3 or
newer is unavailable. In Docker, “system paths” means the container filesystem and
explicitly mounted host paths; unmounted host paths are not visible.

- Never mount the Docker socket, SSH keys, cloud credentials, or other secrets
  into the `webui` container.
- Only mount system paths whose contents the model is allowed to read. Mount the
  sole writable working tree at `AGENT_WORKSPACE`.
- The container retains only the startup capabilities needed to set volume
  ownership and switch to UID/GID 1000, blocks privilege escalation, and runs the
  WebUI and commands as that unprivileged user. Landlock does not mediate a few
  metadata-only operations such as timestamps, mode bits, and extended attributes.
  Device ioctls are denied when the host provides Landlock ABI 5 or newer; on ABI
  3–4, do not expose devices or sensitive writable files owned by UID 1000 outside
  the workspace.
- Shell commands are shown in the conversation and can be interrupted with
  **Stop**. Child processes are terminated with the active command.

When running the Go service directly, commands retain the current user's normal
read access while the same fail-closed Landlock write policy applies. Landlock is
a filesystem boundary, not a process, network, or secret-redaction boundary.

## Run with Docker Compose

```sh
docker compose up --build
```

Open <http://localhost:8080>. Ollama models are stored in the `ollama-data`
volume, chats in `webui-data`, and the host `./workspace` directory is mounted at
`/workspace`. Agent mode requires a model that advertises Ollama's `tools`
capability, for example:

```sh
docker compose exec ollama ollama pull qwen3:8b
```

The default image is multi-stage: Node and Go toolchains exist only in build
stages. The final Alpine image includes Python 3, pip, uv and the shell toolkit used by Agent mode,
but contains neither compiler nor frontend source. If `./workspace` is bind-mounted,
it must be writable by container UID 1000 so shell tasks work.

Agent mode can run Python through its existing shell tool. Prefer `uv run script.py`
or create a workspace-local environment with `uv venv .venv` and install packages
with `uv pip install --python .venv/bin/python PACKAGE`. Without uv, use
`python3 -m venv .venv`, `.venv/bin/python -m pip install PACKAGE`, then
`.venv/bin/python script.py`. Reuse an existing project environment when present.
Keep scripts, environments and outputs inside the workspace; caches and temporary
files also stay there through the agent shell's existing environment settings.
Workspace files persist across container replacement when mounted. Package downloads
need network access. Native extensions without compatible Alpine wheels may need
additional build dependencies in a custom image; compilers are not bundled.

## Container releases

GitHub Actions runs the full test suite and a container smoke test for pull requests
and main-branch pushes. Images are published to GitHub Container Registry only when
a semantic version tag beginning with `v` is pushed:

```sh
git tag v1.2.3
git push origin v1.2.3
```

Stable `v1.2.3` publishes `1.2.3`, `1.2`, `1`, and `latest` tags. A prerelease such
as `v1.2.3-rc.1` publishes only prerelease-safe version tags and never changes
`latest`. Every release contains `linux/amd64` and `linux/arm64` images plus an SBOM
and build provenance. The image name follows the repository automatically:

```text
ghcr.io/<owner>/<repository>:1.2.3
```

For an existing Ollama server on the Docker host, run the published image with:

```sh
docker run -d --name ollama-webui \
  --add-host host.docker.internal:host-gateway \
  -p 8080:8080 \
  -e OLLAMA_BASE_URL=http://host.docker.internal:11434 \
  -v ollama-webui-data:/data \
  -v "$PWD/workspace:/workspace" \
  ghcr.io/<owner>/<repository>:1.2.3
```

Publishing uses the workflow's built-in `GITHUB_TOKEN`; no registry password is
required. GHCR package visibility is controlled separately in the package settings,
so make the package public there if anonymous pulls should work.

The WebUI never sends `keep_alive` unless a future explicit override is added,
so Ollama remains responsible for model lifetime. In Chat mode, choosing
**Ollama default** for the context window omits `num_ctx`. Agent mode instead uses
the managed context budget unless an explicit per-conversation value is selected;
that value is saved with the conversation and sent on every turn. The Thinking setting follows
the same inherited-default behavior and is fixed once a chat starts, unless switching
models resets it to the new model's default. Explicit
effort levels are model-dependent; **Ollama default** is the safest portable choice.

The sidebar **Settings** entry shows application-wide runtime and storage status,
including the effective Agent limits configured by Compose and local SQLite counts.
These startup values are read-only in the WebUI: update the environment variables
below and recreate the WebUI container to change them. Per-chat model controls live
under **Conversation settings** in the composer or conversation menu. System settings
also provides a guarded **Clear all** action; it requires typing `DELETE`, refuses to
run while a response is active, removes database records in one transaction, and
then removes the corresponding managed context directories. Startup reconciliation
cleans up an interrupted filesystem removal. Back up both `DATABASE_PATH` and
`AGENT_CONTEXT_PATH` together when artifact recovery must remain consistent.

## Local development

Build the frontend, then run the Go service:

```sh
cd web
npm ci
npm run build
cd ..
go run ./cmd/server
```

Configuration is provided with environment variables:

| Variable | Default | Purpose |
| --- | --- | --- |
| `LISTEN_ADDR` | `:8080` | HTTP listen address |
| `OLLAMA_BASE_URL` | `http://localhost:11434` | Ollama API base URL |
| `DATABASE_PATH` | `./data/ollama-webui.db` | SQLite database path |
| `WEB_DIST_DIR` | `./web/dist/client` | Built frontend directory |
| `AGENT_WORKSPACE` | `./workspace` | Shell starting directory |
| `AGENT_CONTEXT_PATH` | Next to `DATABASE_PATH` under `agent-context` | Private managed checkpoint artifact directory; the container image sets `/data/agent-context` |
| `AGENT_MAX_TURNS` | `200` | Emergency ceiling on model turns per task |
| `AGENT_CONTEXT_BUDGET_TOKENS` | `32768` | Conservative prompt budget when a conversation does not set an explicit context window; compaction starts near 70% |
| `AGENT_TOOL_FEEDBACK_BYTES` | `8192` | Maximum shell-output excerpt returned to later model turns |
| `AGENT_SUBAGENTS_ENABLED` | `true` | Expose isolated child-agent and image-batch tools |
| `AGENT_SUBAGENT_CONCURRENCY` | `1` | Maximum child jobs running across conversations; increase only when Ollama and available memory can sustain it |
| `AGENT_SUBAGENT_CONTEXT_TOKENS` | `8192` | Context window used by each isolated child |
| `AGENT_SUBAGENT_MAX_TURNS` | `20` | Emergency model-turn ceiling for each child |
| `AGENT_SUBAGENT_RESULT_BYTES` | `4096` | Maximum child summary returned to the parent prompt |
| `SHELL_TIMEOUT_SECONDS` | `600` | Maximum time for each shell command |
| `SHELL_MAX_OUTPUT_BYTES` | `65536` | Combined stdout/stderr retained per command |

Compose also accepts `AGENT_WORKSPACE_PATH` to select the host directory mounted
at `/workspace`. The agent has no overall task timeout; it continues until the
model returns a final response, you press Stop, or the emergency turn ceiling is
reached. Models without the `tools` capability automatically use Chat mode and
remain fully usable for ordinary conversations. Tool-capable models can use
either Chat or Agent mode; the choice is fixed after the conversation starts.

## Current scope

The daily-use core now supports model discovery, ordinary Chat and general Agent
tasks with optional shell use, isolated child delegation, directory image labeling, new
conversations, context and temperature overrides before first load, streamed
responses with Stop, durable history and shell traces, title/message search, and
persisted image input for vision-capable models. Images may be pasted, dropped,
or selected; JPEG, PNG, and WebP are accepted. UTF-8 plain text files can also be
selected or dropped, and large pasted text becomes a file in the browser. Images
and text files share limits of four attachments, 10 MiB
each, and 20 MiB total per message. Completed responses include expandable token,
timing, and generation-rate details.
Conversation rename and deletion are available from each row in the history list.
Markdown rendering, data import/export and retention controls, and advanced generation
parameters remain planned.
