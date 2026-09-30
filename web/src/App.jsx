import { useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { contextUsage } from "./context-usage";
import { cancelRunningSubagents, reduceSubagentEvent } from "./agent-events";
import { formatDuration, formatRate, responseMetrics } from "./response-metrics";
import { scrollToEnd } from "./scroll-position";
import { selectElementText } from "./select-text";
import MarkdownContent from "./markdown-content";
import { thinkingModeForRequest, thinkingModeFromConversation } from "./thinking-mode";
import {
  clearConversations as clearAllConversations,
  changeConversationModel,
  createConversation,
  deleteConversation,
  deleteTurns,
  getConversation,
  getHealth,
  getSettings,
  listConversations,
  listModels,
  renameConversation,
  streamMessage,
} from "./api";
import {
  ArrowCounterClockwise,
  ArrowUp,
  Brain,
  CaretDown,
  ChatCircle,
  CheckCircle,
  CircleNotch,
  DotsThreeVertical,
  Gear,
  Info,
  MagnifyingGlass,
  NotePencil,
  Paperclip,
  SelectionAll,
  SidebarSimple,
  SlidersHorizontal,
  Stop,
  TerminalWindow,
  Trash,
  X,
} from "@phosphor-icons/react";

const contextWindows = [
  { value: "default", label: "Application default" },
  { value: "4096", label: "4,096 tokens" },
  { value: "8192", label: "8,192 tokens" },
  { value: "16384", label: "16,384 tokens" },
  { value: "32768", label: "32,768 tokens" },
  { value: "65536", label: "65,536 tokens" },
  { value: "131072", label: "131,072 tokens" },
];

const thinkingModes = [
  { value: "default", label: "Ollama default" },
  { value: "off", label: "Off" },
  { value: "on", label: "On — default effort" },
  { value: "low", label: "Low effort" },
  { value: "medium", label: "Medium effort" },
  { value: "high", label: "High effort" },
  { value: "max", label: "Maximum effort" },
];

const conversationModes = [
  { value: "agent", label: "Agent" },
  { value: "chat", label: "Chat" },
];

function ModelOptions({ models, model }) {
  return <>
    {model && !models.includes(model) && <option value={model} disabled>{model} — unavailable</option>}
    {!models.length && !model && <option value="">No local models found</option>}
    {models.map((item) => <option key={item} value={item}>{item}</option>)}
  </>;
}

function IconButton({ label, children, className = "", ...props }) {
  return (
    <button className={`icon-button ${className}`} aria-label={label} title={label} {...props}>
      {children}
    </button>
  );
}

function groupConversations(items) {
  const today = new Date();
  const yesterday = new Date(today);
  yesterday.setDate(today.getDate() - 1);
  const groups = new Map();
  for (const item of items) {
    const date = new Date(item.updated_at);
    const label = date.toDateString() === today.toDateString()
      ? "Today"
      : date.toDateString() === yesterday.toDateString() ? "Yesterday" : "Earlier";
    if (!groups.has(label)) groups.set(label, []);
    groups.get(label).push({
      ...item,
      time: label === "Today"
        ? date.toLocaleTimeString([], { hour: "numeric", minute: "2-digit" })
        : date.toLocaleDateString([], { month: "short", day: "numeric" }),
    });
  }
  return [...groups].map(([label, groupedItems]) => ({ label, items: groupedItems }));
}

function ConversationSidebar({ activeId, conversations, query, streaming, onQuery, onSelect, onNew, onRename, onDelete, onSettings, onAbout, onClose }) {
  const filteredGroups = useMemo(() => groupConversations(conversations), [conversations]);
  const [openActionsId, setOpenActionsId] = useState(null);

  return (
    <aside className="conversation-sidebar" aria-label="Conversations">
      <div className="sidebar-heading">
        <span className="wordmark">Ollama</span>
        {onClose && (
          <IconButton label="Close conversations" onClick={onClose}>
            <X size={22} />
          </IconButton>
        )}
      </div>

      <button className="new-chat-button" onClick={onNew}>
        <NotePencil size={23} weight="regular" />
        <span>New chat</span>
      </button>

      <label className="search-field">
        <MagnifyingGlass size={21} />
        <span className="sr-only">Search conversations</span>
        <input
          value={query}
          onChange={(event) => onQuery(event.target.value)}
          placeholder="Search conversations"
        />
      </label>

      <nav className="conversation-list" aria-label="Conversation history">
        {filteredGroups.length ? (
          filteredGroups.map((group) => (
            <section className="conversation-group" key={group.label}>
              <h2>{group.label}</h2>
              {group.items.map((item) => (
                <div
                  key={item.id}
                  className={`conversation-entry ${activeId === item.id ? "active" : ""}`}
                  onBlur={(event) => {
                    if (!event.currentTarget.contains(event.relatedTarget)) setOpenActionsId(null);
                  }}
                  onKeyDown={(event) => {
                    if (event.key === "Escape" && openActionsId === item.id) {
                      setOpenActionsId(null);
                      event.currentTarget.querySelector(".conversation-actions-trigger")?.focus();
                    }
                  }}
                >
                  <button
                    className="conversation-row"
                    aria-current={activeId === item.id ? "page" : undefined}
                    onClick={() => onSelect(item.id)}
                  >
                    <ChatCircle size={18} />
                    <span className="conversation-title">{item.title}</span>
                    <time>{item.time}</time>
                  </button>
                  <div className="conversation-actions-wrap">
                    <IconButton
                      className="conversation-actions-trigger"
                      label={`Actions for ${item.title}`}
                      aria-haspopup="menu"
                      aria-expanded={openActionsId === item.id}
                      onClick={() => setOpenActionsId((current) => current === item.id ? null : item.id)}
                    >
                      <DotsThreeVertical size={18} weight="bold" />
                    </IconButton>
                    {openActionsId === item.id && (
                      <div className="conversation-actions-menu" role="menu">
                        <button
                          role="menuitem"
                          onClick={(event) => {
                            const trigger = event.currentTarget.closest(".conversation-entry")?.querySelector(".conversation-actions-trigger");
                            setOpenActionsId(null);
                            onRename(item, trigger);
                          }}
                        ><NotePencil size={17} />Rename</button>
                        <button
                          className="danger-menu-item"
                          role="menuitem"
                          disabled={streaming && activeId === item.id}
                          onClick={(event) => {
                            const trigger = event.currentTarget.closest(".conversation-entry")?.querySelector(".conversation-actions-trigger");
                            setOpenActionsId(null);
                            onDelete(item, trigger);
                          }}
                        ><Trash size={17} />Delete</button>
                      </div>
                    )}
                  </div>
                </div>
              ))}
            </section>
          ))
        ) : (
          <div className="search-empty">
            <MagnifyingGlass size={24} />
            <p>No matching conversations</p>
          </div>
        )}
      </nav>

      <div className="sidebar-footer">
        <button onClick={(event) => onSettings(event.currentTarget)}><Gear size={23} />Settings</button>
        <button onClick={(event) => onAbout(event.currentTarget)}><Info size={23} />About</button>
      </div>
    </aside>
  );
}

function ConversationSettings({
  models,
  model,
  setModel,
  conversationMode,
  setConversationMode,
  supportsTools,
  onClose,
  systemPrompt,
  setSystemPrompt,
  contextWindow,
  setContextWindow,
  thinkingMode,
  setThinkingMode,
  supportsThinking,
  modelLoaded,
  modelDisabled,
  modelFeedback,
  modelError,
  onRefreshModels,
  temperatureOverride,
  setTemperatureOverride,
  temperature,
  setTemperature,
}) {
  function resetDefaults() {
    setSystemPrompt("");
    if (!modelLoaded) setConversationMode(supportsTools ? "agent" : "chat");
    if (!modelLoaded) setContextWindow("default");
    if (!modelLoaded) setThinkingMode("default");
    setTemperatureOverride(false);
    setTemperature(0.7);
  }

  return (
    <aside className="settings-panel" aria-label="Conversation settings">
      <div className="settings-heading">
        <h2>Conversation settings</h2>
        <IconButton label="Close conversation settings" onClick={onClose}>
          <X size={24} />
        </IconButton>
      </div>

      <div className="settings-scroll">
        <section className="settings-section model-section">
          <label htmlFor="settings-model">Model</label>
          <div className="select-wrap wide">
            <select id="settings-model" value={model} disabled={modelDisabled} onFocus={onRefreshModels} aria-describedby="settings-model-help" onChange={(event) => setModel(event.target.value)}>
              <ModelOptions models={models} model={model} />
            </select>
            <CaretDown size={18} aria-hidden="true" />
          </div>
          <p id="settings-model-help">{modelLoaded ? "Switch models between responses. History and mode stay the same; thinking resets to the new model’s default." : "Select the model to use for this conversation."}</p>
          {modelFeedback && <p className={modelError ? "model-feedback-error" : ""} role={modelError ? "alert" : "status"}>{modelFeedback}</p>}
        </section>

        <section className="settings-section mode-section">
          <label htmlFor="conversation-mode">Mode</label>
          <div className="select-wrap wide">
            <select
              id="conversation-mode"
              value={conversationMode}
              disabled={modelLoaded}
              onChange={(event) => setConversationMode(event.target.value)}
            >
              {conversationModes.map((item) => (
                <option key={item.value} value={item.value} disabled={item.value === "agent" && !supportsTools}>{item.label}</option>
              ))}
            </select>
            <CaretDown size={18} aria-hidden="true" />
          </div>
          {modelLoaded ? (
            <p className="locked-setting"><span className="lock-dot" />Mode is fixed for this conversation.</p>
          ) : supportsTools ? (
            <p>Agent can use the shell; Chat talks to the model without tools.</p>
          ) : (
            <p>This model uses Chat because it does not advertise tool support.</p>
          )}
        </section>

        <section className="settings-section context-section">
          <label htmlFor="context-window">Context window <Info size={17} aria-label="Context window help" /></label>
          <div className="select-wrap wide">
            <select
              id="context-window"
              value={contextWindow}
              disabled={modelLoaded}
              onChange={(event) => setContextWindow(event.target.value)}
            >
              {contextWindows.map((item) => (
                <option key={item.value} value={item.value}>{item.label}</option>
              ))}
            </select>
            <CaretDown size={18} aria-hidden="true" />
          </div>
          {modelLoaded ? (
            <p className="locked-setting"><span className="lock-dot" />Model loaded. Start a new chat to choose a different context window.</p>
          ) : (
            <p>Agent uses the managed server budget; Chat leaves the limit to Ollama.</p>
          )}
        </section>

        <section className="settings-section thinking-section">
          <label htmlFor="thinking-mode">Thinking</label>
          <div className="select-wrap wide">
            <select
              id="thinking-mode"
              value={thinkingMode}
              disabled={modelLoaded || !supportsThinking}
              onChange={(event) => setThinkingMode(event.target.value)}
            >
              {supportsThinking
                ? thinkingModes.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)
                : <option value="default">Not supported</option>}
            </select>
            <CaretDown size={18} aria-hidden="true" />
          </div>
          {!supportsThinking ? (
            <p>This model does not advertise thinking support.</p>
          ) : modelLoaded ? (
            <p className="locked-setting"><span className="lock-dot" />Model loaded. Start a new chat to choose a different thinking mode.</p>
          ) : (
            <p>Default sends no override. Effort levels are passed through exactly and remain model-dependent.</p>
          )}
        </section>

        <section className="settings-section">
          <label htmlFor="system-prompt">System prompt <Info size={17} aria-label="System prompt help" /></label>
          <textarea
            id="system-prompt"
            value={systemPrompt}
            onChange={(event) => setSystemPrompt(event.target.value)}
            placeholder="Model default"
            disabled={modelLoaded}
          />
          <p>{modelLoaded ? "Start a new chat to use a different system prompt." : "Saved with this conversation when its first message is sent."}</p>
        </section>

        <section className="settings-section temperature-section">
          <div className="setting-label-row">
            <label htmlFor="temperature">Temperature <Info size={17} aria-label="Temperature help" /></label>
            <label className="override-toggle">
              <input
                type="checkbox"
                checked={temperatureOverride}
                disabled={modelLoaded}
                onChange={(event) => setTemperatureOverride(event.target.checked)}
              />
              <span>{temperatureOverride ? "Override on" : "Model default"}</span>
            </label>
          </div>
          <div className={`default-value ${temperatureOverride ? "is-override" : ""}`}>
            {temperatureOverride ? temperature.toFixed(1) : "Model default"}
          </div>
          <input
            id="temperature"
            type="range"
            min="0"
            max="2"
            step="0.1"
            value={temperature}
            disabled={modelLoaded || !temperatureOverride}
            onChange={(event) => setTemperature(Number(event.target.value))}
          />
          <div className="range-labels"><span>0</span><span>{temperature.toFixed(1)}</span><span>2</span></div>
          <p>{modelLoaded ? "Start a new chat to use a different temperature." : "Controls randomness in model responses."}</p>
        </section>
      </div>

      <button className="reset-button" onClick={resetDefaults} disabled={modelLoaded}>
        <ArrowCounterClockwise size={21} />
        Reset to model defaults
      </button>
    </aside>
  );
}

