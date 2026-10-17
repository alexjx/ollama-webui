import { useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { contextUsage } from "./context-usage";
import { applyKnownConversationTitles, mergeConversationTitles, newestConversationTitle } from "./conversation-titles";
import { cancelRunningSubagents, reduceSubagentEvent } from "./agent-events";
import { finalizeActivity, readActivityBlocks, recordActivityEvent } from "./activity-timeline";
import { formatDuration, formatRate, responseMetrics } from "./response-metrics";
import { isNearTranscriptEnd, scrollToEnd } from "./scroll-position";
import { clampInputHeight } from "./input-resize";
import { selectElementText } from "./select-text";
import { fileFromLargePaste, isImageAttachment, readAttachment, splitAttachments, validateAttachmentFiles } from "./text-attachments";
import MarkdownContent from "./markdown-content";
import CopyButton from "./copy-button";
import ThinkingLabel from "./thinking-label";
import TavilySettings from "./tavily-settings";
import WebSearchResults from "./web-search-results";
import { WorkspaceLinkContext, resolveWorkspacePath } from "./workspace-links";
import WorkspaceBrowser from "./workspace-browser";
import TemplatesDialog from "./templates-dialog";
import { insertTemplateMessage, templateDraft, templateSettings } from "./conversation-templates";
import { createSettingsRequestGuard } from "./web-search";
import ConversationWebSearch, { conversationWebSearchEnabled } from "./conversation-web-search";
import { applyThinkingTiming, finishThinkingTiming, thinkingDuration } from "./thinking-duration";
import { thinkingModeForRequest, thinkingModeFromConversation } from "./thinking-mode";
import { CONTEXT_WINDOW_VALUES, DEFAULT_MODEL_SETTINGS, createModelPreferences, modelSettingsForCapabilities, normalizeModelSettings } from "./model-preferences";
import {
  clearConversations as clearAllConversations,
  changeConversationModel,
  changeConversationWebSearch,
  compactConversationContext,
  createConversation,
  deleteConversation,
  deleteTurns,
  getConversation,
  getHealth,
  getSettings,
  generateConversationTitle,
  forkConversation,
  listConversations,
  listModels,
  renameConversation,
  saveWebSearchSettings,
  streamMessage,
  testWebSearchSettings,
} from "./api";
import {
  ArrowCounterClockwise,
  ArrowUp,
  ArrowsIn,
  Brain,
  CaretDown,
  ChatCircle,
  CheckCircle,
  CircleNotch,
  Copy,
  DotsThreeVertical,
  FileText,
  FolderOpen,
  Gear,
  GitFork,
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

const contextWindows = CONTEXT_WINDOW_VALUES.map((value) => ({
  value, label: value === "default" ? "Application default" : `${Number(value).toLocaleString("en-US")} tokens`,
}));

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

function ConversationSidebar({ activeId, conversations, query, streaming, titleRequests, titleNotices, onQuery, onSelect, onNew, onTemplates, onRename, onGenerateTitle, onFork, forkDisabled, forkingId, onDelete, onSettings, onAbout, onClose }) {
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

      <button className="templates-sidebar-button" onClick={(event) => onTemplates(event.currentTarget)}>
        <FileText size={22} aria-hidden="true" /><span>Templates</span>
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
                          disabled={!!forkingId || !!titleRequests[item.id]}
                          onClick={(event) => {
                            const trigger = event.currentTarget.closest(".conversation-entry")?.querySelector(".conversation-actions-trigger");
                            setOpenActionsId(null);
                            onRename(item, trigger);
                          }}
                        ><NotePencil size={17} />Rename</button>
                        <button
                          role="menuitem"
                          disabled={!!forkingId || !!titleRequests[item.id] || streaming && activeId === item.id || item.message_count < 2}
                          onClick={(event) => {
                            const trigger = event.currentTarget.closest(".conversation-entry")?.querySelector(".conversation-actions-trigger");
                            setOpenActionsId(null);
                            onGenerateTitle(item);
                            trigger?.focus();
                          }}
                        ><ArrowCounterClockwise size={17} />Generate title</button>
                        <button
                          role="menuitem"
                          disabled={forkDisabled || !!titleRequests[item.id]}
                          onClick={(event) => {
                            const trigger = event.currentTarget.closest(".conversation-entry")?.querySelector(".conversation-actions-trigger");
                            setOpenActionsId(null);
                            onFork(item);
                            trigger?.focus();
                          }}
                        ><GitFork size={17} />{forkingId === item.id ? "Forking…" : "Fork conversation"}</button>
                        <button
                          className="danger-menu-item"
                          role="menuitem"
                          disabled={!!forkingId || !!titleRequests[item.id] || streaming && activeId === item.id}
                          onClick={(event) => {
                            const trigger = event.currentTarget.closest(".conversation-entry")?.querySelector(".conversation-actions-trigger");
                            setOpenActionsId(null);
                            onDelete(item, trigger);
                          }}
                        ><Trash size={17} />Delete</button>
                      </div>
                    )}
                  </div>
                  {(titleRequests[item.id] || titleNotices[item.id]) && (
                    <p className={`session-title-feedback ${titleNotices[item.id]?.error ? "error" : ""}`} role={titleNotices[item.id]?.error ? "alert" : "status"}>
                      {titleRequests[item.id] ? <><CircleNotch size={13} />Generating title…</> : titleNotices[item.id].message}
                    </p>
                  )}
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
  onResetDefaults,
}) {
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
          <p id="settings-model-help">{modelLoaded ? "Switch models between responses. History and mode stay the same; thinking resets to the new model’s default." : "Settings for this model are remembered in this browser for new sessions."}</p>
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
              {!CONTEXT_WINDOW_VALUES.includes(contextWindow) && <option value={contextWindow}>{Number(contextWindow).toLocaleString()} tokens</option>}
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
              disabled={modelLoaded || !supportsThinking && thinkingMode === "default"}
              onChange={(event) => setThinkingMode(event.target.value)}
            >
              {supportsThinking
                ? thinkingModes.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)
                : <><option value="default">{thinkingMode === "default" ? "Not supported" : "Ollama default (no override)"}</option>{thinkingMode !== "default" && <option value={thinkingMode} disabled>{thinkingMode} — not supported</option>}</>}
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

      <button className="reset-button" onClick={onResetDefaults} disabled={modelLoaded}>
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

function SystemSettings({ open, connected, modelCount, runtime, loading, error, streaming, onRefresh, onSaveWebSearch, onTestWebSearch, onClose, onRequestClear, closeRef }) {
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
          <div className="system-section-heading"><div><h3>Runtime</h3><p>Current service and Agent boundaries.</p></div><button className="text-action" type="button" onClick={() => onRefresh()} disabled={loading}>{loading ? "Refreshing…" : "Refresh"}</button></div>
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
            <div><dt>Tool feedback limit</dt><dd>{agent ? formatBytes(agent.tool_feedback_bytes) : "—"}</dd></div>
            <div><dt>Child result limit</dt><dd>{agent ? formatBytes(agent.subagent_result_bytes) : "—"}</dd></div>
          </dl>
          <p className="managed-note"><Info size={17} />These values come from Docker Compose environment settings. Change them there, then recreate the WebUI container.</p>
        </section>

        <section className="settings-section data-settings-section">
          {open && <TavilySettings settings={runtime?.web_search} onSave={onSaveWebSearch} onTest={onTestWebSearch} />}
          <div className="system-section-heading"><div><h3>Local data</h3><p>Conversations, attachments, and managed context stored locally.</p></div><span className="storage-size">{storage ? formatBytes(storage.database_bytes) : "—"}</span></div>
          <dl className="data-counts">
            <div><dt>Conversations</dt><dd>{storage?.conversations ?? "—"}</dd></div>
            <div><dt>Messages</dt><dd>{storage?.messages ?? "—"}</dd></div>
            <div><dt>Attachments</dt><dd>{storage?.attachments ?? "—"}</dd></div>
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
          {streaming && <p className="locked-setting"><span className="lock-dot" />Wait for active operations to finish before clearing data.</p>}
        </section>

      </div>
    </div>
  );
}

