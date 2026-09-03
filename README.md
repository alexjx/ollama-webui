# Ollama WebUI

A lightweight Chat and Agent interface for Ollama. Chat mode works with any
completion-capable model and never sends tools. Agent mode can use a shell for
calculation, search, inspection, execution, or verification when useful and
continues until the task is complete. The Go service streams both modes to a
responsive React interface and stores chats, model thinking, image attachments,
and tool traces in SQLite.

To protect short local-model context windows, user messages above 16 KiB are
temporarily staged as private files in the mounted workspace. Ollama receives a
small reference and can inspect the source in bounded line ranges rather than
receiving the whole text again on every agent turn. The original message remains
unchanged in SQLite, staged files are removed when the run ends, prior thinking
is not sent back to the model, and shell feedback sent to later turns is capped
separately from the fuller activity log.

New-chat setup offers **Ollama default**, **Off**, **On**, and the native
**Low**, **Medium**, **High**, and **Max** effort levels for models that advertise
Ollama's `thinking` capability. Ollama default omits the `think` field; On and Off
send booleans, while effort levels pass through as strings. The choice is stored
with the conversation, and live reasoning appears in an expandable panel separate
from the final answer. Ollama does not publish each model's exact supported effort
levels, so unsupported combinations may still be rejected by the model. Models
without the broad capability show Thinking as unsupported.

## Optional shell access: safety boundary

The agent can execute arbitrary shell commands. `/workspace` is its starting
directory, **not a security sandbox**: commands can access any file that the
`webui` container user can read or write, including the chat database in `/data`.
Docker is the security boundary.

- Never mount the Docker socket, SSH keys, cloud credentials, or other secrets
  into the `webui` container.
- Only mount a workspace whose contents the model is allowed to read and change.
- The container retains only the startup capabilities needed to set volume
  ownership and switch to UID/GID 1000, blocks privilege escalation, and runs the
  WebUI and commands as that unprivileged user. Those measures do not make
  untrusted model output safe.
- Shell commands are shown in the conversation and can be interrupted with
  **Stop**. Child processes are terminated with the active command.

When running the Go service directly for local development, there is no container
boundary: shell commands run as your current operating-system user and can reach
that user's files and processes. Use Compose for the safer default deployment.

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
so Ollama remains responsible for model lifetime. Choosing **Ollama default** for
the context window similarly omits `num_ctx`; an explicit context selection is
saved with the conversation and sent on every turn. The Thinking setting follows
the same inherited-default behavior and is also fixed once a chat starts. Explicit
effort levels are model-dependent; **Ollama default** is the safest portable choice.

The sidebar **Settings** entry shows application-wide runtime and storage status,
including the effective Agent limits configured by Compose and local SQLite counts.
These startup values are read-only in the WebUI: update the environment variables
below and recreate the WebUI container to change them. Per-chat model controls live
under **Conversation settings** in the composer or conversation menu. System settings
also provides a guarded **Clear all** action; it requires typing `DELETE`, refuses to
run while a response is active, and removes conversations and their dependent data
in one database transaction.

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
| `AGENT_MAX_TURNS` | `200` | Emergency ceiling on model turns per task |
| `AGENT_INLINE_INPUT_BYTES` | `16384` | Largest user message kept inline; small context selections lower this to a 4 KiB floor |
| `AGENT_TOOL_FEEDBACK_BYTES` | `8192` | Maximum shell-output excerpt returned to later model turns |
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
tasks with optional shell use, new
conversations, context and temperature overrides before first load, streamed
responses with Stop, durable history and shell traces, title/message search, and
persisted image input for vision-capable models. Images may be pasted, dropped,
or selected; JPEG, PNG, and WebP are accepted with limits of four images, 10 MiB
each, and 20 MiB total per message. Completed responses include expandable token,
timing, and generation-rate details.
Conversation rename and deletion are available from each row in the history list.
Markdown rendering, data import/export and retention controls, and advanced generation
parameters remain planned.