function formatBytes(value) {
  if (!Number.isFinite(value) || value <= 0) return "0 B";
  const units = ["B", "KB", "MB", "GB"];
  const power = Math.min(Math.floor(Math.log(value) / Math.log(1024)), units.length - 1);
  const amount = value / 1024 ** power;
  return `${amount >= 10 || power === 0 ? amount.toFixed(0) : amount.toFixed(1)} ${units[power]}`;
}

function SystemSettings({ connected, modelCount, runtime, loading, error, streaming, onRefresh, onClose, onRequestClear, closeRef }) {
  const storage = runtime?.storage;
  const agent = runtime?.agent;
  return (
    <div className="system-settings-dialog-content">
      <div className="settings-heading">
        <div><span className="settings-eyebrow">Application</span><h2 id="system-settings-title">System settings</h2></div>
        <IconButton ref={closeRef} label="Close system settings" onClick={onClose}><X size={24} /></IconButton>
      </div>

      <div className="settings-scroll">
        <section className="settings-section system-status-section">
          <div className="system-section-heading"><div><h3>Runtime</h3><p>Current service and Agent boundaries.</p></div><button className="text-action" type="button" onClick={onRefresh} disabled={loading}>{loading ? "Refreshing…" : "Refresh"}</button></div>
          {error && <p className="settings-error" role="alert">{error}</p>}
          <div className="runtime-cards">
            <article>
              <div className="runtime-card-title"><span className={`status-dot ${connected ? "connected" : ""}`} /><strong>Ollama</strong></div>
              <span>{connected ? "Connected" : "Unavailable"}</span>
              <small>{modelCount} installed {modelCount === 1 ? "model" : "models"}</small>
            </article>
            <article>
              <div className="runtime-card-title"><TerminalWindow size={18} /><strong>Agent shell</strong></div>
              <span>{agent ? `${agent.max_turns} turn limit` : "Loading…"}</span>
              <small>{agent ? `${agent.shell_timeout_seconds}s command timeout` : ""}</small>
            </article>
          </div>
          <dl className="runtime-details">
            <div><dt>Filesystem policy</dt><dd>System read · workspace write</dd></div>
            <div><dt>Writable workspace</dt><dd title={agent?.workspace}>{agent?.workspace || "—"}</dd></div>
            <div><dt>Managed context</dt><dd title={agent?.context_path}>{agent?.context_path || "—"}</dd></div>
            <div><dt>Context budget</dt><dd>{agent ? `${agent.context_budget_tokens?.toLocaleString() || "—"} tokens` : "—"}</dd></div>
            <div><dt>Child agents</dt><dd>{agent ? agent.subagents_enabled ? `Enabled · ${agent.subagent_concurrency} concurrent` : "Disabled" : "—"}</dd></div>
            <div><dt>Child context</dt><dd>{agent?.subagents_enabled ? `${agent.subagent_context_tokens?.toLocaleString()} tokens · ${agent.subagent_max_turns} turns` : "—"}</dd></div>
            <div><dt>Shell output limit</dt><dd>{agent ? formatBytes(agent.shell_max_output_bytes) : "—"}</dd></div>
            <div><dt>Inline input limit</dt><dd>{agent ? formatBytes(agent.inline_input_bytes) : "—"}</dd></div>
            <div><dt>Tool feedback limit</dt><dd>{agent ? formatBytes(agent.tool_feedback_bytes) : "—"}</dd></div>
            <div><dt>Child result limit</dt><dd>{agent ? formatBytes(agent.subagent_result_bytes) : "—"}</dd></div>
          </dl>
          <p className="managed-note"><Info size={17} />These values come from Docker Compose environment settings. Change them there, then recreate the WebUI container.</p>
        </section>

        <section className="settings-section data-settings-section">
          <div className="system-section-heading"><div><h3>Local data</h3><p>Conversations, images, and managed context stored locally.</p></div><span className="storage-size">{storage ? formatBytes(storage.database_bytes) : "—"}</span></div>
          <dl className="data-counts">
            <div><dt>Conversations</dt><dd>{storage?.conversations ?? "—"}</dd></div>
            <div><dt>Messages</dt><dd>{storage?.messages ?? "—"}</dd></div>
            <div><dt>Images</dt><dd>{storage?.attachments ?? "—"}</dd></div>
            <div><dt>Agent actions</dt><dd>{storage?.agent_steps ?? "—"}</dd></div>
            <div><dt>Child jobs</dt><dd>{storage?.agent_jobs ?? "—"}</dd></div>
            <div><dt>Child runs</dt><dd>{storage?.agent_runs ?? "—"}</dd></div>
            <div><dt>Context checkpoints</dt><dd>{storage?.context_checkpoints ?? "—"}</dd></div>
            <div><dt>Context artifacts</dt><dd>{storage?.context_artifacts ?? "—"}</dd></div>
            <div><dt>Context storage</dt><dd>{storage ? formatBytes(storage.context_artifact_bytes) : "—"}</dd></div>
            <div><dt>Image storage</dt><dd>{storage ? formatBytes(storage.attachment_bytes) : "—"}</dd></div>
          </dl>
          <div className="danger-zone">
            <div><strong>Clear conversation history</strong><p>Deletes every conversation, message, image, Agent action, checkpoint, and context artifact.</p></div>
            <button type="button" onClick={onRequestClear} disabled={streaming || !storage?.conversations}><Trash size={18} />Clear all</button>
          </div>
          {streaming && <p className="locked-setting"><span className="lock-dot" />Stop the active response before clearing data.</p>}
        </section>

      </div>
    </div>
  );
}