function AgentActivity({ message, mode, showProgress = true, progressOnly = false, roundLabel = "Turn" }) {
  const thinkingRef = useRef(null);
  const steps = message.agent_steps || [];
  const subagentRuns = message.subagent_runs || [];
  const hasActivity = progressOnly || message.status === "streaming" || Boolean(message.thinking) || steps.length > 0 || subagentRuns.length > 0;

  const phaseLabels = {
    starting: "Starting",
    thinking: "Thinking",
    shell: "Running shell",
    web_search: "Searching the web",
    reviewing: "Reviewing result",
    responding: "Writing answer",
    finished: "Finished",
    stopped: "Stopped",
    error: "Failed",
  };
  const phase = message.agent_phase || (message.status === "complete" ? "finished" : message.status === "cancelled" ? "stopped" : message.status === "error" ? "error" : "thinking");
  const turn = mode === "agent" ? message.agent_turn || message.metadata?.agent_turns : null;
  const active = message.status === "streaming";
  const liveThinking = thinkingDuration(message)?.live ?? (active && phase === "thinking");

  useEffect(() => {
    if (liveThinking && thinkingRef.current) {
      thinkingRef.current.scrollTop = thinkingRef.current.scrollHeight;
    }
  }, [liveThinking, message.thinking]);

  if (!hasActivity) return null;

  return (
    <section className={`agent-activity ${phase}`} aria-label={mode === "agent" ? "Agent activity" : "Response activity"}>
      {showProgress && <div className="agent-progress" role="status" aria-live="polite">
        <span className="agent-progress-icon" aria-hidden="true">
          {active ? <CircleNotch size={17} /> : phase === "finished" ? <CheckCircle size={17} /> : <Stop size={15} weight="fill" />}
        </span>
        <strong>{phaseLabels[phase] || "Working"}</strong>
        {turn && <span>{roundLabel} {turn}</span>}
        {steps.length > 0 && <span>{steps.length} {steps.length === 1 ? "action" : "actions"}</span>}
      </div>}

      {!progressOnly && message.thinking && (
        <details className="thinking-step" open={liveThinking}>
          <summary>
            <span className="agent-step-icon"><Brain size={17} /></span>
            <ThinkingLabel message={message} />
            <span className="agent-step-status">{liveThinking ? "Live" : "View"}</span>
          </summary>
          <div className="thinking-content" ref={thinkingRef}>{message.thinking}{liveThinking && <span className="stream-cursor" aria-hidden="true" />}</div>
        </details>
      )}

      {!progressOnly && steps.length > 0 && (
        <div className="agent-steps" aria-label="Tool activity">
          {steps.map((step) => {
            let parsedInput = {};
            try { parsedInput = JSON.parse(step.input); } catch { /* Show the recorded input. */ }
            const shellStep = step.tool_name === "shell" || !step.tool_name;
            const label = shellStep
              ? parsedInput.command || step.input
              : step.tool_name === "web_search"
                ? parsedInput.query || "Web search"
              : step.tool_name === "delegate_task"
                ? parsedInput.task || "Delegated task"
                : step.tool_name === "label_image_directory"
                  ? `Label images in ${parsedInput.directory || "directory"}`
                  : step.tool_name?.replaceAll("_", " ") || "Tool action";
            const statusLabel = step.status === "running" ? "Running" : step.status === "complete" ? shellStep ? `Exited ${step.exit_code ?? 0}` : "Complete" : step.status === "cancelled" ? "Stopped" : "Failed";
            const StepIcon = step.tool_name === "web_search" ? MagnifyingGlass : step.tool_name === "delegate_task" ? Brain : step.tool_name === "label_image_directory" ? SelectionAll : shellStep ? TerminalWindow : Gear;
            return (
              <details className={`agent-step ${step.status}`} key={step.id} open={step.status === "running"}>
                <summary>
                  <span className="agent-step-icon"><StepIcon size={17} /></span>
                  <span className={shellStep ? "agent-step-command" : "agent-step-label"}>{label}</span>
                  <span className="agent-step-status">{statusLabel}</span>
                </summary>
                {step.output && (step.tool_name === "web_search" ? <WebSearchResults output={step.output} /> : <pre>{step.output}</pre>)}
              </details>
            );
          })}
        </div>
      )}

      {!progressOnly && subagentRuns.length > 0 && (
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

function AssistantResponse({ message, showActions = true }) {
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
      {showActions && message.content && message.status !== "streaming" && (
        <div className="response-actions">
          <CopyButton label="Copy Markdown" text={message.content} className="select-response-button" onFailure={() => {
            setShowMarkdown(true);
            selectMarkdown();
          }}>
            <Copy size={16} aria-hidden="true" />
          </CopyButton>
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

function AssistantMessage({ message, mode }) {
  const blocks = readActivityBlocks(message);
  // Historical replies have no recorded ordering. Keep their original display.
  if (blocks === null) return <>
    <AgentActivity message={message} mode={mode} />
    {(message.content || message.status !== "streaming") && <AssistantResponse message={message} />}
  </>;

  return <section className="response-timeline" aria-label="Response progress">
    <AgentActivity message={message} mode={mode} progressOnly roundLabel="Round" />
    <ol className="response-timeline-blocks">
      {blocks.map((block, index) => {
        const newRound = mode === "agent" && block.turn > 0 && (index === 0 || blocks[index - 1].turn !== block.turn);
        const activity = {
          ...message,
          thinking: block.type === "thinking" ? block.text : "",
          agent_steps: block.type === "tool" ? [block.step] : [],
          subagent_runs: block.type === "subagent" ? [block.run] : [],
          thinking_timing: block.thinking_timing,
          metadata: { ...message.metadata, thinking_duration_ms: block.duration_ms },
        };
        return <li className={`response-timeline-block timeline-${block.type}`} key={block.key} data-activity-type={block.type} data-round={block.turn}>
          {newRound && <div className="timeline-round">Round {block.turn}</div>}
          {block.type === "content"
            ? <AssistantResponse message={{ ...message, content: block.text, status: block.live ? "streaming" : message.status === "streaming" ? "complete" : message.status }} showActions={message.status !== "streaming"} />
            : <AgentActivity message={activity} mode={mode} showProgress={false} />}
        </li>;
      })}
    </ol>
    {!blocks.some((block) => block.type === "content") && message.status !== "streaming" && <p className="message-status">No response text was generated.</p>}
  </section>;
}

function ChatTranscript({
  streaming,
  compacting,
  generatingTitle,
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
            <div className="message-images" aria-label="Attachments">
              {message.attachments.map((attachment) => (
                <a className={isImageAttachment(attachment) ? undefined : "message-text-file"} href={attachment.url} target="_blank" rel="noreferrer" download={isImageAttachment(attachment) ? undefined : attachment.file_name} key={attachment.id || attachment.url}>
                  {isImageAttachment(attachment)
                    ? <img src={attachment.url} alt={`Attached image: ${attachment.file_name}`} loading="lazy" />
                    : <FileText size={28} aria-hidden="true" />}
                  <span>{attachment.file_name}{!isImageAttachment(attachment) && <small>Text file · {formatBytes(attachment.size)}</small>}</span>
                </a>
              ))}
            </div>
          )}
          {message.role === "assistant"
            ? <AssistantMessage message={message} mode={conversationMode} />
            : message.content && <p className="message-content">{message.content}</p>}
          {message.status === "cancelled" && <p className="message-status">Generation stopped</p>}
          {message.status === "error" && <p className="message-status error">Generation failed</p>}
          {message.role === "assistant" && <ResponseMetrics message={message} />}
          {message.role === "user" && (
            <button className="select-response-button delete-turn-button" type="button"
              disabled={streaming || compacting || generatingTitle || message.id === "pending-user"}
              title={generatingTitle ? "Wait for title generation before deleting this turn" : compacting ? "Wait for context compaction before deleting this turn" : streaming ? "Stop generation before deleting this turn" : "Delete this turn and all later turns"}
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

function Composer({ message, setMessage, onTemplates, webSearchEnabled, onChangeWebSearch, webSearchSaving, webSearchError, webSearchProvider, webSearchProviderError, streaming, onStop, onSend, empty, model, models, setModel, modelDisabled, onRefreshModels, conversationMode, setConversationMode, contextWindow, setContextWindow, thinkingMode, setThinkingMode, contextUsedTokens, contextEstimated, compacting, compactDisabled, compactionNotice, onCompact, disabled, supportsImages, supportsTools, supportsThinking, onOpenSettings }) {
  const [attachments, setAttachments] = useState([]);
  const [attachmentError, setAttachmentError] = useState("");
  const [draggingImages, setDraggingImages] = useState(false);
  const inputRef = useRef(null);
  const resizeDragRef = useRef(null);
  const [inputHeight, setInputHeight] = useState(null);
  const fileInputRef = useRef(null);
  const attachmentsRef = useRef([]);
  const attachmentQueueRef = useRef(Promise.resolve());
  const pasteNumberRef = useRef(0);
  const [preparingAttachments, setPreparingAttachments] = useState(0);
  const hasImageAttachments = attachments.some(isImageAttachment);

  function updateAttachments(next) {
    attachmentsRef.current = next;
    setAttachments(next);
  }

  useEffect(() => {
    if (empty) inputRef.current?.focus();
  }, [empty]);

  useEffect(() => {
    if (!supportsImages && hasImageAttachments) {
      setAttachmentError("Choose a vision-capable model before sending these images.");
    } else if (supportsImages) {
      setAttachmentError("");
    }
  }, [supportsImages, hasImageAttachments]);

  function submit() {
    const value = message.trim();
    if (streaming || disabled || preparingAttachments || (!value && attachments.length === 0) || !supportsImages && hasImageAttachments) return;
    const submittedIDs = new Set(attachments.map((attachment) => attachment.id));
    onSend(value, attachments, () => {
      setMessage((current) => current === message ? "" : current);
      updateAttachments(attachmentsRef.current.filter((attachment) => !submittedIDs.has(attachment.id)));
      setAttachmentError("");
    });
  }

  function addFiles(files) {
    const candidates = [...files];
    if (!candidates.length) return;
    setPreparingAttachments((current) => current + 1);
    attachmentQueueRef.current = attachmentQueueRef.current.then(async () => {
      validateAttachmentFiles(candidates, attachmentsRef.current, supportsImages);
      const prepared = await Promise.all(candidates.map(readAttachment));
      updateAttachments([...attachmentsRef.current, ...prepared]);
      setAttachmentError("");
    }).catch((error) => setAttachmentError(error.message))
      .finally(() => setPreparingAttachments((current) => current - 1));
  }

  function onKeyDown(event) {
    if (event.key === "Enter" && !event.shiftKey) {
      event.preventDefault();
      submit();
    }
  }

  function startInputResize(event) {
    if (!event.isPrimary || event.button !== 0) return;
    event.preventDefault();
    event.currentTarget.focus({ preventScroll: true });
    event.currentTarget.setPointerCapture(event.pointerId);
    resizeDragRef.current = { pointerId: event.pointerId, y: event.clientY, height: inputRef.current.getBoundingClientRect().height };
  }

  function moveInputResize(event) {
    const drag = resizeDragRef.current;
    if (!drag || drag.pointerId !== event.pointerId) return;
    setInputHeight(clampInputHeight(drag.height + drag.y - event.clientY, window.innerHeight));
  }

  function finishInputResize(event) {
    if (resizeDragRef.current?.pointerId !== event.pointerId) return;
    resizeDragRef.current = null;
    if (event.currentTarget.hasPointerCapture(event.pointerId)) event.currentTarget.releasePointerCapture(event.pointerId);
  }

  function resizeInputWithKeyboard(event) {
    const directions = { ArrowUp: 24, ArrowDown: -24 };
    if (!(event.key in directions) && event.key !== "Home" && event.key !== "End") return;
    event.preventDefault();
    const height = event.key === "Home" ? 48 : event.key === "End" ? window.innerHeight / 2 : inputRef.current.getBoundingClientRect().height + directions[event.key];
    setInputHeight(clampInputHeight(height, window.innerHeight));
  }

  const contextLimit = contextWindow === "default" ? null : Number(contextWindow);
  const usedPercent = contextLimit ? Math.min(100, contextUsedTokens / contextLimit * 100) : null;
  const displayPercent = usedPercent == null ? null : usedPercent > 0 && usedPercent < 1 ? "<1" : Math.round(usedPercent).toString();
  const contextLabel = contextLimit
    ? `${contextUsedTokens.toLocaleString()} / ${contextLimit.toLocaleString()} tokens (${displayPercent}%)`
    : contextUsedTokens > 0
      ? `${contextUsedTokens.toLocaleString()} tokens · ${conversationMode === "agent" ? "managed limit" : "Ollama limit"}`
      : `${conversationMode === "agent" ? "Managed" : "Ollama"} default · usage available after model turn`;

  const messageField = (
    <textarea
      ref={inputRef}
      id="message-input"
      style={inputHeight == null ? undefined : { height: `${inputHeight}px` }}
      value={message}
      rows={empty ? "6" : "1"}
      onChange={(event) => setMessage(event.target.value)}
      onPaste={(event) => {
        const images = [...event.clipboardData.files].filter((file) => file.type.startsWith("image/"));
        if (images.length) {
          event.preventDefault();
          addFiles(images);
          return;
        }
        const file = fileFromLargePaste(event.clipboardData.getData("text/plain"), pasteNumberRef.current + 1);
        if (file) {
          event.preventDefault();
          pasteNumberRef.current += 1;
          addFiles([file]);
        }
      }}
      onKeyDown={onKeyDown}
      placeholder={empty ? "Ask or assign a task…" : model ? `Message ${model}` : "Choose a model"}
      aria-label="Message"
    />
  );

  const attachButton = (
    <IconButton
      label="Attach files"
      className="attach-button"
      onClick={() => fileInputRef.current?.click()}
    >
      <Paperclip size={22} />
    </IconButton>
  );

  const composerTools = (
    <div className="composer-tools">
      {attachButton}
      <IconButton label="Choose template" className="attach-button" onClick={(event) => onTemplates(event.currentTarget)}><FileText size={22} /></IconButton>
      <ConversationWebSearch mode={conversationMode} enabled={webSearchEnabled} onChange={onChangeWebSearch} disabled={modelDisabled} saving={webSearchSaving} provider={webSearchProvider} providerError={webSearchProviderError} />
    </div>
  );

  const sendButton = streaming ? (
    <button className="stop-button" aria-label="Stop generation" onClick={onStop}>
      <Stop size={16} weight="fill" /><span>Stop</span>
    </button>
  ) : (
    <button className="send-button" aria-label="Send message" onClick={submit} disabled={preparingAttachments > 0 || (!message.trim() && attachments.length === 0) || disabled || (!supportsImages && hasImageAttachments)}>
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
          addFiles(event.dataTransfer.files);
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
          <div className="attachment-previews" aria-label="Attachments ready to send">
            {attachments.map((attachment) => (
              <div className={`attachment-preview ${isImageAttachment(attachment) ? "" : "text-file-preview"}`} key={attachment.id}>
                {isImageAttachment(attachment)
                  ? <img src={attachment.url} alt={`Preview of ${attachment.name}`} />
                  : <a className="text-file-tile" href={attachment.url} download={attachment.name} aria-label={`Download ${attachment.name}`}><FileText size={26} aria-hidden="true" /><span>TXT · {formatBytes(attachment.size)}</span></a>}
                <button
                  type="button"
                  aria-label={`Remove ${attachment.name}`}
                  onClick={() => updateAttachments(attachmentsRef.current.filter((item) => item.id !== attachment.id))}
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
          accept={supportsImages ? ".txt,text/plain,image/jpeg,image/png,image/webp" : ".txt,text/plain"}
          multiple
          aria-hidden="true"
          tabIndex="-1"
          onChange={(event) => {
            addFiles(event.target.files);
            event.target.value = "";
          }}
        />
        <button
          type="button"
          className="input-resize-handle"
          aria-label="Resize message input"
          aria-controls="message-input"
          title="Drag up to enlarge or down to shrink. Use Up/Down arrows, Home, or End when focused."
          onPointerDown={startInputResize}
          onPointerMove={moveInputResize}
          onPointerUp={finishInputResize}
          onPointerCancel={finishInputResize}
          onLostPointerCapture={() => { resizeDragRef.current = null; }}
          onKeyDown={resizeInputWithKeyboard}
        />
        {empty ? (
          <>
            <div className="launch-prompt-row">{messageField}</div>
            <div className="launch-dock-footer">
              {composerTools}
              <label className="launch-runtime-control">
                <span>Context</span>
                <select aria-label="Context window" value={contextWindow} onChange={(event) => setContextWindow(event.target.value)}>
                  {!CONTEXT_WINDOW_VALUES.includes(contextWindow) && <option value={contextWindow}>{Number(contextWindow).toLocaleString()} tokens</option>}
                  {contextWindows.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}
                </select>
                <CaretDown size={16} aria-hidden="true" />
              </label>
              <label className="launch-runtime-control">
                <span>Thinking</span>
                <select aria-label="Thinking" value={thinkingMode} disabled={!supportsThinking && thinkingMode === "default"} onChange={(event) => setThinkingMode(event.target.value)}>
                  {supportsThinking
                    ? thinkingModes.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)
                    : <><option value="default">{thinkingMode === "default" ? "Not supported" : "Ollama default (no override)"}</option>{thinkingMode !== "default" && <option value={thinkingMode} disabled>{thinkingMode} — not supported</option>}</>}
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
            <div className="composer-row">{messageField}<div className="composer-actions">{composerTools}{sendButton}</div></div>
            <div className="context-row">
              <span>{contextEstimated ? "Estimated context" : "Context"}&nbsp;&nbsp;{contextLabel}</span>
              <div
                className={`context-track ${usedPercent == null ? "unknown-limit" : ""}`}
                aria-label={`${contextEstimated ? "Estimated " : ""}${contextLimit ? `${contextUsedTokens} of ${contextLimit} context tokens used` : `${contextUsedTokens} context tokens used; limit inherited from Ollama`}`}
                role={contextLimit ? "progressbar" : undefined}
                aria-valuenow={usedPercent ?? undefined}
                aria-valuemin={contextLimit ? "0" : undefined}
                aria-valuemax={contextLimit ? "100" : undefined}
              >
                {usedPercent != null && <span style={{ width: `${usedPercent}%` }} />}
              </div>
              <button className={`compact-context-button ${compacting ? "working" : ""}`} type="button" onClick={onCompact} disabled={compactDisabled} aria-label="Compact context" title="Summarize older messages for future replies; recent messages and full saved history are kept.">
                {compacting ? <CircleNotch size={16} aria-hidden="true" /> : <ArrowsIn size={16} aria-hidden="true" />}
                {compacting ? "Compacting…" : "Compact"}
              </button>
            </div>
          </>
        )}
        {webSearchError && <p className="context-feedback error" role="alert">{webSearchError}</p>}
        {compactionNotice && <p className={`context-feedback ${compactionNotice.error ? "error" : ""}`} role={compactionNotice.error ? "alert" : "status"}>{compactionNotice.message}</p>}
      </div>
    </div>
  );
}

export function App() {
  const [draftMessage, setDraftMessage] = useState("");
  const [templatesDialog, setTemplatesDialog] = useState(null);
  const templatesTriggerRef = useRef(null);
  const templateDraftRef = useRef(false);
  const [activeId, setActiveId] = useState(null);
  const [activeTitle, setActiveTitle] = useState("");
  const [titlePollIDs, setTitlePollIDs] = useState([]);
  const titleVersionRef = useRef(0);
  const recentTitlesRef = useRef(new Map());
  const [titleRequests, setTitleRequests] = useState({});
  const [titleNotices, setTitleNotices] = useState({});
  const titleRequestRef = useRef(new Map());
  const activeIdRef = useRef(activeId);
  activeIdRef.current = activeId;
  const [model, setModel] = useState("");
  const modelRef = useRef(model);
  modelRef.current = model;
  const installedModelsRef = useRef(new Map());
  const [modelPreferences] = useState(() => createModelPreferences());
  const [changingModel, setChangingModel] = useState(false);
  const [modelError, setModelError] = useState("");
  const [modelNotice, setModelNotice] = useState("");
  const [modelsLoaded, setModelsLoaded] = useState(false);
  const [loadingConversation, setLoadingConversation] = useState(false);
  const conversationViewRef = useRef(0);
  const modelChangeRef = useRef(null);
  const webSearchChangeRef = useRef(null);
  const [webSearchEnabled, setWebSearchEnabled] = useState(true);
  const [webSearchSaving, setWebSearchSaving] = useState(false);
  const [webSearchError, setWebSearchError] = useState("");
  const modelListRequestRef = useRef(0);
  const [models, setModels] = useState([]);
  const [visionModels, setVisionModels] = useState(() => new Set());
  const [toolModels, setToolModels] = useState(() => new Set());
  const [thinkingModels, setThinkingModels] = useState(() => new Set());
  const [conversationItems, setConversationItems] = useState([]);
  const [query, setQuery] = useState("");
  const queryRef = useRef(query);
  queryRef.current = query;
  const [messages, setMessages] = useState([]);
  const [scrollRequest, setScrollRequest] = useState(0);
  const [connected, setConnected] = useState(false);
  const [chatError, setChatError] = useState("");
  const [settingsOpen, setSettingsOpen] = useState(false);
  const workspaceTriggerRef = useRef(null);
  const [workspaceOpen, setWorkspaceOpen] = useState(false);
  const [workspacePath, setWorkspacePath] = useState("");
  const workspaceReturnRef = useRef(null);
  const [systemSettingsOpen, setSystemSettingsOpen] = useState(false);
  const [runtimeSettings, setRuntimeSettings] = useState(null);
  const [runtimeSettingsLoading, setRuntimeSettingsLoading] = useState(false);
  const [runtimeSettingsError, setRuntimeSettingsError] = useState("");
  const [settingsRequestGuard] = useState(createSettingsRequestGuard);
  const [sidebarOpen, setSidebarOpen] = useState(false);
  const [menuOpen, setMenuOpen] = useState(false);
  const forkRequestRef = useRef(null);
  const [forkingId, setForkingId] = useState(null);
  const [forkNotice, setForkNotice] = useState(null);
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
  const [modelSettings, setModelSettings] = useState(() => ({ ...DEFAULT_MODEL_SETTINGS }));
  const { conversationMode, systemPrompt, contextWindow, thinkingMode, temperatureOverride, temperature } = modelSettings;
  const [contextCheckpoint, setContextCheckpoint] = useState(null);
  const [compacting, setCompacting] = useState(false);
  const [compactionNotice, setCompactionNotice] = useState(null);
  const compactionRef = useRef(null);
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
  const followProgressRef = useRef(true);
  const currentContextUsage = useMemo(() => contextUsage(messages, contextWindow, contextCheckpoint), [messages, contextWindow, contextCheckpoint]);
  const activeTitleGenerating = !!titleRequests[activeId];

  function setConversationMode(value) {
    setModelSettings((current) => ({ ...current, conversationMode: value }));
  }

  function setThinkingMode(value) {
    setModelSettings((current) => ({ ...current, thinkingMode: value }));
  }

  function restoreModelPreferences(name) {
    setModelSettings(modelPreferences.get(name, installedModelsRef.current.get(name)?.capabilities));
  }

  function updateChosenModelSettings(patch) {
    if (modelLoaded) return;
    const next = normalizeModelSettings({ ...modelSettings, ...patch });
    // A template/API may supply a context size outside the quick-pick list.
    if (!Object.hasOwn(patch, "contextWindow")) next.contextWindow = modelSettings.contextWindow;
    setModelSettings(next);
    // Only explicit edits save defaults; loading a saved conversation never does.
    if (installedModelsRef.current.has(model)) modelPreferences.update(model, patch);
  }

  function resetModelDefaults() {
    if (modelLoaded) return;
    modelPreferences.clear(model);
    setModelSettings(modelSettingsForCapabilities(DEFAULT_MODEL_SETTINGS, installedModelsRef.current.get(model)?.capabilities));
  }

  useEffect(() => () => {
    for (const controller of titleRequestRef.current.values()) controller.abort();
  }, []);

  useLayoutEffect(() => {
    followProgressRef.current = true;
    if (activeId) scrollToEnd(transcriptRef.current);
  }, [activeId, scrollRequest]);

  useLayoutEffect(() => {
    const latest = messages.at(-1);
    if (followProgressRef.current && (latest?.activity?.version === 1 || latest?.metadata?.activity?.version === 1)) {
      scrollToEnd(transcriptRef.current);
    }
  }, [messages]);

  async function refreshConversations(search = query, signal) {
    const refreshed = await listConversations(search, signal);
    if (signal?.aborted || search !== queryRef.current) return;
    for (const item of refreshed) {
      if (!item.auto_title_pending) recentTitlesRef.current.set(item.id, newestConversationTitle(item, recentTitlesRef.current.get(item.id)));
    }
    const items = applyKnownConversationTitles(refreshed, recentTitlesRef.current);
    setConversationItems(items);
    const pending = items.filter((item) => item.auto_title_pending && item.message_count >= 2).map((item) => item.id);
    if (pending.length) setTitlePollIDs((current) => [...new Set([...current, ...pending])]);
  }

  const pendingTitleIDs = [...titlePollIDs].sort().join(",");

  useEffect(() => {
    if (!pendingTitleIDs) return;
    const controller = new AbortController();
    const pending = pendingTitleIDs.split(",").map(Number);
    const titleVersion = titleVersionRef.current;
    let timer;
    async function refreshTitles() {
      try {
        const items = await listConversations("", controller.signal);
        if (!controller.signal.aborted) {
          for (const item of items) {
            if (pending.includes(item.id) && !item.auto_title_pending) recentTitlesRef.current.set(item.id, newestConversationTitle(item, recentTitlesRef.current.get(item.id)));
          }
          setConversationItems((current) => mergeConversationTitles(current, items));
          setTitlePollIDs((current) => current.filter((id) => items.some((item) => item.id === id && item.auto_title_pending)));
          const active = items.find((item) => item.id === activeId);
          if (titleVersion === titleVersionRef.current && pending.includes(activeId) && active && !active.auto_title_pending) setActiveTitle(active.title);
        }
      } catch {
        // A title refresh failure must not interrupt the conversation.
      }
      // Watch until the server resolves the pending titles. Slow model calls
      // and first answers must not exhaust an arbitrary polling allowance.
      if (!controller.signal.aborted) timer = window.setTimeout(refreshTitles, 1000);
    }
    timer = window.setTimeout(refreshTitles, 1000);
    return () => { controller.abort(); window.clearTimeout(timer); };
  }, [pendingTitleIDs, activeId]);

  useEffect(() => {
    const active = conversationItems.find((item) => item.id === activeId);
    if (active) setActiveTitle(active.title);
  }, [activeId, conversationItems]);

  useEffect(() => {
    document.title = activeId && activeTitle ? `${activeTitle} · Ollama WebUI` : "Ollama WebUI";
  }, [activeId, activeTitle]);

  async function refreshModels(signal) {
    const requestId = ++modelListRequestRef.current;
    const items = await listModels(signal);
    if (signal?.aborted || requestId !== modelListRequestRef.current) return;
    const names = items.map((item) => item.name);
    const toolNames = items.filter((item) => item.capabilities?.includes("tools")).map((item) => item.name);
    installedModelsRef.current = new Map(items.map((item) => [item.name, item]));
    modelPreferences.prune(names);
    setModels(names);
    setVisionModels(new Set(items.filter((item) => item.capabilities?.includes("vision")).map((item) => item.name)));
    setToolModels(new Set(toolNames));
    setThinkingModels(new Set(items.filter((item) => item.capabilities?.includes("thinking")).map((item) => item.name)));
    // Ordinary refreshes keep the draft. A missing model falls back only in new-session setup.
    if (!templateDraftRef.current && !activeIdRef.current && (!modelRef.current || !names.includes(modelRef.current))) {
      const nextModel = modelPreferences.getSelectedModel() || toolNames[0] || names[0] || "";
      setModel(nextModel);
      restoreModelPreferences(nextModel);
    }
    setModelsLoaded(true);
    setConnected(true);
  }

  function refreshModelChoices() {
    refreshModels().catch((error) => setModelError(`Could not refresh models: ${error.message}`));
  }

  async function changeSessionWebSearch(enabled) {
    if (forkRequestRef.current || conversationMode !== "agent" || streaming || titleRequestRef.current.has(activeId) || compactionRef.current || modelChangeRef.current || webSearchChangeRef.current || loadingConversation || deletingTurns || turnDeleteTarget) return;
    setWebSearchError("");
    if (!activeId) {
      setWebSearchEnabled(enabled);
      return;
    }
    const view = conversationViewRef.current;
    const pendingChange = changeConversationWebSearch(activeId, enabled);
    webSearchChangeRef.current = pendingChange;
    setWebSearchSaving(true);
    try {
      const updated = await pendingChange;
      setConversationItems((items) => items.map((item) => item.id === updated.id ? { ...item, web_search_enabled: updated.web_search_enabled } : item));
      // Keep the previous selection until the server confirms it. Failed saves
      // need no rollback, and a switched session keeps its own choice.
      if (view === conversationViewRef.current) setWebSearchEnabled(conversationWebSearchEnabled(updated));
    } catch (error) {
      if (view === conversationViewRef.current) setWebSearchError(error.message);
    } finally {
      if (webSearchChangeRef.current === pendingChange) {
        webSearchChangeRef.current = null;
        setWebSearchSaving(false);
      }
    }
  }

  async function changeModel(nextModel) {
    if (forkRequestRef.current || nextModel === model || streaming || titleRequestRef.current.has(activeId) || compactionRef.current || modelChangeRef.current || webSearchChangeRef.current || loadingConversation || deletingTurns || turnDeleteTarget) return;
    setModelError("");
    setModelNotice("");
    if (!activeId) {
      modelPreferences.select(nextModel);
      setModel(nextModel);
      if (!templateDraftRef.current) restoreModelPreferences(nextModel);
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
      modelPreferences.select(updated.model);
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
    if (!templateDraftRef.current && !activeId && !modelLoaded && model && !thinkingModels.has(model) && thinkingMode !== "default") {
      setThinkingMode("default");
    }
  }, [activeId, model, modelLoaded, thinkingMode, thinkingModels]);

  useEffect(() => {
    if (!templateDraftRef.current && !activeId && !modelLoaded && model && !toolModels.has(model) && conversationMode === "agent") {
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

  // Hash links also work when opened in a new tab or pasted into the address bar.
  useEffect(() => {
    const root = runtimeSettings?.agent?.workspace;
    if (!root) return undefined;
    function openWorkspaceHash() {
      if (!window.location.hash.startsWith("#workspace=")) return;
      let target;
      try { target = decodeURIComponent(window.location.hash.slice(11)); } catch { return; }
      if (target.startsWith("/")) return;
      const path = resolveWorkspacePath(root.replace(/\/+$/, "") + "/" + target, root, true);
      if (path === null) return;
      workspaceReturnRef.current = workspaceTriggerRef.current;
      setWorkspacePath(path);
      setWorkspaceOpen(true);
    }
    openWorkspaceHash();
    window.addEventListener("hashchange", openWorkspaceHash);
    return () => window.removeEventListener("hashchange", openWorkspaceHash);
  }, [runtimeSettings?.agent?.workspace]);

  // The composer needs provider status before any settings panel is opened.
  useEffect(() => {
    const controller = new AbortController();
    refreshRuntimeSettings(controller.signal);
    return () => controller.abort();
  }, []);

  useEffect(() => {
    if (!systemSettingsOpen) return undefined;
    const controller = new AbortController();
    refreshRuntimeSettings(controller.signal);
    return () => controller.abort();
  }, [systemSettingsOpen]);

  useEffect(() => {
    function onKeyDown(event) {
      if (event.key !== "Escape") return;
      if (templatesDialog || workspaceOpen || renameTarget || deleteTarget || aboutOpen || systemSettingsOpen || clearAllOpen) {
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
  }, [templatesDialog, workspaceOpen, renameTarget, deleteTarget, aboutOpen, systemSettingsOpen, clearAllOpen, settingsOpen, sidebarOpen, viewportWidth]);

  useEffect(() => {
    if (templatesDialog || workspaceOpen) return undefined;
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
  }, [templatesDialog, workspaceOpen, settingsOpen, sidebarOpen, viewportWidth]);

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
    refreshRuntimeSettings();
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

  async function refreshRuntimeSettings(signal) {
    const read = settingsRequestGuard.read();
    setRuntimeSettingsLoading(true);
    setRuntimeSettingsError("");
    try {
      const result = await getSettings(signal);
      if (!signal?.aborted && settingsRequestGuard.current(read)) setRuntimeSettings(result);
    } catch (error) {
      if (error.name !== "AbortError" && settingsRequestGuard.current(read)) setRuntimeSettingsError(error.message);
    } finally {
      if (!signal?.aborted && settingsRequestGuard.latest(read)) setRuntimeSettingsLoading(false);
    }
  }

  async function saveSearchSettings(input, signal) {
    const write = settingsRequestGuard.startWrite();
    try {
      const result = await saveWebSearchSettings(input, signal);
      if (!signal?.aborted && settingsRequestGuard.currentWrite(write)) setRuntimeSettings((current) => ({ ...current, web_search: result }));
      return result;
    } finally {
      settingsRequestGuard.finishWrite();
      // Re-read after the mutation so overlapping refreshes cannot leave the
      // public status stale, including when the dialog closed during Save.
      refreshRuntimeSettings();
    }
  }

  function openClearAll(trigger) {
    if (streaming || titleRequestRef.current.size || compactionRef.current || webSearchChangeRef.current) return;
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
    templateDraftRef.current = false;
    setTemplatesDialog(null);
    const view = ++conversationViewRef.current;
    setLoadingConversation(true);
    setModelError("");
    setModelNotice("");
    generationRef.current?.abort();
    compactionRef.current?.abort();
    setCompactionNotice(null);
    detailRequestRef.current?.abort();
    const controller = new AbortController();
    detailRequestRef.current = controller;
    setChatError("");
    setSidebarOpen(false);
    try {
      // Reopening a session while its model is saving must read the saved value.
      await modelChangeRef.current?.catch(() => {});
      await webSearchChangeRef.current?.catch(() => {});
      if (view !== conversationViewRef.current) return;
      const payload = await getConversation(id, controller.signal);
      if (view !== conversationViewRef.current) return;
      setActiveId(payload.conversation.id);
      const recentTitle = recentTitlesRef.current.get(id);
      setActiveTitle(newestConversationTitle(payload.conversation, recentTitle).title);
      if (payload.conversation.auto_title_pending && payload.conversation.message_count >= 2) {
        setTitlePollIDs((current) => [...new Set([...current, id])]);
      }
      setModel(payload.conversation.model);
      setWebSearchEnabled(conversationWebSearchEnabled(payload.conversation));
      setWebSearchError("");
      setModelSettings({
        conversationMode: payload.conversation.mode || "agent",
        systemPrompt: payload.conversation.system_prompt || "",
        contextWindow: payload.conversation.context_window == null ? "default" : String(payload.conversation.context_window),
        thinkingMode: thinkingModeFromConversation(payload.conversation),
        temperatureOverride: payload.conversation.temperature != null,
        temperature: payload.conversation.temperature ?? 0.7,
      });
      setModelLoaded(payload.conversation.message_count > 0);
      setMessages(payload.messages);
      setContextCheckpoint(payload.context_checkpoint || null);
    } catch (error) {
      if (error.name !== "AbortError") setChatError(error.message);
    } finally {
      if (view === conversationViewRef.current) setLoadingConversation(false);
    }
  }

  function resetConversation(closeNavigation = true) {
    templateDraftRef.current = false;
    setTemplatesDialog(null);
    ++conversationViewRef.current;
    detailRequestRef.current?.abort();
    setLoadingConversation(false);
    setModelError("");
    setModelNotice("");
    generationRef.current?.abort();
    compactionRef.current?.abort();
    setContextCheckpoint(null);
    setCompactionNotice(null);
    setActiveId(null);
    setActiveTitle("");
    setMessages([]);
    setStreaming(false);
    setModelLoaded(false);
    const nextModel = modelPreferences.getSelectedModel() || model;
    setModel(nextModel);
    restoreModelPreferences(nextModel);
    setWebSearchEnabled(true);
    setWebSearchError("");
    setChatError("");
    if (closeNavigation) setSidebarOpen(false);
  }

  function newChat() {
    resetConversation(true);
    refreshModelChoices();
  }

  function openTemplates(trigger, fromConversation = false) {
    templatesTriggerRef.current = trigger;
    setMenuOpen(false);
    const sourceMessages = messages.filter((message) => message.role === "user" && message.status === "complete");
    setTemplatesDialog({
      view: conversationViewRef.current,
      sourceMessages: fromConversation ? sourceMessages : [],
      initialTemplate: fromConversation ? {
        name: Array.from(activeTitle || "").slice(0, 120).join(""),
        description: "",
        message: sourceMessages[0]?.content || draftMessage,
        system_prompt: systemPrompt,
        settings: templateSettings(model, modelSettings, webSearchEnabled),
      } : null,
    });
  }

  function closeTemplates(focusComposer = false) {
    setTemplatesDialog(null);
    window.requestAnimationFrame(() => {
      const trigger = templatesTriggerRef.current;
      if (focusComposer || !trigger?.isConnected) document.getElementById("message-input")?.focus();
      else trigger.focus();
    });
  }

  function applyTemplate(template, target, insertion) {
    if (templatesDialog?.view !== conversationViewRef.current) return "This conversation has changed. Reopen Templates to continue.";
    if (modelDisabled || deleting || clearingAll) return "Wait for the current operation to finish before applying a template.";
    if (target === "current" && !template.message?.trim()) return "This template has no message to insert. Use it in a new chat instead.";
    if (target === "new") {
      const next = templateDraft(template, { model, modelSettings, webSearchEnabled });
      resetConversation(true);
      templateDraftRef.current = true;
      setModel(next.model);
      setModelSettings(next.modelSettings);
      setWebSearchEnabled(next.webSearchEnabled);
      setModelNotice(`Template “${template.name}” loaded. Review your message and settings, then send.`);
    }
    setDraftMessage((current) => insertTemplateMessage(current, template.message, insertion));
    closeTemplates(true);
    return "";
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

  async function forkSession(conversation) {
    if (forkRequestRef.current || streaming || compactionRef.current || modelChangeRef.current || webSearchChangeRef.current || loadingConversation || deletingTurns || turnDeleteTarget || deleting || clearingAll || titleRequestRef.current.has(conversation.id)) return;
    const view = conversationViewRef.current;
    const pending = forkConversation(conversation.id);
    forkRequestRef.current = pending;
    setForkingId(conversation.id);
    setForkNotice(null);
    try {
      const created = await pending;
      if (view === conversationViewRef.current) {
        setQuery("");
        await chooseConversation(created.id);
      } else {
        setForkNotice({ message: "Fork created: " + created.title, id: created.id });
      }
      await refreshConversations(queryRef.current).catch(() => {});
    } catch (error) {
      setForkNotice({ message: "Could not fork conversation: " + error.message, error: true });
    } finally {
      forkRequestRef.current = null;
      setForkingId(null);
    }
  }

  function openRename(conversation, trigger) {
    if (forkRequestRef.current || titleRequestRef.current.has(conversation.id)) return;
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
      recentTitlesRef.current.set(updated.id, updated);
      if (renameTarget.id === activeId) {
        titleVersionRef.current++;
        setActiveTitle(updated.title);
      }
      setTitlePollIDs((current) => current.filter((id) => id !== renameTarget.id));
      setConversationItems((items) => items.map((item) => item.id === renameTarget.id ? { ...item, title: updated.title, auto_title_pending: false, updated_at: updated.updated_at } : item));
      setRenameTarget(null);
      await refreshConversations(query);
    } catch (error) {
      setRenameError(error.message);
    } finally {
      setRenaming(false);
    }
  }

  async function generateLatestSessionTitle(conversation) {
    if (forkRequestRef.current || titleRequestRef.current.has(conversation.id) || (conversation.id === activeId && (streaming || compactionRef.current || modelChangeRef.current || webSearchChangeRef.current || deletingTurns))) return;
    const controller = new AbortController();
    titleRequestRef.current.set(conversation.id, controller);
    setTitleRequests((current) => ({ ...current, [conversation.id]: true }));
    setTitleNotices((current) => ({ ...current, [conversation.id]: null }));
    // An explicit naming request supersedes automatic first-turn naming.
    if (activeIdRef.current === conversation.id) titleVersionRef.current++;
    setTitlePollIDs((current) => current.filter((id) => id !== conversation.id));
    try {
      const updated = await generateConversationTitle(conversation.id, controller.signal);
      recentTitlesRef.current.set(updated.id, updated);
      if (activeIdRef.current === conversation.id) setActiveTitle(updated.title);
      setConversationItems((items) => items.map((item) => item.id === updated.id ? { ...item, title: updated.title, auto_title_pending: false, updated_at: updated.updated_at } : item));
      setTitleNotices((current) => ({ ...current, [conversation.id]: { message: "Title updated.", error: false } }));
      refreshConversations(queryRef.current).catch(() => {});
    } catch (error) {
      if (error.name !== "AbortError") setTitleNotices((current) => ({ ...current, [conversation.id]: { message: error.message, error: true } }));
    } finally {
      titleRequestRef.current.delete(conversation.id);
      setTitleRequests((current) => { const next = { ...current }; delete next[conversation.id]; return next; });
    }
  }

  function openDelete(conversation, trigger) {
    if (forkRequestRef.current || titleRequestRef.current.has(conversation.id) || (streaming || webSearchChangeRef.current) && activeId === conversation.id) return;
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
    if (titleRequestRef.current.has(activeId)) return;
    if (streaming || compactionRef.current || webSearchChangeRef.current) return;
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
      setContextCheckpoint(payload.context_checkpoint || null);
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

  async function manuallyCompactContext() {
    if (forkRequestRef.current || !activeId || streaming || titleRequestRef.current.has(activeId) || compactionRef.current || modelChangeRef.current || webSearchChangeRef.current || loadingConversation || deletingTurns || turnDeleteTarget) return;
    const view = conversationViewRef.current;
    const controller = new AbortController();
    compactionRef.current = controller;
    setCompacting(true);
    setCompactionNotice(null);
    try {
      const result = await compactConversationContext(activeId, controller.signal);
      if (view !== conversationViewRef.current) return;
      setContextCheckpoint(result.context_checkpoint || null);
      setCompactionNotice({ message: result.compacted
        ? `Context compacted. Estimated prompt reduced from ${result.before_tokens.toLocaleString()} to ${result.estimated_tokens.toLocaleString()} tokens.`
        : result.reason === "not_reduced"
          ? "The summary did not reduce context. Current context kept."
          : "No older messages to compact. Recent messages are kept." });
    } catch (error) {
      if (error.name !== "AbortError" && view === conversationViewRef.current) {
        setCompactionNotice({ message: error.message, error: true });
      }
    } finally {
      if (compactionRef.current === controller) {
        compactionRef.current = null;
        setCompacting(false);
      }
    }
  }

  async function sendMessage(value, attachments = [], onAccepted = () => {}) {
    if (forkRequestRef.current || !model || !models.includes(model) || streaming || titleRequestRef.current.has(activeId) || compactionRef.current || modelChangeRef.current || webSearchChangeRef.current || loadingConversation || deletingTurns || turnDeleteTarget) return;
    if (!activeId && templateDraftRef.current && (conversationMode === "agent" && !toolModels.has(model) || thinkingMode !== "default" && !thinkingModels.has(model))) return;
    setChatError("");
    setModelError("");
    setModelNotice("");
    const controller = new AbortController();
    generationRef.current = controller;
    setStreaming(true);
    setModelLoaded(true);
    let conversationId = activeId;
    let accepted = false;
    try {
      if (!conversationId) {
        const conversation = await createConversation({
          title: value.trim().replace(/\s+/g, " ").slice(0, 60) || `File: ${attachments[0]?.name || "conversation"}`,
          model,
          mode: conversationMode,
          web_search_enabled: webSearchEnabled,
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
        attachments: attachments.map((attachment) => ({ id: attachment.id, file_name: attachment.name, url: attachment.url, media_type: attachment.media_type, size: attachment.size })),
      };
      const pendingAssistant = { id: "pending-assistant", role: "assistant", content: "", thinking: "", status: "streaming", agent_phase: "starting", agent_turn: 1 };
      setMessages((current) => [...current, pendingUser, pendingAssistant]);
      setScrollRequest((current) => current + 1);

      await streamMessage(conversationId, value, {
        signal: controller.signal,
        ...splitAttachments(attachments),
        onEvent(event) {
          // Capture order before the aggregate reducers append text/update tools.
          // Only a new server's activity.started event enables the timeline.
          if (event.type !== "started") setMessages((current) => current.map((message, index) =>
            index === current.length - 1 ? recordActivityEvent(message, event) : message));
          if (event.type === "started") {
            accepted = true;
            onAccepted();
            setCompactionNotice(null);
            setMessages((current) => current.map((message) => {
              if (message.id === "pending-user") return event.user_message;
              if (message.id === "pending-assistant") return { ...event.assistant_message, agent_phase: "thinking", agent_turn: 1 };
              return message;
            }));
          } else if (event.type === "turn.started") {
            setMessages((current) => current.map((message, index) =>
              index === current.length - 1 ? { ...message, agent_phase: "thinking", agent_turn: event.turn } : message));
          } else if (event.type === "turn.done") {
            setMessages((current) => current.map((message, index) =>
              index === current.length - 1 ? finishThinkingTiming(message, event.metadata) : message));
          } else if (event.type === "thinking.timing") {
            setMessages((current) => current.map((message, index) =>
              index === current.length - 1 ? applyThinkingTiming(message, event.metadata) : message));
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
                ? { ...message, agent_steps: [...(message.agent_steps || []), event.step], agent_phase: event.step.tool_name === "web_search" ? "web_search" : "shell", agent_turn: event.turn }
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
            if (event.title_pending) setTitlePollIDs((current) => [...new Set([...current, conversationId])]);
            setMessages((current) => current.map((message, index) =>
              index === current.length - 1 ? { ...finishThinkingTiming(message, event.metadata), status: "complete", agent_phase: "finished", agent_turn: event.metadata?.agent_turns || message.agent_turn } : message));
          } else if (event.type === "error") {
            throw new Error(event.message);
          }
        },
      });
    } catch (error) {
      const cancelled = error.name === "AbortError";
      setMessages((current) => !accepted
        ? current.filter((message) => message.id !== "pending-user" && message.id !== "pending-assistant")
        : current.map((message, index) =>
        index === current.length - 1 && message.role === "assistant"
          ? {
              ...finishThinkingTiming(finalizeActivity(message)),
              status: cancelled ? "cancelled" : "error",
              agent_phase: cancelled ? "stopped" : "error",
              agent_steps: (message.agent_steps || []).map((step) =>
                step.status === "running" ? { ...step, status: cancelled ? "cancelled" : "error" } : step),
              subagent_runs: cancelled ? cancelRunningSubagents(message.subagent_runs) : message.subagent_runs,
            }
          : message));
      if (!accepted) setModelLoaded(modelLoaded);
      if (!cancelled) setChatError(error.message);
      if (cancelled && conversationId && generationRef.current === controller) {
        try {
          const payload = await getConversation(conversationId);
          const refreshedMessages = payload.messages || [];
          // Cancellation may still be saving; keep the local stopped response and its measured usage.
          if (generationRef.current === controller && refreshedMessages.at(-1)?.status !== "streaming") {
            setMessages(refreshedMessages);
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
  const templateModelError = !activeId && templateDraftRef.current && !modelUnavailable && (
    conversationMode === "agent" && !toolModels.has(model)
      ? "This template uses Agent mode. Choose a tool-capable model or switch to Chat."
      : thinkingMode !== "default" && !thinkingModels.has(model)
        ? "This template requests thinking. Choose a thinking-capable model or select Ollama default for Thinking."
        : ""
  );
  const modelDisabled = !!forkingId || streaming || activeTitleGenerating || compacting || changingModel || webSearchSaving || loadingConversation || deletingTurns || !!turnDeleteTarget;
  const modelFeedback = changingModel ? "Changing model…" : modelError || (modelUnavailable ? "This model is no longer installed. Choose another model to continue this conversation." : templateModelError || modelNotice);

  return (
    <div className={`app-shell ${settingsOpen ? "settings-is-open" : ""}`}>
      <div className={`sidebar-layer ${sidebarOpen ? "open" : ""}`} aria-hidden={!sidebarVisible} inert={!sidebarVisible ? "" : undefined}>
        <ConversationSidebar
          activeId={activeId}
          conversations={conversationItems}
          query={query}
          streaming={streaming || compacting || webSearchSaving}
          titleRequests={titleRequests}
          titleNotices={titleNotices}
          onQuery={setQuery}
          onSelect={chooseConversation}
          onNew={newChat}
          onTemplates={openTemplates}
          onRename={openRename}
          onGenerateTitle={generateLatestSessionTitle}
          onFork={forkSession}
          forkingId={forkingId}
          forkDisabled={modelDisabled || deleting || clearingAll}
          onDelete={openDelete}
          onSettings={openSystemSettings}
          onAbout={openAbout}
          onClose={closeSidebar}
        />
      </div>

      <main className={`chat-workspace ${!activeId ? "is-new-chat" : ""} ${modelFeedback || forkingId || forkNotice ? "has-model-feedback" : ""}`}>
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
            <button ref={workspaceTriggerRef} type="button" className="workspace-trigger" aria-label="Open workspace" title="Browse workspace files" onClick={(event) => { workspaceReturnRef.current = event.currentTarget; setWorkspacePath(""); setWorkspaceOpen(true); }}><FolderOpen size={21} aria-hidden="true" /><span>Workspace</span></button>
            <div className={`agent-status ${streaming || compacting ? "working" : ""}`} role="status" aria-live="polite">
              {streaming || compacting ? <CircleNotch size={16} /> : <ChatCircle size={16} />}
              <span>{compacting ? "Compacting context" : conversationMode === "agent" ? (streaming ? "Agent working" : "Agent ready") : (streaming ? "Chat responding" : "Chat ready")}</span>
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
                  <button role="menuitem" disabled={modelDisabled || deleting || clearingAll} onClick={() => openTemplates(settingsTriggerRef.current, true)}><FileText size={19} />Save as template</button>
                  <button
                    role="menuitem"
                    disabled={!activeId || !!forkingId || activeTitleGenerating}
                    onClick={() => {
                      setMenuOpen(false);
                      openRename({ id: activeId, title: activeTitle }, settingsTriggerRef.current);
                    }}
                  ><NotePencil size={19} />Rename chat</button>
                  <button
                    role="menuitem"
                    disabled={!activeId || modelDisabled || deleting || clearingAll}
                    onClick={() => {
                      setMenuOpen(false);
                      forkSession({ id: activeId, title: activeTitle });
                      settingsTriggerRef.current?.focus();
                    }}
                  ><GitFork size={19} />Fork conversation</button>
                  <button
                    className="danger-menu-item"
                    role="menuitem"
                    disabled={!activeId || !!forkingId || streaming || compacting || activeTitleGenerating}
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

        {(modelFeedback || forkingId || forkNotice) && (
          <div>
            {modelFeedback && <div id="active-model-feedback" className={`model-feedback ${modelError || modelUnavailable ? "model-feedback-error" : ""}`} role={modelError ? "alert" : "status"}>{modelFeedback}</div>}
            {(forkingId || forkNotice) && (
              <div className={`model-feedback ${forkNotice?.error ? "model-feedback-error" : ""}`} role={forkNotice?.error ? "alert" : "status"}>
                {forkingId ? "Forking conversation…" : forkNotice.message}
                {!forkingId && forkNotice?.id && <button type="button" className="fork-open-button" onClick={() => { chooseConversation(forkNotice.id); setForkNotice(null); }}>Open fork</button>}
              </div>
            )}
          </div>
        )}

        <section className="transcript" aria-label="Conversation" ref={transcriptRef} onScroll={(event) => { followProgressRef.current = isNearTranscriptEnd(event.currentTarget); }}>
          <WorkspaceLinkContext.Provider value={{ root: runtimeSettings?.agent?.workspace, open: (path, trigger) => { workspaceReturnRef.current = trigger; setWorkspacePath(path); setWorkspaceOpen(true); } }}>
          <ChatTranscript
            streaming={streaming}
            compacting={compacting}
            generatingTitle={activeTitleGenerating}
            onDeleteTurn={openTurnDelete}
            empty={!activeId}
            messages={messages}
            conversationMode={conversationMode}
            error={chatError}
          />
          </WorkspaceLinkContext.Provider>
        </section>
        <Composer
          message={draftMessage}
          setMessage={setDraftMessage}
          onTemplates={openTemplates}
          webSearchEnabled={webSearchEnabled}
          onChangeWebSearch={changeSessionWebSearch}
          webSearchSaving={webSearchSaving}
          webSearchError={webSearchError}
          webSearchProvider={runtimeSettings?.web_search}
          webSearchProviderError={runtimeSettingsError}
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
          setConversationMode={(value) => updateChosenModelSettings({ conversationMode: value })}
          contextWindow={contextWindow}
          setContextWindow={(value) => updateChosenModelSettings({ contextWindow: value })}
          thinkingMode={thinkingMode}
          setThinkingMode={(value) => updateChosenModelSettings({ thinkingMode: value })}
          contextUsedTokens={currentContextUsage.usedTokens}
          contextEstimated={!!currentContextUsage.estimated}
          compacting={compacting}
          compactDisabled={!activeId || modelDisabled || modelUnavailable || !messages.length}
          compactionNotice={compactionNotice}
          onCompact={manuallyCompactContext}
          disabled={!!forkingId || !model || !models.includes(model) || !!templateModelError || compacting || activeTitleGenerating || changingModel || webSearchSaving || loadingConversation || deletingTurns || !!turnDeleteTarget || conversationMode === "agent" && !toolModels.has(model)}
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
          setConversationMode={(value) => updateChosenModelSettings({ conversationMode: value })}
          supportsTools={toolModels.has(model)}
          onClose={closeSettings}
          systemPrompt={systemPrompt}
          setSystemPrompt={(value) => updateChosenModelSettings({ systemPrompt: value })}
          contextWindow={contextWindow}
          setContextWindow={(value) => updateChosenModelSettings({ contextWindow: value })}
          thinkingMode={thinkingMode}
          setThinkingMode={(value) => updateChosenModelSettings({ thinkingMode: value })}
          supportsThinking={thinkingModels.has(model)}
          modelLoaded={modelLoaded}
          temperatureOverride={temperatureOverride}
          setTemperatureOverride={(value) => updateChosenModelSettings({ temperatureOverride: value })}
          temperature={temperature}
          setTemperature={(value) => updateChosenModelSettings({ temperature: value })}
          onResetDefaults={resetModelDefaults}
        />
      </div>

      {(sidebarOpen || overlaySettings) && (
        <button
          className="drawer-backdrop"
          aria-label="Close open panel"
          onClick={() => { if (sidebarOpen) closeSidebar(); if (viewportWidth < 1200 && settingsOpen) closeSettings(); }}
        />
      )}

      {workspaceOpen && <WorkspaceBrowser key={workspacePath} initialPath={workspacePath} onClose={() => { setWorkspaceOpen(false); window.requestAnimationFrame(() => (workspaceReturnRef.current?.isConnected ? workspaceReturnRef.current : workspaceTriggerRef.current)?.focus()); }} />}

      {templatesDialog && <TemplatesDialog
        onClose={() => closeTemplates()}
        onApply={applyTemplate}
        initialTemplate={templatesDialog.initialTemplate}
        sourceMessages={templatesDialog.sourceMessages}
        models={models}
        currentSettings={templateSettings(model, modelSettings, webSearchEnabled)}
        hasConversation={!!activeId}
        hasDraft={!!draftMessage}
        applyDisabled={modelDisabled || deleting || clearingAll}
      />}

      <dialog
        className="system-settings-dialog"
        ref={systemSettingsDialogRef}
        aria-labelledby="system-settings-title"
        onCancel={(event) => { event.preventDefault(); closeSystemSettings(); }}
        onClose={restoreSystemSettingsFocus}
      >
        <SystemSettings
          open={systemSettingsOpen}
          connected={connected}
          modelCount={models.length}
          runtime={runtimeSettings}
          loading={runtimeSettingsLoading}
          error={runtimeSettingsError}
          streaming={!!forkingId || streaming || compacting || webSearchSaving || Object.keys(titleRequests).length > 0}
          onRefresh={refreshRuntimeSettings}
          onSaveWebSearch={saveSearchSettings}
          onTestWebSearch={testWebSearchSettings}
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
