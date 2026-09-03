import { useEffect, useMemo, useRef, useState } from "react";
import {
  createConversation,
  getConversation,
  getHealth,
  listConversations,
  listModels,
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
  { value: "enabled", label: "On" },
  { value: "disabled", label: "Off" },
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

function ConversationSidebar({ activeId, conversations, query, onQuery, onSelect, onNew, onClose }) {
  const filteredGroups = useMemo(() => groupConversations(conversations), [conversations]);

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
                <button
                  key={item.id}
                  className={`conversation-row ${activeId === item.id ? "active" : ""}`}
                  aria-current={activeId === item.id ? "page" : undefined}
                  onClick={() => onSelect(item.id)}
                >
                  <ChatCircle size={18} />
                  <span className="conversation-title">{item.title}</span>
                  <time>{item.time}</time>
                </button>
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
        <button><Gear size={23} />Settings</button>
        <button><Info size={23} />About</button>
      </div>
    </aside>
  );
}

function ModelSettings({
  models,
  model,
  setModel,
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
            <p>Ollama default sends no thinking override. On and Off explicitly control model reasoning.</p>
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

function AgentActivity({ message }) {
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
  const turn = message.agent_turn || message.metadata?.agent_turns;
  const active = message.status === "streaming";

  useEffect(() => {
    if (active && phase === "thinking" && thinkingRef.current) {
      thinkingRef.current.scrollTop = thinkingRef.current.scrollHeight;
    }
  }, [active, phase, message.thinking]);

  if (!hasActivity) return null;

  return (
    <section className={`agent-activity ${phase}`} aria-label="Agent activity">
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

function ChatTranscript({
  empty,
  streaming,
  messages,
  model,
  models,
  setModel,
  contextWindow,
  setContextWindow,
  thinkingMode,
  setThinkingMode,
  supportsThinking,
  error,
}) {
  if (empty) {
    return (
      <div className="empty-chat">
        <div className="empty-icon"><ChatCircle size={30} /></div>
        <h1>Start a local conversation</h1>
        <p>Choose how the model should load, then send your first message.</p>
        <div className="preload-setup" aria-label="New chat model setup">
          <div className="preload-status"><span />Model not loaded</div>
          <div className="preload-fields">
            <label className="setup-field">
              <span>Model</span>
              <div className="select-wrap wide">
                <select value={model} onChange={(event) => setModel(event.target.value)}>
                  {models.length ? models.map((item) => <option key={item}>{item}</option>) : <option value="">No local models found</option>}
                </select>
                <CaretDown size={18} aria-hidden="true" />
              </div>
            </label>
            <label className="setup-field">
              <span>Context window</span>
              <div className="select-wrap wide">
                <select value={contextWindow} onChange={(event) => setContextWindow(event.target.value)}>
                  {contextWindows.map((item) => (
                    <option key={item.value} value={item.value}>{item.label}</option>
                  ))}
                </select>
                <CaretDown size={18} aria-hidden="true" />
              </div>
            </label>
            <label className="setup-field">
              <span>Thinking</span>
              <div className="select-wrap wide">
                <select
                  value={thinkingMode}
                  disabled={!supportsThinking}
                  onChange={(event) => setThinkingMode(event.target.value)}
                >
                  {supportsThinking
                    ? thinkingModes.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)
                    : <option value="default">Not supported</option>}
                </select>
                <CaretDown size={18} aria-hidden="true" />
              </div>
            </label>
          </div>
          <p className="preload-help">Context and thinking are fixed when the first message loads the model. Ollama default sends no override.</p>
        </div>
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
          {message.role === "assistant" && <AgentActivity message={message} />}
          {(message.content || message.role === "assistant" && message.status !== "streaming") && (
            <p className="message-content">{message.content || (message.status === "streaming" ? "" : "No response was generated.")}
              {message.role === "assistant" && message.status === "streaming" && <span className="stream-cursor" aria-hidden="true" />}
            </p>
          )}
          {message.status === "cancelled" && <p className="message-status">Generation stopped</p>}
          {message.status === "error" && <p className="message-status error">Generation failed</p>}
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

function Composer({ streaming, onStop, onSend, empty, model, contextWindow, disabled, supportsImages, supportsTools }) {
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
  const usedPercent = 0;
  const contextLabel = contextLimit ? `${contextLimit.toLocaleString()} token limit` : "Ollama default";

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
        {!supportsTools && model && (
          <div className="agent-model-warning" role="status">Agent mode requires a tool-capable model, even when a task may not need a tool.</div>
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
        <div className="composer-row">
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
          <IconButton
            label={supportsImages ? "Attach images" : "Image input requires a vision model"}
            className="attach-button"
            aria-disabled={!supportsImages}
            onClick={() => supportsImages ? fileInputRef.current?.click() : setAttachmentError("Choose a vision-capable model to attach images.")}
          >
            <Paperclip size={22} />
          </IconButton>
          <textarea
            ref={inputRef}
            value={message}
            rows="1"
            onChange={(event) => setMessage(event.target.value)}
            onPaste={(event) => {
              const images = [...event.clipboardData.files].filter((file) => file.type.startsWith("image/"));
              if (images.length) {
                event.preventDefault();
                addImages(images);
              }
            }}
            onKeyDown={onKeyDown}
            placeholder={supportsTools ? `Ask ${model} anything` : "Choose a tool-capable model"}
            aria-label="Message"
          />
          {streaming ? (
            <button className="stop-button" aria-label="Stop generation" onClick={onStop}>
              <Stop size={16} weight="fill" /><span>Stop</span>
            </button>
          ) : (
            <button className="send-button" aria-label="Send message" onClick={submit} disabled={(!message.trim() && attachments.length === 0) || disabled || (!supportsImages && attachments.length > 0)}>
              <ArrowUp size={19} weight="bold" /><span>Send</span>
            </button>
          )}
        </div>
        <div className="context-row">
          <span>Context&nbsp;&nbsp;{contextLabel}</span>
          <div className="context-track" aria-label={`${usedPercent}% of context used`} role="progressbar" aria-valuenow={usedPercent} aria-valuemin="0" aria-valuemax="100">
            <span style={{ width: `${usedPercent}%` }} />
          </div>
        </div>
      </div>
    </div>
  );
}

export function App() {
  const [activeId, setActiveId] = useState(null);
  const [model, setModel] = useState("");
  const [models, setModels] = useState([]);
  const [visionModels, setVisionModels] = useState(() => new Set());
  const [toolModels, setToolModels] = useState(() => new Set());
  const [thinkingModels, setThinkingModels] = useState(() => new Set());
  const [conversationItems, setConversationItems] = useState([]);
  const [query, setQuery] = useState("");
  const [messages, setMessages] = useState([]);
  const [connected, setConnected] = useState(false);
  const [chatError, setChatError] = useState("");
  const [settingsOpen, setSettingsOpen] = useState(() => window.innerWidth >= 1200);
  const [sidebarOpen, setSidebarOpen] = useState(false);
  const [menuOpen, setMenuOpen] = useState(false);
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
    function onKeyDown(event) {
      if (event.key !== "Escape") return;
      if (settingsOpen && viewportWidth < 1200) {
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
  }, [settingsOpen, sidebarOpen, viewportWidth]);

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
      setModel(payload.conversation.model);
      setSystemPrompt(payload.conversation.system_prompt || "");
      setContextWindow(payload.conversation.context_window == null ? "default" : String(payload.conversation.context_window));
      setThinkingMode(payload.conversation.thinking_enabled == null
        ? "default"
        : payload.conversation.thinking_enabled ? "enabled" : "disabled");
      setTemperatureOverride(payload.conversation.temperature != null);
      setTemperature(payload.conversation.temperature ?? 0.7);
      setModelLoaded(payload.conversation.message_count > 0);
      setMessages(payload.messages);
    } catch (error) {
      if (error.name !== "AbortError") setChatError(error.message);
    }
  }

  function newChat() {
    generationRef.current?.abort();
    setActiveId(null);
    setMessages([]);
    setStreaming(false);
    setModelLoaded(false);
    setSystemPrompt("");
    setContextWindow("default");
    setThinkingMode("default");
    setTemperatureOverride(false);
    setTemperature(0.7);
    setChatError("");
    setSidebarOpen(false);
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
          system_prompt: systemPrompt,
          context_window: contextWindow === "default" ? null : Number(contextWindow),
          thinking_enabled: thinkingMode === "default" ? null : thinkingMode === "enabled",
          temperature: temperatureOverride ? temperature : null,
        }, controller.signal);
        conversationId = conversation.id;
        setActiveId(conversationId);
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
          onQuery={setQuery}
          onSelect={chooseConversation}
          onNew={newChat}
          onClose={closeSidebar}
        />
      </div>

      <main className="chat-workspace">
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
            <div className="terminal-button" aria-hidden="true"><ChatCircle size={24} /></div>
            <div className="select-wrap model-select">
              <select aria-label="Active model" value={model} disabled={modelLoaded} onChange={(event) => setModel(event.target.value)}>
                {models.length ? models.map((item) => <option key={item}>{item}</option>) : <option value="">No models</option>}
              </select>
              <CaretDown size={17} aria-hidden="true" />
            </div>
          </div>
          <div className="topbar-right">
            <div className={`agent-status ${streaming ? "working" : ""}`} role="status" aria-live="polite">
              {streaming ? <CircleNotch size={16} /> : <ChatCircle size={16} />}<span>{streaming ? "Agent working" : "Agent ready"}</span>
            </div>
            <div className={`connected-status ${connected ? "" : "disconnected"}`}><span /> <span className="connected-text">{connected ? "Connected" : "Ollama unavailable"}</span></div>
            <div className="menu-wrap">
              <IconButton ref={settingsTriggerRef} label="Conversation menu" onClick={() => setMenuOpen((value) => !value)}>
                <DotsThreeVertical size={23} weight="bold" />
              </IconButton>
              {menuOpen && (
                <div className="overflow-menu" role="menu">
                  <button
                    role="menuitem"
                    onClick={() => { setSettingsOpen(true); setMenuOpen(false); }}
                  ><SlidersHorizontal size={19} />Model settings</button>
                  <button role="menuitem"><NotePencil size={19} />Rename chat</button>
                </div>
              )}
            </div>
          </div>
        </header>

        <section className="transcript" aria-label="Conversation">
          <ChatTranscript
            empty={!activeId}
            streaming={streaming}
            messages={messages}
            model={model}
            models={models}
            setModel={setModel}
            contextWindow={contextWindow}
            setContextWindow={setContextWindow}
            thinkingMode={thinkingMode}
            setThinkingMode={setThinkingMode}
            supportsThinking={thinkingModels.has(model)}
            error={chatError}
          />
        </section>
        <Composer
          streaming={streaming}
          onStop={() => generationRef.current?.abort()}
          onSend={sendMessage}
          empty={!activeId}
          model={model}
          contextWindow={contextWindow}
          disabled={!model || !toolModels.has(model)}
          supportsImages={visionModels.has(model)}
          supportsTools={toolModels.has(model)}
        />
      </main>

      <div className={`settings-layer ${settingsOpen ? "open" : ""}`} aria-hidden={!settingsOpen} inert={!settingsOpen ? "" : undefined}>
        <ModelSettings
          models={models}
          model={model}
          setModel={setModel}
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
    </div>
  );
}