function AgentActivity({ message, mode }) {
  const thinkingRef = useRef(null);
  const steps = message.agent_steps || [];
  const subagentRuns = message.subagent_runs || [];
  const hasActivity = message.status === "streaming" || Boolean(message.thinking) || steps.length > 0 || subagentRuns.length > 0;

  const phaseLabels = {
    starting: "Starting",
    thinking: "Thinking",
    shell: "Running shell",
    reviewing: "Reviewing result",
    responding: "Writing answer",
    finished: "Finished",
    stopped: "Stopped",
    error: "Failed",
  };
  const phase = message.agent_phase || (message.status === "complete" ? "finished" : message.status === "cancelled" ? "stopped" : message.status === "error" ? "error" : "thinking");
  const turn = mode === "agent" ? message.agent_turn || message.metadata?.agent_turns : null;
  const active = message.status === "streaming";

  useEffect(() => {
    if (active && phase === "thinking" && thinkingRef.current) {
      thinkingRef.current.scrollTop = thinkingRef.current.scrollHeight;
    }
  }, [active, phase, message.thinking]);

  if (!hasActivity) return null;

  return (
    <section className={`agent-activity ${phase}`} aria-label={mode === "agent" ? "Agent activity" : "Response activity"}>
      <div className="agent-progress" role="status" aria-live="polite">
        <span className="agent-progress-icon" aria-hidden="true">
          {active ? <CircleNotch size={17} /> : phase === "finished" ? <CheckCircle size={17} /> : <Stop size={15} weight="fill" />}
        </span>
        <strong>{phaseLabels[phase] || "Working"}</strong>
        {turn && <span>Turn {turn}</span>}
        {steps.length > 0 && <span>{steps.length} {steps.length === 1 ? "action" : "actions"}</span>}
      </div>

      {message.thinking && (
        <details className="thinking-step" open={active && phase === "thinking"}>
          <summary>
            <span className="agent-step-icon"><Brain size={17} /></span>
            <span>Model thinking</span>
            <span className="agent-step-status">{active && phase === "thinking" ? "Live" : "View"}</span>
          </summary>
          <div className="thinking-content" ref={thinkingRef}>{message.thinking}{active && phase === "thinking" && <span className="stream-cursor" aria-hidden="true" />}</div>
        </details>
      )}

      {steps.length > 0 && (
        <div className="agent-steps" aria-label="Tool activity">
          {steps.map((step) => {
            let parsedInput = {};
            try { parsedInput = JSON.parse(step.input); } catch { /* Show the recorded input. */ }
            const shellStep = step.tool_name === "shell" || !step.tool_name;
            const label = shellStep
              ? parsedInput.command || step.input
              : step.tool_name === "delegate_task"
                ? parsedInput.task || "Delegated task"
                : step.tool_name === "label_image_directory"
                  ? `Label images in ${parsedInput.directory || "directory"}`
                  : step.tool_name?.replaceAll("_", " ") || "Tool action";
            const statusLabel = step.status === "running" ? "Running" : step.status === "complete" ? shellStep ? `Exited ${step.exit_code ?? 0}` : "Complete" : step.status === "cancelled" ? "Stopped" : "Failed";
            const StepIcon = step.tool_name === "delegate_task" ? Brain : step.tool_name === "label_image_directory" ? SelectionAll : shellStep ? TerminalWindow : Gear;
            return (
              <details className={`agent-step ${step.status}`} key={step.id} open={step.status === "running"}>
                <summary>
                  <span className="agent-step-icon"><StepIcon size={17} /></span>
                  <span className={shellStep ? "agent-step-command" : "agent-step-label"}>{label}</span>
                  <span className="agent-step-status">{statusLabel}</span>
                </summary>
                {step.output && <pre>{step.output}</pre>}
              </details>
            );
          })}
        </div>
      )}

      {subagentRuns.length > 0 && (
        <div className="subagent-runs" aria-label="Child tasks" aria-live="polite">
          {subagentRuns.map((run) => {
            const statusLabel = run.status === "running" ? "Running" : run.status === "complete" ? "Complete" : run.status === "cancelled" ? "Stopped" : run.status === "failed" ? "Failed" : "Queued";
            const RunIcon = run.status === "running" || run.status === "queued" ? CircleNotch : run.status === "complete" ? CheckCircle : Stop;
            return (
              <details className={`subagent-run ${run.status}`} key={run.id} open={run.status === "running"}>
                <summary>
                  <span className="subagent-run-icon" aria-hidden="true"><RunIcon size={17} /></span>
                  <span className="subagent-run-task">{run.task || "Child task"}</span>
                  <span className="subagent-run-status">{statusLabel}</span>
                </summary>
                <div className="subagent-run-result">
                  {run.result_summary && <p>{run.result_summary}</p>}
                  {run.error && <p className="subagent-run-error">{run.error}</p>}
                  {run.progress?.total > 0 && <p>{run.progress.processed} of {run.progress.total} processed · {run.progress.failed || 0} failed · {run.progress.needs_review || 0} need review</p>}
                  {run.output_refs?.length > 0 && (
                    <div className="subagent-output-refs">
                      <strong>Outputs</strong>
                      <ul>{run.output_refs.map((path) => <li key={path}><code>{path}</code></li>)}</ul>
                    </div>
                  )}
                  {!run.result_summary && !run.error && !run.output_refs?.length && <p>{statusLabel}.</p>}
                </div>
              </details>
            );
          })}
        </div>
      )}
    </section>
  );
}

function ResponseMetrics({ message }) {
  if (message.status !== "complete") return null;
  const metrics = responseMetrics(message.metadata);
  if (!metrics) return null;
  const generationRate = formatRate(metrics.generationRate);
  const modelTime = formatDuration(metrics.totalSeconds);
  const summary = [
    generationRate,
    metrics.outputTokens != null ? `${metrics.outputTokens.toLocaleString()} output tokens` : null,
    modelTime ? `${modelTime} model time` : null,
  ].filter(Boolean);
  const details = [
    ["Generation", generationRate],
    ["Output", metrics.outputTokens != null ? `${metrics.outputTokens.toLocaleString()} tokens` : null],
    ["Processed input", metrics.inputTokens != null ? `${metrics.inputTokens.toLocaleString()} tokens` : null],
    ["Prompt processing", formatRate(metrics.promptRate)],
    ["Model time", modelTime],
    ["Model load", formatDuration(metrics.loadSeconds)],
    ["Model turns", metrics.turns ? metrics.turns.toLocaleString() : null],
  ].filter(([, value]) => value);
  if (summary.length === 0) return null;

  return (
    <details className="response-metrics">
      <summary aria-label={`Response performance: ${summary.join(", ")}`}>
        <Info size={15} aria-hidden="true" />
        {summary.map((item, index) => <span className={index === 0 ? "primary" : ""} key={item}>{item}</span>)}
      </summary>
      <dl>
        {details.map(([label, value]) => (
          <div key={label}><dt>{label}</dt><dd>{value}</dd></div>
        ))}
      </dl>
      <p>{metrics.mode === "chat" ? "Single Chat model call." : metrics.aggregate ? "Across the complete agent run." : "Final model call; older conversation metadata."}</p>
    </details>
  );
}

function AssistantResponse({ message }) {
  const contentRef = useRef(null);
  const markdownRef = useRef(null);
  const markdownButtonRef = useRef(null);
  const [showMarkdown, setShowMarkdown] = useState(false);
  const [selected, setSelected] = useState(false);
  const selectedTimerRef = useRef(null);

  useEffect(() => () => window.clearTimeout(selectedTimerRef.current), []);

  function selectMarkdown() {
    markdownRef.current?.focus();
    markdownRef.current?.select();
  }

  useLayoutEffect(() => {
    if (showMarkdown) selectMarkdown();
  }, [showMarkdown]);

  function selectResponse() {
    if (!selectElementText(contentRef.current)) return;
    setSelected(true);
    window.clearTimeout(selectedTimerRef.current);
    selectedTimerRef.current = window.setTimeout(() => setSelected(false), 1600);
  }

  return (
    <>
      <div className="message-content markdown-content" ref={contentRef}>
        <MarkdownContent>{message.content || (message.status === "streaming" ? "" : "No response was generated.")}</MarkdownContent>
        {message.status === "streaming" && <span className="stream-cursor" aria-hidden="true" />}
      </div>
      {message.content && message.status !== "streaming" && (
        <div className="response-actions">
          <button className="select-response-button" type="button" onClick={selectResponse}>
            <SelectionAll size={16} aria-hidden="true" />
            <span aria-live="polite">{selected ? "Selected" : "Select response"}</span>
          </button>
          <button className="select-response-button" type="button" ref={markdownButtonRef} onClick={() => {
            setShowMarkdown(true);
            selectMarkdown();
          }}>
            <SelectionAll size={16} aria-hidden="true" />
            Select Markdown
          </button>
        </div>
      )}
      {showMarkdown && (
        <div className="markdown-source">
          <div className="markdown-source-toolbar">
            <span>Copy original Markdown with Ctrl+C or ⌘C.</span>
            <button type="button" onClick={() => {
              setShowMarkdown(false);
              markdownButtonRef.current?.focus();
            }}>Hide source</button>
          </div>
          <textarea ref={markdownRef} aria-label="Original Markdown" readOnly value={message.content} spellCheck={false} />
        </div>
      )}
    </>
  );
}

function ChatTranscript({
  streaming,
  onDeleteTurn,
  empty,
  messages,
  conversationMode,
  error,
}) {
  if (empty) {
    return (
      <div className="empty-chat">
        <div className="empty-icon"><ChatCircle size={38} /></div>
        <h1>New conversation</h1>
        <p>Chat privately with your local models.<br />Your data stays on this machine.</p>
        {error && <div className="chat-error" role="alert">{error}</div>}
        </div>
    );
  }

  return (
    <div className="transcript-inner">
      {messages.map((message, index) => (
        <article className={`message ${message.role === "user" ? "user-message" : "assistant-message"}`} key={message.id || `${message.role}-${index}`}>
          <header>
            <strong>{message.role === "user" ? "You" : "Ollama"}</strong>
            <time>{message.created_at ? new Date(message.created_at).toLocaleTimeString([], { hour: "numeric", minute: "2-digit" }) : "Now"}</time>
          </header>
          {message.attachments?.length > 0 && (
            <div className="message-images" aria-label="Attached images">
              {message.attachments.map((attachment) => (
                <a href={attachment.url} target="_blank" rel="noreferrer" key={attachment.id || attachment.url}>
                  <img src={attachment.url} alt={`Attached image: ${attachment.file_name}`} loading="lazy" />
                  <span>{attachment.file_name}</span>
                </a>
              ))}
            </div>
          )}
          {message.role === "assistant" && <AgentActivity message={message} mode={conversationMode} />}
          {message.role === "assistant"
            ? (message.content || message.status !== "streaming") && <AssistantResponse message={message} />
            : message.content && <p className="message-content">{message.content}</p>}
          {message.status === "cancelled" && <p className="message-status">Generation stopped</p>}
          {message.status === "error" && <p className="message-status error">Generation failed</p>}
          {message.role === "assistant" && <ResponseMetrics message={message} />}
          {message.role === "user" && (
            <button className="select-response-button delete-turn-button" type="button"
              disabled={streaming || message.id === "pending-user"}
              title={streaming ? "Stop generation before deleting this turn" : "Delete this turn and all later turns"}
              onClick={(event) => onDeleteTurn(message, event.currentTarget)}>
              <Trash size={16} aria-hidden="true" />Delete from here
            </button>
          )}
        </article>
      ))}
      {error && <div className="chat-error" role="alert">{error}</div>}
    </div>
  );
}

