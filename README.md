# Ollama WebUI

A lightweight Chat and Agent interface for Ollama. Chat mode works with any
completion-capable model and never sends tools. Agent mode can use a shell for
calculation, search, inspection, execution, or verification when useful and
continues until the task is complete. The Go service streams both modes to a
responsive React interface and stores chats, model thinking, image attachments,
and tool traces in SQLite.

To protect short local-model context windows, Agent mode automatically replaces
an immutable prefix of long conversations with a durable checkpoint summary while
keeping recent messages verbatim. Original messages remain unchanged in SQLite.
Large captured shell outputs are stored under the managed context directory and
represented in later model turns by a bounded excerpt plus a conversation-scoped
artifact ID; the Agent can list and page through those artifacts when needed.
User messages above 16 KiB are still temporarily staged as private workspace files,
and prior thinking is never sent back to the model. Artifacts preserve the output
captured by the shell executor; output beyond `SHELL_MAX_OUTPUT_BYTES` is still
discarded at capture time and is marked as source-truncated.

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
stages. The final Alpine image keeps the small shell toolkit used by Agent mode,
but contains neither compiler nor frontend source. If `./workspace` is bind-mounted,
it must be writable by container UID 1000 so large-input staging and shell tasks work.

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
the same inherited-default behavior and is also fixed once a chat starts. Explicit
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
| `AGENT_INLINE_INPUT_BYTES` | `16384` | Largest user message kept inline; small context selections lower this to a 4 KiB floor |
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
or selected; JPEG, PNG, and WebP are accepted with limits of four images, 10 MiB
each, and 20 MiB total per message. Completed responses include expandable token,
timing, and generation-rate details.
Conversation rename and deletion are available from each row in the history list.
Markdown rendering, data import/export and retention controls, and advanced generation
parameters remain planned.
