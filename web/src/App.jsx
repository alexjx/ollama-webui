import { useEffect, useMemo, useRef, useState } from "react";
import { contextUsage } from "./context-usage";
import { formatDuration, formatRate, responseMetrics } from "./response-metrics";
import { thinkingModeForRequest, thinkingModeFromConversation } from "./thinking-mode";
import {
  createConversation,
  deleteConversation,
  getConversation,
  getHealth,
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
  SidebarSimple,
  SlidersHorizontal,
  Stop,
  TerminalWindow,
  Trash,
  X,
} from "@phosphor-icons/react";

const contextWindows = [
  { value: "default", label: "Ollama default" },
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

function ConversationSidebar({ activeId, conversations, query, streaming, onQuery, onSelect, onNew, onRename, onDelete, onSettings, onClose }) {
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
        <button onClick={onSettings}><Gear size={23} />Settings</button>
        <button><Info size={23} />About</button>
      </div>
    </aside>
  );
}

function ModelSettings({
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
    <aside className="settings-panel" aria-label="Model settings">
      <div className="settings-heading">
        <h2>Model settings</h2>
        <IconButton label="Close model settings" onClick={onClose}>
          <X size={24} />
        </IconButton>
      </div>

      <div className="settings-scroll">
        <section className="settings-section model-section">
          <label htmlFor="settings-model">Model</label>
          <div className="select-wrap wide">
            <select id="settings-model" value={model} disabled={modelLoaded} onChange={(event) => setModel(event.target.value)}>
              {models.map((item) => <option key={item}>{item}</option>)}
            </select>
            <CaretDown size={18} aria-hidden="true" />
          </div>
          <p>Select the model to use for this conversation.</p>
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
            <p>Applied when the first message loads this model. Ollama default sends no context override.</p>
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
          />
          <p>Used when the conversation doesn’t have a custom system prompt.</p>
        </section>

        <section className="settings-section temperature-section">
          <div className="setting-label-row">
            <label htmlFor="temperature">Temperature <Info size={17} aria-label="Temperature help" /></label>
            <label className="override-toggle">
              <input
                type="checkbox"
                checked={temperatureOverride}
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
            disabled={!temperatureOverride}
            onChange={(event) => setTemperature(Number(event.target.value))}
          />
          <div className="range-labels"><span>0</span><span>{temperature.toFixed(1)}</span><span>2</span></div>
          <p>Controls randomness in model responses.</p>
        </section>
      </div>

      <button className="reset-button" onClick={resetDefaults}>
        <ArrowCounterClockwise size={21} />
        Reset to model defaults
      </button>
    </aside>
  );
}