const acceptedImageTypes = new Set(["image/jpeg", "image/png", "image/webp"]);

function readImage(file) {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onerror = () => reject(new Error(`Could not read ${file.name}`));
    reader.onload = () => {
      const dataURL = String(reader.result);
      resolve({
        id: `${file.name}-${file.lastModified}-${file.size}`,
        name: file.name,
        media_type: file.type,
        size: file.size,
        data: dataURL.slice(dataURL.indexOf(",") + 1),
        url: dataURL,
        file_name: file.name,
      });
    };
    reader.readAsDataURL(file);
  });
}

function Composer({ streaming, onStop, onSend, empty, model, models, setModel, modelDisabled, onRefreshModels, conversationMode, setConversationMode, contextWindow, setContextWindow, thinkingMode, setThinkingMode, contextUsedTokens, disabled, supportsImages, supportsTools, supportsThinking, onOpenSettings }) {
  const [message, setMessage] = useState("");
  const [attachments, setAttachments] = useState([]);
  const [attachmentError, setAttachmentError] = useState("");
  const [draggingImages, setDraggingImages] = useState(false);
  const inputRef = useRef(null);
  const fileInputRef = useRef(null);

  useEffect(() => {
    if (empty) inputRef.current?.focus();
  }, [empty]);

  useEffect(() => {
    if (!supportsImages && attachments.length > 0) {
      setAttachmentError("Choose a vision-capable model before sending these images.");
    } else if (supportsImages) {
      setAttachmentError("");
    }
  }, [supportsImages, attachments.length]);

  function submit() {
    const value = message.trim();
    if (disabled || (!value && attachments.length === 0) || !supportsImages && attachments.length > 0) return;
    onSend(value, attachments);
    setMessage("");
    setAttachments([]);
    setAttachmentError("");
  }

  async function addImages(files) {
    setAttachmentError("");
    if (!supportsImages) {
      setAttachmentError("Choose a vision-capable model to attach images.");
      return;
    }
    const candidates = [...files];
    if (attachments.length + candidates.length > 4) {
      setAttachmentError("Attach at most 4 images per message.");
      return;
    }
    if (candidates.some((file) => !acceptedImageTypes.has(file.type))) {
      setAttachmentError("Images must be JPEG, PNG, or WebP files.");
      return;
    }
    if (candidates.some((file) => file.size === 0 || file.size > 10 * 1024 * 1024)) {
      setAttachmentError("Each image must be between 1 byte and 10 MiB.");
      return;
    }
    const total = attachments.reduce((sum, item) => sum + item.size, 0)
      + candidates.reduce((sum, file) => sum + file.size, 0);
    if (total > 20 * 1024 * 1024) {
      setAttachmentError("Images may total at most 20 MiB per message.");
      return;
    }
    try {
      const prepared = await Promise.all(candidates.map(readImage));
      setAttachments((current) => [...current, ...prepared]);
    } catch (error) {
      setAttachmentError(error.message);
    }
  }

  function onKeyDown(event) {
    if (event.key === "Enter" && !event.shiftKey) {
      event.preventDefault();
      submit();
    }
  }

  const contextLimit = contextWindow === "default" ? null : Number(contextWindow);
  const usedPercent = contextLimit ? Math.min(100, contextUsedTokens / contextLimit * 100) : null;
  const displayPercent = usedPercent == null ? null : usedPercent > 0 && usedPercent < 1 ? "<1" : Math.round(usedPercent).toString();
  const contextLabel = contextLimit
    ? `${contextUsedTokens.toLocaleString()} / ${contextLimit.toLocaleString()} tokens (${displayPercent}%)`
    : contextUsedTokens > 0
      ? `${contextUsedTokens.toLocaleString()} tokens · ${conversationMode === "agent" ? "managed limit" : "Ollama limit"}`
      : `${conversationMode === "agent" ? "Managed" : "Ollama"} default · usage available after response`;

  const messageField = (
    <textarea
      ref={inputRef}
      value={message}
      rows={empty ? "6" : "1"}
      onChange={(event) => setMessage(event.target.value)}
      onPaste={(event) => {
        const images = [...event.clipboardData.files].filter((file) => file.type.startsWith("image/"));
        if (images.length) {
          event.preventDefault();
          addImages(images);
        }
      }}
      onKeyDown={onKeyDown}
      placeholder={empty ? "Ask or assign a task…" : model ? `Message ${model}` : "Choose a model"}
      aria-label="Message"
    />
  );

  const attachButton = (
    <IconButton
      label={supportsImages ? "Attach images" : "Image input requires a vision model"}
      className="attach-button"
      aria-disabled={!supportsImages}
      onClick={() => supportsImages ? fileInputRef.current?.click() : setAttachmentError("Choose a vision-capable model to attach images.")}
    >
      <Paperclip size={22} />
    </IconButton>
  );

  const sendButton = streaming ? (
    <button className="stop-button" aria-label="Stop generation" onClick={onStop}>
      <Stop size={16} weight="fill" /><span>Stop</span>
    </button>
  ) : (
    <button className="send-button" aria-label="Send message" onClick={submit} disabled={(!message.trim() && attachments.length === 0) || disabled || (!supportsImages && attachments.length > 0)}>
      <ArrowUp size={19} weight="bold" /><span>Send</span>
    </button>
  );

  return (
    <div className="composer-wrap">
      <div
        className={`composer focus-within ${draggingImages ? "is-dragging" : ""}`}
        onDragEnter={(event) => { event.preventDefault(); setDraggingImages(true); }}
        onDragOver={(event) => event.preventDefault()}
        onDragLeave={(event) => {
          if (!event.currentTarget.contains(event.relatedTarget)) setDraggingImages(false);
        }}
        onDrop={(event) => {
          event.preventDefault();
          setDraggingImages(false);
          addImages([...event.dataTransfer.files].filter((file) => file.type.startsWith("image/")));
        }}
      >
        {empty && (
          <div className="launch-dock-header">
            <div className="select-wrap launch-dock-model">
              <select aria-label="Model" value={model} disabled={modelDisabled} onFocus={onRefreshModels} onChange={(event) => setModel(event.target.value)}>
                <ModelOptions models={models} model={model} />
              </select>
              <CaretDown size={17} aria-hidden="true" />
            </div>
            <div className="launch-dock-modes" role="radiogroup" aria-label="Conversation mode">
              <label className={conversationMode === "chat" ? "selected" : ""}>
                <input type="radio" name="conversation-mode" value="chat" checked={conversationMode === "chat"} onChange={() => setConversationMode("chat")} />
                <ChatCircle size={20} /><span>Chat</span>
              </label>
              <label className={`${conversationMode === "agent" ? "selected" : ""} ${!supportsTools ? "disabled" : ""}`}>
                <input type="radio" name="conversation-mode" value="agent" checked={conversationMode === "agent"} disabled={!supportsTools} onChange={() => setConversationMode("agent")} />
                <TerminalWindow size={20} /><span>Agent</span>
              </label>
            </div>
            <div className="launch-capabilities" aria-label="Selected model capabilities">
              {supportsTools && <span>Tools</span>}
              {supportsImages && <span>Vision</span>}
              {supportsThinking && <span>Thinking</span>}
            </div>
            <IconButton label="Open model settings" className="launch-settings-button" onClick={onOpenSettings}>
              <SlidersHorizontal size={21} />
            </IconButton>
          </div>
        )}
        {conversationMode === "chat" && model && !empty && (
          <div className="mode-notice" role="status">
            Chat mode · {supportsTools ? "tools are off for this conversation" : "this model does not support tools"}
          </div>
        )}
        {attachments.length > 0 && (
          <div className="attachment-previews" aria-label="Images ready to send">
            {attachments.map((attachment) => (
              <div className="attachment-preview" key={attachment.id}>
                <img src={attachment.url} alt={`Preview of ${attachment.name}`} />
                <button
                  type="button"
                  aria-label={`Remove ${attachment.name}`}
                  onClick={() => setAttachments((current) => current.filter((item) => item.id !== attachment.id))}
                ><X size={16} /></button>
                <span>{attachment.name}</span>
              </div>
            ))}
          </div>
        )}
        {attachmentError && <div className="attachment-error" role="alert">{attachmentError}</div>}
        <input
          ref={fileInputRef}
          className="sr-only"
          type="file"
          accept="image/jpeg,image/png,image/webp"
          multiple
          aria-hidden="true"
          tabIndex="-1"
          onChange={(event) => {
            addImages(event.target.files);
            event.target.value = "";
          }}
        />
        {empty ? (
          <>
            <div className="launch-prompt-row">{messageField}</div>
            <div className="launch-dock-footer">
              {attachButton}
              <label className="launch-runtime-control">
                <span>Context</span>
                <select aria-label="Context window" value={contextWindow} onChange={(event) => setContextWindow(event.target.value)}>
                  {contextWindows.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}
                </select>
                <CaretDown size={16} aria-hidden="true" />
              </label>
              <label className="launch-runtime-control">
                <span>Thinking</span>
                <select aria-label="Thinking" value={thinkingMode} disabled={!supportsThinking} onChange={(event) => setThinkingMode(event.target.value)}>
                  {supportsThinking
                    ? thinkingModes.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)
                    : <option value="default">Not supported</option>}
                </select>
                <CaretDown size={16} aria-hidden="true" />
              </label>
              <div className="launch-context-summary">
                <span>{contextLimit ? contextLabel : "Limit inherited"}</span>
                <div className={`context-track ${usedPercent == null ? "unknown-limit" : ""}`} aria-hidden="true">
                  {usedPercent != null && <span style={{ width: `${usedPercent}%` }} />}
                </div>
              </div>
              {sendButton}
            </div>
          </>
        ) : (
          <>
            <div className="composer-row">{attachButton}{messageField}{sendButton}</div>
            <div className="context-row">
              <span>Context&nbsp;&nbsp;{contextLabel}</span>
              <div
                className={`context-track ${usedPercent == null ? "unknown-limit" : ""}`}
                aria-label={contextLimit ? `${contextUsedTokens} of ${contextLimit} context tokens used` : `${contextUsedTokens} context tokens used; limit inherited from Ollama`}
                role={contextLimit ? "progressbar" : undefined}
                aria-valuenow={usedPercent ?? undefined}
                aria-valuemin={contextLimit ? "0" : undefined}
                aria-valuemax={contextLimit ? "100" : undefined}
              >
                {usedPercent != null && <span style={{ width: `${usedPercent}%` }} />}
              </div>
            </div>
          </>
        )}
      </div>
    </div>
  );
}