function AgentActivity({ message, mode }) {
  const thinkingRef = useRef(null);
  const steps = message.agent_steps || [];
  const hasActivity = message.status === "streaming" || Boolean(message.thinking) || steps.length > 0;

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
        <div className="agent-steps" aria-label="Shell activity">
          {steps.map((step) => {
            let command = step.input;
            try { command = JSON.parse(step.input).command || step.input; } catch { /* Show the recorded input. */ }
            const statusLabel = step.status === "running" ? "Running" : step.status === "complete" ? `Exited ${step.exit_code ?? 0}` : step.status === "cancelled" ? "Stopped" : "Failed";
            return (
              <details className={`agent-step ${step.status}`} key={step.id} open={step.status === "running"}>
                <summary>
                  <span className="agent-step-icon"><TerminalWindow size={17} /></span>
                  <code>{command}</code>
                  <span className="agent-step-status">{statusLabel}</span>
                </summary>
                {step.output && <pre>{step.output}</pre>}
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

function ChatTranscript({
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
          {(message.content || message.role === "assistant" && message.status !== "streaming") && (
            <p className="message-content">{message.content || (message.status === "streaming" ? "" : "No response was generated.")}
              {message.role === "assistant" && message.status === "streaming" && <span className="stream-cursor" aria-hidden="true" />}
            </p>
          )}
          {message.status === "cancelled" && <p className="message-status">Generation stopped</p>}
          {message.status === "error" && <p className="message-status error">Generation failed</p>}
          {message.role === "assistant" && <ResponseMetrics message={message} />}
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

function Composer({ streaming, onStop, onSend, empty, model, models, setModel, conversationMode, setConversationMode, contextWindow, setContextWindow, thinkingMode, setThinkingMode, contextUsedTokens, disabled, supportsImages, supportsTools, supportsThinking, onOpenSettings }) {
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
      ? `${contextUsedTokens.toLocaleString()} tokens · Ollama default limit`
      : "Ollama default · usage available after response";

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
              <select aria-label="Model" value={model} onChange={(event) => setModel(event.target.value)}>
                {models.length ? models.map((item) => <option key={item}>{item}</option>) : <option value="">No local models found</option>}
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
  const [conversationMode, setConversationMode] = useState("agent");
  const [models, setModels] = useState([]);
  const [visionModels, setVisionModels] = useState(() => new Set());
  const [toolModels, setToolModels] = useState(() => new Set());
  const [thinkingModels, setThinkingModels] = useState(() => new Set());
  const [conversationItems, setConversationItems] = useState([]);
  const [query, setQuery] = useState("");
  const [messages, setMessages] = useState([]);
  const [connected, setConnected] = useState(false);
  const [chatError, setChatError] = useState("");
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [sidebarOpen, setSidebarOpen] = useState(false);
  const [menuOpen, setMenuOpen] = useState(false);
  const [renameTarget, setRenameTarget] = useState(null);
  const [renameTitle, setRenameTitle] = useState("");
  const [renameError, setRenameError] = useState("");
  const [renaming, setRenaming] = useState(false);
  const [deleteTarget, setDeleteTarget] = useState(null);
  const [deleteError, setDeleteError] = useState("");
  const [deleting, setDeleting] = useState(false);
  const [systemPrompt, setSystemPrompt] = useState("");
  const [contextWindow, setContextWindow] = useState("default");
  const [thinkingMode, setThinkingMode] = useState("default");
  const [temperatureOverride, setTemperatureOverride] = useState(false);
  const [temperature, setTemperature] = useState(0.7);
  const [modelLoaded, setModelLoaded] = useState(false);
  const [streaming, setStreaming] = useState(false);
  const [viewportWidth, setViewportWidth] = useState(() => window.innerWidth);
  const settingsTriggerRef = useRef(null);
  const sidebarTriggerRef = useRef(null);
  const generationRef = useRef(null);
  const detailRequestRef = useRef(null);
  const renameDialogRef = useRef(null);
  const renameInputRef = useRef(null);
  const deleteDialogRef = useRef(null);
  const deleteCancelRef = useRef(null);
  const actionReturnRef = useRef(null);
  const currentContextUsage = useMemo(() => contextUsage(messages, contextWindow), [messages, contextWindow]);

  async function refreshConversations(search = query, signal) {
    const items = await listConversations(search, signal);
    setConversationItems(items);
  }

  useEffect(() => {
    const controller = new AbortController();
    Promise.allSettled([
      listModels(controller.signal).then((items) => {
        const names = items.map((item) => item.name);
        const toolNames = items.filter((item) => item.capabilities?.includes("tools")).map((item) => item.name);
        setModels(names);
        setVisionModels(new Set(items.filter((item) => item.capabilities?.includes("vision")).map((item) => item.name)));
        setToolModels(new Set(toolNames));
        setThinkingModels(new Set(items.filter((item) => item.capabilities?.includes("thinking")).map((item) => item.name)));
        setModel((current) => current || toolNames[0] || names[0] || "");
        setConnected(true);
      }),
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
    if (!modelLoaded && model && !thinkingModels.has(model) && thinkingMode !== "default") {
      setThinkingMode("default");
    }
  }, [model, modelLoaded, thinkingMode, thinkingModels]);

  useEffect(() => {
    if (!modelLoaded && model && !toolModels.has(model) && conversationMode === "agent") {
      setConversationMode("chat");
    }
  }, [conversationMode, model, modelLoaded, toolModels]);

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
    function onKeyDown(event) {
      if (event.key !== "Escape") return;
      if (renameTarget || deleteTarget) {
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
  }, [renameTarget, deleteTarget, settingsOpen, sidebarOpen, viewportWidth]);

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
    window.setTimeout(() => settingsTriggerRef.current?.focus(), 0);
  }

  async function chooseConversation(id) {
    generationRef.current?.abort();
    detailRequestRef.current?.abort();
    const controller = new AbortController();
    detailRequestRef.current = controller;
    setChatError("");
    setSidebarOpen(false);
    try {
      const payload = await getConversation(id, controller.signal);
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
    }
  }

  function resetConversation(closeNavigation = true) {
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

  async function sendMessage(value, images = []) {
    if (!model || streaming) return;
    setChatError("");
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
            }
          : message));
      if (!cancelled) setChatError(error.message);
    } finally {
      setStreaming(false);
      generationRef.current = null;
      refreshConversations(query).catch(() => {});
    }
  }

  const overlaySettings = settingsOpen && viewportWidth < 1200;
  const sidebarVisible = viewportWidth > 900 || sidebarOpen;

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
          onSettings={() => { setSettingsOpen(true); closeSidebar(); }}
          onClose={closeSidebar}
        />
      </div>

      <main className={`chat-workspace ${!activeId ? "is-new-chat" : ""}`}>
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
                  <select aria-label="Active model" value={model} disabled={modelLoaded} onChange={(event) => setModel(event.target.value)}>
                    {models.length ? models.map((item) => <option key={item}>{item}</option>) : <option value="">No models</option>}
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
                    onClick={() => { setSettingsOpen(true); setMenuOpen(false); }}
                  ><SlidersHorizontal size={19} />Model settings</button>
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

        <section className="transcript" aria-label="Conversation">
          <ChatTranscript
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
          setModel={setModel}
          conversationMode={conversationMode}
          setConversationMode={setConversationMode}
          contextWindow={contextWindow}
          setContextWindow={setContextWindow}
          thinkingMode={thinkingMode}
          setThinkingMode={setThinkingMode}
          contextUsedTokens={currentContextUsage.usedTokens}
          disabled={!model || conversationMode === "agent" && !toolModels.has(model)}
          supportsImages={visionModels.has(model)}
          supportsTools={toolModels.has(model)}
          supportsThinking={thinkingModels.has(model)}
          onOpenSettings={() => setSettingsOpen(true)}
        />
      </main>

      <div className={`settings-layer ${settingsOpen ? "open" : ""}`} aria-hidden={!settingsOpen} inert={!settingsOpen ? "" : undefined}>
        <ModelSettings
          models={models}
          model={model}
          setModel={setModel}
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
    </div>
  );
}