export function App() {
  const [activeId, setActiveId] = useState(null);
  const [activeTitle, setActiveTitle] = useState("");
  const [model, setModel] = useState("");
  const [changingModel, setChangingModel] = useState(false);
  const [modelError, setModelError] = useState("");
  const [modelNotice, setModelNotice] = useState("");
  const [modelsLoaded, setModelsLoaded] = useState(false);
  const [loadingConversation, setLoadingConversation] = useState(false);
  const conversationViewRef = useRef(0);
  const modelChangeRef = useRef(null);
  const modelListRequestRef = useRef(0);
  const [conversationMode, setConversationMode] = useState("agent");
  const [models, setModels] = useState([]);
  const [visionModels, setVisionModels] = useState(() => new Set());
  const [toolModels, setToolModels] = useState(() => new Set());
  const [thinkingModels, setThinkingModels] = useState(() => new Set());
  const [conversationItems, setConversationItems] = useState([]);
  const [query, setQuery] = useState("");
  const [messages, setMessages] = useState([]);
  const [scrollRequest, setScrollRequest] = useState(0);
  const [connected, setConnected] = useState(false);
  const [chatError, setChatError] = useState("");
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [systemSettingsOpen, setSystemSettingsOpen] = useState(false);
  const [runtimeSettings, setRuntimeSettings] = useState(null);
  const [runtimeSettingsLoading, setRuntimeSettingsLoading] = useState(false);
  const [runtimeSettingsError, setRuntimeSettingsError] = useState("");
  const [sidebarOpen, setSidebarOpen] = useState(false);
  const [menuOpen, setMenuOpen] = useState(false);
  const [renameTarget, setRenameTarget] = useState(null);
  const [renameTitle, setRenameTitle] = useState("");
  const [renameError, setRenameError] = useState("");
  const [renaming, setRenaming] = useState(false);
  const [turnDeleteTarget, setTurnDeleteTarget] = useState(null);
  const [turnDeleteError, setTurnDeleteError] = useState("");
  const [deletingTurns, setDeletingTurns] = useState(false);
  const turnDeleteDialogRef = useRef(null);
  const turnDeleteCancelRef = useRef(null);
  const turnDeleteReturnRef = useRef(null);
  const [deleteTarget, setDeleteTarget] = useState(null);
  const [deleteError, setDeleteError] = useState("");
  const [deleting, setDeleting] = useState(false);
  const [aboutOpen, setAboutOpen] = useState(false);
  const [clearAllOpen, setClearAllOpen] = useState(false);
  const [clearAllConfirmation, setClearAllConfirmation] = useState("");
  const [clearAllError, setClearAllError] = useState("");
  const [clearingAll, setClearingAll] = useState(false);
  const [systemPrompt, setSystemPrompt] = useState("");
  const [contextWindow, setContextWindow] = useState("default");
  const [thinkingMode, setThinkingMode] = useState("default");
  const [temperatureOverride, setTemperatureOverride] = useState(false);
  const [temperature, setTemperature] = useState(0.7);
  const [modelLoaded, setModelLoaded] = useState(false);
  const [streaming, setStreaming] = useState(false);
  const [viewportWidth, setViewportWidth] = useState(() => window.innerWidth);
  const settingsTriggerRef = useRef(null);
  const settingsReturnRef = useRef(null);
  const systemSettingsDialogRef = useRef(null);
  const systemSettingsCloseRef = useRef(null);
  const systemSettingsReturnRef = useRef(null);
  const sidebarTriggerRef = useRef(null);
  const generationRef = useRef(null);
  const detailRequestRef = useRef(null);
  const renameDialogRef = useRef(null);
  const renameInputRef = useRef(null);
  const deleteDialogRef = useRef(null);
  const deleteCancelRef = useRef(null);
  const aboutDialogRef = useRef(null);
  const aboutCloseRef = useRef(null);
  const aboutReturnRef = useRef(null);
  const clearAllDialogRef = useRef(null);
  const clearAllInputRef = useRef(null);
  const clearAllReturnRef = useRef(null);
  const actionReturnRef = useRef(null);
  const transcriptRef = useRef(null);
  const currentContextUsage = useMemo(() => contextUsage(messages, contextWindow), [messages, contextWindow]);

  useLayoutEffect(() => {
    if (activeId) scrollToEnd(transcriptRef.current);
  }, [activeId, scrollRequest]);

  async function refreshConversations(search = query, signal) {
    const items = await listConversations(search, signal);
    setConversationItems(items);
  }

  async function refreshModels(signal) {
    const requestId = ++modelListRequestRef.current;
    const items = await listModels(signal);
    if (requestId !== modelListRequestRef.current) return;
    const names = items.map((item) => item.name);
    const toolNames = items.filter((item) => item.capabilities?.includes("tools")).map((item) => item.name);
    setModels(names);
    setVisionModels(new Set(items.filter((item) => item.capabilities?.includes("vision")).map((item) => item.name)));
    setToolModels(new Set(toolNames));
    setThinkingModels(new Set(items.filter((item) => item.capabilities?.includes("thinking")).map((item) => item.name)));
    setModel((current) => current || toolNames[0] || names[0] || "");
    setModelsLoaded(true);
    setConnected(true);
  }

  function refreshModelChoices() {
    refreshModels().catch((error) => setModelError(`Could not refresh models: ${error.message}`));
  }

  async function changeModel(nextModel) {
    if (nextModel === model || streaming || modelChangeRef.current || loadingConversation || deletingTurns || turnDeleteTarget) return;
    setModelError("");
    setModelNotice("");
    if (!activeId) {
      setModel(nextModel);
      return;
    }
    const view = conversationViewRef.current;
    const pendingChange = changeConversationModel(activeId, nextModel);
    modelChangeRef.current = pendingChange;
    setChangingModel(true);
    try {
      const updated = await pendingChange;
      setConversationItems((items) => items.map((item) => item.id === updated.id ? { ...item, model: updated.model } : item));
      // A request may finish after the user has opened another conversation.
      if (view !== conversationViewRef.current) return;
      setModel(updated.model);
      setThinkingMode(thinkingModeFromConversation(updated));
      setChatError("");
      setModelNotice(`Now using ${updated.model}. History kept; thinking uses the model’s default.`);
    } catch (error) {
      if (view === conversationViewRef.current) setModelError(error.message);
    } finally {
      modelChangeRef.current = null;
      setChangingModel(false);
    }
  }

  useEffect(() => {
    const controller = new AbortController();
    Promise.allSettled([
      refreshModels(controller.signal),
      refreshConversations("", controller.signal),
      getHealth(controller.signal).then((health) => setConnected(health.ollama?.status === "ok")),
    ]).then((results) => {
      const modelResult = results[0];
      if (modelResult.status === "rejected" && modelResult.reason?.name !== "AbortError") {
        setConnected(false);
        setChatError(`Could not reach Ollama: ${modelResult.reason.message}`);
      }
    });
    return () => controller.abort();
  }, []);

  useEffect(() => {
    if (!activeId && !modelLoaded && model && !thinkingModels.has(model) && thinkingMode !== "default") {
      setThinkingMode("default");
    }
  }, [activeId, model, modelLoaded, thinkingMode, thinkingModels]);

  useEffect(() => {
    if (!activeId && !modelLoaded && model && !toolModels.has(model) && conversationMode === "agent") {
      setConversationMode("chat");
    }
  }, [activeId, conversationMode, model, modelLoaded, toolModels]);

  useEffect(() => {
    const controller = new AbortController();
    const timer = window.setTimeout(() => {
      refreshConversations(query, controller.signal).catch((error) => {
        if (error.name !== "AbortError") setChatError(error.message);
      });
    }, 250);
    return () => {
      window.clearTimeout(timer);
      controller.abort();
    };
  }, [query]);

  useEffect(() => {
    function onResize() { setViewportWidth(window.innerWidth); }
    window.addEventListener("resize", onResize);
    return () => window.removeEventListener("resize", onResize);
  }, []);

  useEffect(() => {
    const dialog = renameDialogRef.current;
    if (!dialog) return;
    if (renameTarget && !dialog.open) {
      dialog.showModal();
      window.setTimeout(() => {
        renameInputRef.current?.focus();
        renameInputRef.current?.select();
      }, 0);
    } else if (!renameTarget && dialog.open) {
      dialog.close();
    }
  }, [renameTarget]);

  useEffect(() => {
    const dialog = turnDeleteDialogRef.current;
    if (turnDeleteTarget && !dialog.open) {
      dialog.showModal();
      turnDeleteCancelRef.current?.focus();
    } else if (!turnDeleteTarget && dialog.open) {
      dialog.close();
    }
  }, [turnDeleteTarget]);

  useEffect(() => {
    const dialog = deleteDialogRef.current;
    if (!dialog) return;
    if (deleteTarget && !dialog.open) {
      dialog.showModal();
      window.setTimeout(() => deleteCancelRef.current?.focus(), 0);
    } else if (!deleteTarget && dialog.open) {
      dialog.close();
    }
  }, [deleteTarget]);

  useEffect(() => {
    const dialog = aboutDialogRef.current;
    if (!dialog) return;
    if (aboutOpen && !dialog.open) {
      dialog.showModal();
      window.setTimeout(() => aboutCloseRef.current?.focus(), 0);
    } else if (!aboutOpen && dialog.open) {
      dialog.close();
    }
  }, [aboutOpen]);

  useEffect(() => {
    const dialog = systemSettingsDialogRef.current;
    if (!dialog) return;
    if (systemSettingsOpen && !dialog.open) {
      dialog.showModal();
      window.setTimeout(() => systemSettingsCloseRef.current?.focus(), 0);
    } else if (!systemSettingsOpen && dialog.open) {
      dialog.close();
    }
  }, [systemSettingsOpen]);

  useEffect(() => {
    const dialog = clearAllDialogRef.current;
    if (!dialog) return;
    if (clearAllOpen && !dialog.open) {
      dialog.showModal();
      window.setTimeout(() => clearAllInputRef.current?.focus(), 0);
    } else if (!clearAllOpen && dialog.open) {
      dialog.close();
    }
  }, [clearAllOpen]);

  useEffect(() => {
    if (!systemSettingsOpen) return undefined;
    const controller = new AbortController();
    setRuntimeSettingsLoading(true);
    setRuntimeSettingsError("");
    getSettings(controller.signal)
      .then(setRuntimeSettings)
      .catch((error) => { if (error.name !== "AbortError") setRuntimeSettingsError(error.message); })
      .finally(() => { if (!controller.signal.aborted) setRuntimeSettingsLoading(false); });
    return () => controller.abort();
  }, [systemSettingsOpen]);

  useEffect(() => {
    function onKeyDown(event) {
      if (event.key !== "Escape") return;
      if (renameTarget || deleteTarget || aboutOpen || systemSettingsOpen || clearAllOpen) {
        return;
      } else if (settingsOpen && viewportWidth < 1200) {
        setSettingsOpen(false);
        settingsTriggerRef.current?.focus();
      } else if (sidebarOpen) {
        setSidebarOpen(false);
        sidebarTriggerRef.current?.focus();
      } else {
        setMenuOpen(false);
      }
    }
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [renameTarget, deleteTarget, aboutOpen, systemSettingsOpen, clearAllOpen, settingsOpen, sidebarOpen, viewportWidth]);

  useEffect(() => {
    const isSettingsOverlay = settingsOpen && viewportWidth < 1200;
    const isSidebarOverlay = sidebarOpen && viewportWidth <= 900;
    const overlay = isSettingsOverlay
      ? document.querySelector(".settings-panel")
      : isSidebarOverlay
        ? document.querySelector(".conversation-sidebar")
        : null;
    if (!overlay) return undefined;

    const selector = "button:not([disabled]), input:not([disabled]), textarea:not([disabled]), select:not([disabled]), [tabindex]:not([tabindex='-1'])";
    const focusable = [...overlay.querySelectorAll(selector)];
    focusable[0]?.focus();

    function trapFocus(event) {
      if (event.key !== "Tab" || focusable.length === 0) return;
      const first = focusable[0];
      const last = focusable[focusable.length - 1];
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault();
        last.focus();
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault();
        first.focus();
      }
    }

    document.addEventListener("keydown", trapFocus);
    return () => document.removeEventListener("keydown", trapFocus);
  }, [settingsOpen, sidebarOpen, viewportWidth]);

  function closeSidebar() {
    setSidebarOpen(false);
    window.setTimeout(() => sidebarTriggerRef.current?.focus(), 0);
  }

  function closeSettings() {
    setSettingsOpen(false);
    window.setTimeout(() => {
      const trigger = settingsReturnRef.current;
      if (trigger?.isConnected && !trigger.closest('[inert]')) trigger.focus();
      else settingsTriggerRef.current?.focus();
    }, 0);
  }

  function openConversationSettings(trigger) {
    settingsReturnRef.current = trigger;
    setMenuOpen(false);
    setSettingsOpen(true);
  }

  function openSystemSettings(trigger) {
    systemSettingsReturnRef.current = trigger;
    setSidebarOpen(false);
    setSystemSettingsOpen(true);
  }

  function closeSystemSettings() {
    setSystemSettingsOpen(false);
  }

  function restoreSystemSettingsFocus() {
    window.setTimeout(() => {
      const trigger = systemSettingsReturnRef.current;
      if (trigger?.isConnected && !trigger.closest('[inert]')) trigger.focus();
      else sidebarTriggerRef.current?.focus();
    }, 0);
  }

  function openAbout(trigger) {
    aboutReturnRef.current = trigger;
    setSidebarOpen(false);
    setAboutOpen(true);
  }

  function closeAbout() {
    setAboutOpen(false);
  }

  function restoreAboutFocus() {
    window.setTimeout(() => {
      const trigger = aboutReturnRef.current;
      if (trigger?.isConnected && !trigger.closest('[inert]')) trigger.focus();
      else sidebarTriggerRef.current?.focus();
    }, 0);
  }

  async function refreshRuntimeSettings() {
    setRuntimeSettingsLoading(true);
    setRuntimeSettingsError("");
    try {
      setRuntimeSettings(await getSettings());
    } catch (error) {
      setRuntimeSettingsError(error.message);
    } finally {
      setRuntimeSettingsLoading(false);
    }
  }

  function openClearAll(trigger) {
    if (streaming) return;
    clearAllReturnRef.current = trigger;
    setClearAllConfirmation("");
    setClearAllError("");
    setClearAllOpen(true);
  }

  function closeClearAll() {
    if (clearingAll) return;
    setClearAllOpen(false);
    setClearAllConfirmation("");
    setClearAllError("");
  }

  function restoreClearAllFocus() {
    window.setTimeout(() => {
      const trigger = clearAllReturnRef.current;
      if (trigger?.isConnected && !trigger.closest('[inert]')) trigger.focus();
    }, 0);
  }

  async function confirmClearAll(event) {
    event.preventDefault();
    if (clearAllConfirmation !== "DELETE" || clearingAll) return;
    setClearingAll(true);
    setClearAllError("");
    try {
      await clearAllConversations();
      resetConversation(false);
      setConversationItems([]);
      setClearAllOpen(false);
      await refreshRuntimeSettings();
    } catch (error) {
      setClearAllError(error.message);
    } finally {
      setClearingAll(false);
    }
  }

  async function chooseConversation(id) {
    const view = ++conversationViewRef.current;
    setLoadingConversation(true);
    setModelError("");
    setModelNotice("");
    generationRef.current?.abort();
    detailRequestRef.current?.abort();
    const controller = new AbortController();
    detailRequestRef.current = controller;
    setChatError("");
    setSidebarOpen(false);
    try {
      // Reopening a session while its model is saving must read the saved value.
      await modelChangeRef.current?.catch(() => {});
      if (view !== conversationViewRef.current) return;
      const payload = await getConversation(id, controller.signal);
      if (view !== conversationViewRef.current) return;
      setActiveId(payload.conversation.id);
      setActiveTitle(payload.conversation.title);
      setModel(payload.conversation.model);
      setConversationMode(payload.conversation.mode || "agent");
      setSystemPrompt(payload.conversation.system_prompt || "");
      setContextWindow(payload.conversation.context_window == null ? "default" : String(payload.conversation.context_window));
      setThinkingMode(thinkingModeFromConversation(payload.conversation));
      setTemperatureOverride(payload.conversation.temperature != null);
      setTemperature(payload.conversation.temperature ?? 0.7);
      setModelLoaded(payload.conversation.message_count > 0);
      setMessages(payload.messages);
    } catch (error) {
      if (error.name !== "AbortError") setChatError(error.message);
    } finally {
      if (view === conversationViewRef.current) setLoadingConversation(false);
    }
  }

  function resetConversation(closeNavigation = true) {
    ++conversationViewRef.current;
    detailRequestRef.current?.abort();
    setLoadingConversation(false);
    setModelError("");
    setModelNotice("");
    generationRef.current?.abort();
    setActiveId(null);
    setActiveTitle("");
    setMessages([]);
    setStreaming(false);
    setModelLoaded(false);
    setConversationMode(toolModels.has(model) ? "agent" : "chat");
    setSystemPrompt("");
    setContextWindow("default");
    setThinkingMode("default");
    setTemperatureOverride(false);
    setTemperature(0.7);
    setChatError("");
    if (closeNavigation) setSidebarOpen(false);
  }

  function newChat() {
    resetConversation(true);
  }

  function restoreActionFocus() {
    window.setTimeout(() => {
      const trigger = actionReturnRef.current;
      if (trigger?.isConnected) {
        trigger.focus();
      } else {
        document.querySelector(".new-chat-button")?.focus();
      }
    }, 0);
  }

  function openRename(conversation, trigger) {
    actionReturnRef.current = trigger;
    setRenameTitle(conversation.title);
    setRenameError("");
    setRenameTarget(conversation);
  }

  function closeRename() {
    if (renaming) return;
    setRenameTarget(null);
    setRenameError("");
  }

  async function saveRename(event) {
    event.preventDefault();
    const title = renameTitle.trim();
    if (!renameTarget) return;
    if (!title) {
      setRenameError("Enter a conversation name.");
      renameInputRef.current?.focus();
      return;
    }
    setRenaming(true);
    setRenameError("");
    try {
      const updated = await renameConversation(renameTarget.id, title);
      if (renameTarget.id === activeId) setActiveTitle(updated.title);
      setConversationItems((items) => items.map((item) => item.id === renameTarget.id ? { ...item, title: updated.title } : item));
      setRenameTarget(null);
      await refreshConversations(query);
    } catch (error) {
      setRenameError(error.message);
    } finally {
      setRenaming(false);
    }
  }

  function openDelete(conversation, trigger) {
    if (streaming && activeId === conversation.id) return;
    actionReturnRef.current = trigger;
    setDeleteError("");
    setDeleteTarget(conversation);
  }

  function closeDelete() {
    if (deleting) return;
    setDeleteTarget(null);
    setDeleteError("");
  }

  async function confirmDelete() {
    if (!deleteTarget || deleting) return;
    const deletedId = deleteTarget.id;
    setDeleting(true);
    setDeleteError("");
    try {
      await deleteConversation(deletedId);
      setConversationItems((items) => items.filter((item) => item.id !== deletedId));
      setDeleteTarget(null);
      if (deletedId === activeId) resetConversation(false);
      await refreshConversations(query);
    } catch (error) {
      setDeleteError(error.message);
    } finally {
      setDeleting(false);
    }
  }

  function openTurnDelete(message, trigger) {
    if (streaming) return;
    turnDeleteReturnRef.current = trigger;
    setTurnDeleteError("");
    setTurnDeleteTarget({ ...message, conversationId: activeId });
  }

  function closeTurnDelete() {
    if (deletingTurns) return;
    setTurnDeleteTarget(null);
    setTurnDeleteError("");
  }

  async function confirmTurnDelete() {
    if (!turnDeleteTarget || deletingTurns || streaming) return;
    setDeletingTurns(true);
    setTurnDeleteError("");
    try {
      const payload = await deleteTurns(turnDeleteTarget.conversationId, turnDeleteTarget.id);
      setMessages(payload.messages || []);
      setModelLoaded(payload.conversation.message_count > 0);
      setChatError("");
      setTurnDeleteTarget(null);
      refreshConversations(query).catch((error) => setChatError(error.message));
    } catch (error) {
      setTurnDeleteError(error.message);
    } finally {
      setDeletingTurns(false);
    }
  }

  async function sendMessage(value, images = []) {
    if (!model || !models.includes(model) || streaming || modelChangeRef.current || loadingConversation || deletingTurns || turnDeleteTarget) return;
    setChatError("");
    setModelError("");
    setModelNotice("");
    const controller = new AbortController();
    generationRef.current = controller;
    setStreaming(true);
    setModelLoaded(true);
    let conversationId = activeId;
    try {
      if (!conversationId) {
        const conversation = await createConversation({
          title: value.trim().replace(/\s+/g, " ").slice(0, 60) || `Image: ${images[0]?.name || "conversation"}`,
          model,
          mode: conversationMode,
          system_prompt: systemPrompt,
          context_window: contextWindow === "default" ? null : Number(contextWindow),
          thinking_mode: thinkingModeForRequest(thinkingMode),
          temperature: temperatureOverride ? temperature : null,
        }, controller.signal);
        conversationId = conversation.id;
        setActiveId(conversationId);
        setActiveTitle(conversation.title);
      }

      const pendingUser = {
        id: "pending-user",
        role: "user",
        content: value,
        status: "complete",
        attachments: images.map((image) => ({ id: image.id, file_name: image.name, url: image.url, media_type: image.media_type, size: image.size })),
      };
      const pendingAssistant = { id: "pending-assistant", role: "assistant", content: "", thinking: "", status: "streaming", agent_phase: "starting", agent_turn: 1 };
      setMessages((current) => [...current, pendingUser, pendingAssistant]);
      setScrollRequest((current) => current + 1);

      await streamMessage(conversationId, value, {
        signal: controller.signal,
        images: images.map(({ name, media_type, data }) => ({ name, media_type, data })),
        onEvent(event) {
          if (event.type === "started") {
            setMessages((current) => current.map((message) => {
              if (message.id === "pending-user") return event.user_message;
              if (message.id === "pending-assistant") return { ...event.assistant_message, agent_phase: "thinking", agent_turn: 1 };
              return message;
            }));
          } else if (event.type === "turn.started") {
            setMessages((current) => current.map((message, index) =>
              index === current.length - 1 ? { ...message, agent_phase: "thinking", agent_turn: event.turn } : message));
          } else if (event.type === "thinking.delta") {
            setMessages((current) => current.map((message, index) =>
              index === current.length - 1
                ? { ...message, thinking: (message.thinking || "") + event.content, agent_phase: "thinking", agent_turn: event.turn }
                : message));
          } else if (event.type === "delta") {
            setMessages((current) => current.map((message, index) =>
              index === current.length - 1 ? { ...message, content: message.content + event.content, agent_phase: "responding", agent_turn: event.turn || message.agent_turn } : message));
          } else if (event.type === "tool.started") {
            setMessages((current) => current.map((message, index) =>
              index === current.length - 1
                ? { ...message, agent_steps: [...(message.agent_steps || []), event.step], agent_phase: "shell", agent_turn: event.turn }
                : message));
          } else if (event.type === "tool.done") {
            setMessages((current) => current.map((message, index) =>
              index === current.length - 1
                ? { ...message, agent_steps: (message.agent_steps || []).map((step) => step.id === event.step.id ? event.step : step), agent_phase: "reviewing", agent_turn: event.turn }
                : message));
          } else if (event.type.startsWith("subagent.")) {
            setMessages((current) => current.map((message, index) =>
              index === current.length - 1
                ? { ...message, subagent_runs: reduceSubagentEvent(message.subagent_runs, event) }
                : message));
          } else if (event.type === "done") {
            setMessages((current) => current.map((message, index) =>
              index === current.length - 1 ? { ...message, status: "complete", metadata: event.metadata, agent_phase: "finished", agent_turn: event.metadata?.agent_turns || message.agent_turn } : message));
          } else if (event.type === "error") {
            throw new Error(event.message);
          }
        },
      });
    } catch (error) {
      const cancelled = error.name === "AbortError";
      setMessages((current) => current.map((message, index) =>
        index === current.length - 1 && message.role === "assistant"
          ? {
              ...message,
              status: cancelled ? "cancelled" : "error",
              agent_phase: cancelled ? "stopped" : "error",
              agent_steps: (message.agent_steps || []).map((step) =>
                step.status === "running" ? { ...step, status: cancelled ? "cancelled" : "error" } : step),
              subagent_runs: cancelled ? cancelRunningSubagents(message.subagent_runs) : message.subagent_runs,
            }
          : message));
      if (!cancelled) setChatError(error.message);
      if (cancelled && conversationId && generationRef.current === controller) {
        try {
          const payload = await getConversation(conversationId);
          if (generationRef.current === controller) {
            setMessages(payload.messages || []);
          }
        } catch {
          // Keep the visible stopped turn if the refresh is unavailable.
        }
      }
    } finally {
      setStreaming(false);
      generationRef.current = null;
      refreshConversations(query).catch(() => {});
    }
  }

  const overlaySettings = settingsOpen && viewportWidth < 1200;
  const sidebarVisible = viewportWidth > 900 || sidebarOpen;
  const modelUnavailable = modelsLoaded && model && !models.includes(model);
  const modelDisabled = streaming || changingModel || loadingConversation || deletingTurns || !!turnDeleteTarget;
  const modelFeedback = changingModel ? "Changing model…" : modelError || (modelUnavailable ? "This model is no longer installed. Choose another model to continue this conversation." : modelNotice);

  return (
    <div className={`app-shell ${settingsOpen ? "settings-is-open" : ""}`}>
      <div className={`sidebar-layer ${sidebarOpen ? "open" : ""}`} aria-hidden={!sidebarVisible} inert={!sidebarVisible ? "" : undefined}>
        <ConversationSidebar
          activeId={activeId}
          conversations={conversationItems}
          query={query}
          streaming={streaming}
          onQuery={setQuery}
          onSelect={chooseConversation}
          onNew={newChat}
          onRename={openRename}
          onDelete={openDelete}
          onSettings={openSystemSettings}
          onAbout={openAbout}
          onClose={closeSidebar}
        />
      </div>

      <main className={`chat-workspace ${!activeId ? "is-new-chat" : ""} ${modelFeedback ? "has-model-feedback" : ""}`}>
        <header className="topbar">
          <div className="topbar-left">
            <IconButton
              ref={sidebarTriggerRef}
              label="Open conversations"
              className="sidebar-trigger"
              onClick={() => setSidebarOpen(true)}
            >
              <SidebarSimple size={23} />
            </IconButton>
            {activeId && (
              <>
                <div className="terminal-button" aria-hidden="true"><ChatCircle size={24} /></div>
                <div className="select-wrap model-select">
                  <select aria-label="Active model" value={model} disabled={modelDisabled} onFocus={refreshModelChoices} aria-describedby={modelFeedback ? "active-model-feedback" : undefined} onChange={(event) => changeModel(event.target.value)}>
                    <ModelOptions models={models} model={model} />
                  </select>
                  <CaretDown size={17} aria-hidden="true" />
                </div>
              </>
            )}
          </div>
          <div className="topbar-right">
            <div className={`agent-status ${streaming ? "working" : ""}`} role="status" aria-live="polite">
              {streaming ? <CircleNotch size={16} /> : <ChatCircle size={16} />}
              <span>{conversationMode === "agent" ? (streaming ? "Agent working" : "Agent ready") : (streaming ? "Chat responding" : "Chat ready")}</span>
            </div>
            <div className={`connected-status ${connected ? "" : "disconnected"}`}><span /> <span className="connected-text">{connected ? "Connected" : "Ollama unavailable"}</span></div>
            <div
              className="menu-wrap"
              onBlur={(event) => {
                if (!event.currentTarget.contains(event.relatedTarget)) setMenuOpen(false);
              }}
            >
              <IconButton
                ref={settingsTriggerRef}
                label="Conversation menu"
                aria-haspopup="menu"
                aria-expanded={menuOpen}
                onClick={() => setMenuOpen((value) => !value)}
              >
                <DotsThreeVertical size={23} weight="bold" />
              </IconButton>
              {menuOpen && (
                <div className="overflow-menu" role="menu">
                  <button
                    role="menuitem"
                    onClick={() => openConversationSettings(settingsTriggerRef.current)}
                  ><SlidersHorizontal size={19} />Conversation settings</button>
                  <button
                    role="menuitem"
                    disabled={!activeId}
                    onClick={() => {
                      setMenuOpen(false);
                      openRename({ id: activeId, title: activeTitle }, settingsTriggerRef.current);
                    }}
                  ><NotePencil size={19} />Rename chat</button>
                  <button
                    className="danger-menu-item"
                    role="menuitem"
                    disabled={!activeId || streaming}
                    onClick={() => {
                      setMenuOpen(false);
                      openDelete({ id: activeId, title: activeTitle }, settingsTriggerRef.current);
                    }}
                  ><Trash size={19} />Delete chat</button>
                </div>
              )}
            </div>
          </div>
        </header>

        {modelFeedback && <div id="active-model-feedback" className={`model-feedback ${modelError || modelUnavailable ? "model-feedback-error" : ""}`} role={modelError ? "alert" : "status"}>{modelFeedback}</div>}

        <section className="transcript" aria-label="Conversation" ref={transcriptRef}>
          <ChatTranscript
            streaming={streaming}
            onDeleteTurn={openTurnDelete}
            empty={!activeId}
            messages={messages}
            conversationMode={conversationMode}
            error={chatError}
          />
        </section>
        <Composer
          streaming={streaming}
          onStop={() => generationRef.current?.abort()}
          onSend={sendMessage}
          empty={!activeId}
          model={model}
          models={models}
          setModel={changeModel}
          modelDisabled={modelDisabled}
          onRefreshModels={refreshModelChoices}
          conversationMode={conversationMode}
          setConversationMode={setConversationMode}
          contextWindow={contextWindow}
          setContextWindow={setContextWindow}
          thinkingMode={thinkingMode}
          setThinkingMode={setThinkingMode}
          contextUsedTokens={currentContextUsage.usedTokens}
          disabled={!model || !models.includes(model) || changingModel || loadingConversation || deletingTurns || !!turnDeleteTarget || conversationMode === "agent" && !toolModels.has(model)}
          supportsImages={visionModels.has(model)}
          supportsTools={toolModels.has(model)}
          supportsThinking={thinkingModels.has(model)}
          onOpenSettings={(event) => openConversationSettings(event.currentTarget)}
        />
      </main>

      <div className={`settings-layer ${settingsOpen ? "open" : ""}`} aria-hidden={!settingsOpen} inert={!settingsOpen ? "" : undefined}>
        <ConversationSettings
          models={models}
          model={model}
          setModel={changeModel}
          modelDisabled={modelDisabled}
          modelFeedback={modelFeedback}
          modelError={!!modelError || !!modelUnavailable}
          onRefreshModels={refreshModelChoices}
          conversationMode={conversationMode}
          setConversationMode={setConversationMode}
          supportsTools={toolModels.has(model)}
          onClose={closeSettings}
          systemPrompt={systemPrompt}
          setSystemPrompt={setSystemPrompt}
          contextWindow={contextWindow}
          setContextWindow={setContextWindow}
          thinkingMode={thinkingMode}
          setThinkingMode={setThinkingMode}
          supportsThinking={thinkingModels.has(model)}
          modelLoaded={modelLoaded}
          temperatureOverride={temperatureOverride}
          setTemperatureOverride={setTemperatureOverride}
          temperature={temperature}
          setTemperature={setTemperature}
        />
      </div>

      {(sidebarOpen || overlaySettings) && (
        <button
          className="drawer-backdrop"
          aria-label="Close open panel"
          onClick={() => { if (sidebarOpen) closeSidebar(); if (viewportWidth < 1200 && settingsOpen) closeSettings(); }}
        />
      )}

      <dialog
        className="system-settings-dialog"
        ref={systemSettingsDialogRef}
        aria-labelledby="system-settings-title"
        onCancel={(event) => { event.preventDefault(); closeSystemSettings(); }}
        onClose={restoreSystemSettingsFocus}
      >
        <SystemSettings
          connected={connected}
          modelCount={models.length}
          runtime={runtimeSettings}
          loading={runtimeSettingsLoading}
          error={runtimeSettingsError}
          streaming={streaming}
          onRefresh={refreshRuntimeSettings}
          onClose={closeSystemSettings}
          onRequestClear={(event) => openClearAll(event.currentTarget)}
          closeRef={systemSettingsCloseRef}
        />
      </dialog>

      <dialog
        className="rename-dialog"
        ref={renameDialogRef}
        aria-labelledby="rename-title"
        onCancel={(event) => { event.preventDefault(); closeRename(); }}
        onClose={restoreActionFocus}
      >
        <form onSubmit={saveRename}>
          <header>
            <div>
              <h2 id="rename-title">Rename conversation</h2>
              <p>Use a short name that will be easy to find later.</p>
            </div>
            <IconButton label="Cancel rename" type="button" onClick={closeRename} disabled={renaming}>
              <X size={22} />
            </IconButton>
          </header>
          <label htmlFor="conversation-title">Conversation name</label>
          <input
            id="conversation-title"
            ref={renameInputRef}
            value={renameTitle}
            onChange={(event) => { setRenameTitle(event.target.value); if (renameError) setRenameError(""); }}
            autoComplete="off"
            disabled={renaming}
          />
          {renameError && <p className="rename-error" role="alert">{renameError}</p>}
          <footer>
            <button className="rename-cancel" type="button" onClick={closeRename} disabled={renaming}>Cancel</button>
            <button className="rename-save" type="submit" disabled={renaming || !renameTitle.trim()}>{renaming ? "Saving…" : "Save name"}</button>
          </footer>
        </form>
      </dialog>

      <dialog
        className="delete-dialog"
        ref={turnDeleteDialogRef}
        aria-labelledby="delete-turn-title"
        aria-describedby="delete-turn-description"
        onCancel={(event) => { event.preventDefault(); closeTurnDelete(); }}
        onClose={() => window.setTimeout(() => {
          const trigger = turnDeleteReturnRef.current;
          if (trigger?.isConnected) trigger.focus();
          else document.querySelector(".composer textarea")?.focus();
        }, 0)}
      >
        <div>
          <header>
            <span className="delete-dialog-icon" aria-hidden="true"><Trash size={22} /></span>
            <div>
              <h2 id="delete-turn-title">Delete from this turn?</h2>
              <p id="delete-turn-description">This permanently deletes this prompt, its response, and all later turns, including saved images and agent activity. This cannot be undone.</p>
              {turnDeleteTarget && <p className="delete-turn-preview">{turnDeleteTarget.content?.slice(0, 180) || "Image prompt"}{turnDeleteTarget.content?.length > 180 ? "…" : ""}</p>}
            </div>
          </header>
          {turnDeleteError && <p className="delete-error" role="alert">{turnDeleteError}</p>}
          <footer>
            <button ref={turnDeleteCancelRef} className="delete-cancel" type="button" onClick={closeTurnDelete} disabled={deletingTurns}>Cancel</button>
            <button className="delete-confirm" type="button" onClick={confirmTurnDelete} disabled={deletingTurns}>{deletingTurns ? "Deleting…" : "Delete from here"}</button>
          </footer>
        </div>
      </dialog>

      <dialog
        className="delete-dialog"
        ref={deleteDialogRef}
        aria-labelledby="delete-title"
        aria-describedby="delete-description"
        onCancel={(event) => { event.preventDefault(); closeDelete(); }}
        onClose={restoreActionFocus}
      >
        <div>
          <header>
            <span className="delete-dialog-icon" aria-hidden="true"><Trash size={22} /></span>
            <div>
              <h2 id="delete-title">Delete conversation?</h2>
              <p id="delete-description">
                <strong>{deleteTarget?.title || "This conversation"}</strong> and all of its messages and agent activity will be permanently removed.
              </p>
            </div>
          </header>
          {deleteError && <p className="delete-error" role="alert">{deleteError}</p>}
          <footer>
            <button ref={deleteCancelRef} className="delete-cancel" type="button" onClick={closeDelete} disabled={deleting}>Cancel</button>
            <button className="delete-confirm" type="button" onClick={confirmDelete} disabled={deleting}>{deleting ? "Deleting…" : "Delete conversation"}</button>
          </footer>
        </div>
      </dialog>

      <dialog
        className="delete-dialog clear-all-dialog"
        ref={clearAllDialogRef}
        aria-labelledby="clear-all-title"
        aria-describedby="clear-all-description"
        onCancel={(event) => { event.preventDefault(); closeClearAll(); }}
        onClose={restoreClearAllFocus}
      >
        <form onSubmit={confirmClearAll}>
          <header>
            <span className="delete-dialog-icon" aria-hidden="true"><Trash size={22} /></span>
            <div>
              <h2 id="clear-all-title">Clear all conversations?</h2>
              <p id="clear-all-description">This permanently deletes every conversation, message, image, and recorded Agent action. This cannot be undone.</p>
            </div>
          </header>
          <label htmlFor="clear-all-confirmation">Type <strong>DELETE</strong> to confirm</label>
          <input
            id="clear-all-confirmation"
            ref={clearAllInputRef}
            value={clearAllConfirmation}
            onChange={(event) => { setClearAllConfirmation(event.target.value); if (clearAllError) setClearAllError(""); }}
            autoComplete="off"
            disabled={clearingAll}
          />
          {clearAllError && <p className="delete-error" role="alert">{clearAllError}</p>}
          <footer>
            <button className="delete-cancel" type="button" onClick={closeClearAll} disabled={clearingAll}>Cancel</button>
            <button className="delete-confirm" type="submit" disabled={clearingAll || clearAllConfirmation !== "DELETE"}>{clearingAll ? "Clearing…" : "Clear everything"}</button>
          </footer>
        </form>
      </dialog>

      <dialog
        className="about-dialog"
        ref={aboutDialogRef}
        aria-labelledby="about-title"
        aria-describedby="about-description"
        onCancel={(event) => { event.preventDefault(); closeAbout(); }}
        onClose={restoreAboutFocus}
      >
        <div>
          <header>
            <div className="about-mark" aria-hidden="true"><ChatCircle size={26} /></div>
            <div>
              <p className="about-eyebrow">Local Ollama client</p>
              <h2 id="about-title">Ollama WebUI</h2>
              <p id="about-description">A lightweight interface for private conversations with models running on your Ollama server.</p>
            </div>
            <IconButton ref={aboutCloseRef} label="Close About" type="button" onClick={closeAbout}>
              <X size={22} />
            </IconButton>
          </header>
          <dl className="about-facts">
            <div><dt>Chat</dt><dd>Talk directly to any completion-capable model without tools.</dd></div>
            <div><dt>Agent</dt><dd>Let a tool-capable model continue working and use the configured shell when needed.</dd></div>
            <div><dt>Your data</dt><dd>Conversations and activity are stored locally in SQLite.</dd></div>
          </dl>
          <p className="about-security"><Info size={18} aria-hidden="true" />Agent shell access is powerful. Docker and the directories you mount define its security boundary.</p>
          <footer><button type="button" onClick={closeAbout}>Done</button></footer>
        </div>
      </dialog>
    </div>
  );
}
