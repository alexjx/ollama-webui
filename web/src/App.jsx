import { useEffect, useMemo, useRef, useState } from "react";
import hljs from "highlight.js/lib/core";
import python from "highlight.js/lib/languages/python";
import {
  ArrowCounterClockwise,
  ArrowUp,
  CaretDown,
  ChatCircle,
  Check,
  Copy,
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

const conversations = [
  {
    label: "Today",
    items: [
      { id: "python-server", title: "Implement a Python HTTP server", time: "10:42 AM" },
      { id: "quantize", title: "Quantize models in Ollama", time: "9:18 AM" },
      { id: "gpu", title: "Troubleshoot GPU offload", time: "8:03 AM" },
    ],
  },
  {
    label: "Yesterday",
    items: [
      { id: "kv-cache", title: "Explain KV cache", time: "Yesterday" },
      { id: "modelfile", title: "Use Modelfile template", time: "Yesterday" },
      { id: "rag", title: "RAG with local documents", time: "Yesterday" },
    ],
  },
  {
    label: "Previous 7 days",
    items: [
      { id: "system-prompt", title: "System prompt best practices", time: "May 10" },
      { id: "context", title: "Context length limits", time: "May 9" },
      { id: "api", title: "Ollama API examples", time: "May 8" },
      { id: "lora", title: "Fine-tune with LoRA", time: "May 7" },
    ],
  },
];

const models = ["qwen3:8b", "llama3.2:latest", "gemma3:12b"];
const contextWindows = [
  { value: "default", label: "Ollama default" },
  { value: "4096", label: "4,096 tokens" },
  { value: "8192", label: "8,192 tokens" },
  { value: "16384", label: "16,384 tokens" },
  { value: "32768", label: "32,768 tokens" },
  { value: "65536", label: "65,536 tokens" },
  { value: "131072", label: "131,072 tokens" },
];

hljs.registerLanguage("python", python);

const code = `import json
from http.server import BaseHTTPRequestHandler, HTTPServer
from datetime import datetime

class Handler(BaseHTTPRequestHandler):
    def log_message(self, format, *args):
        # Log each request with client address and timestamp
        now = datetime.now().strftime("%Y-%m-%d %H:%M:%S")
        print(f"[{now}] {self.client_address[0]} - {format % args}")

    def do_GET(self):
        if self.path == "/api/health":
            payload = {"status": "ok", "service": "ollama-webui"}
            body = json.dumps(payload).encode("utf-8")
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
        else:
            self.send_response(404)
            self.end_headers()
            self.wfile.write(b"Not found")

if __name__ == "__main__":
    host = "0.0.0.0"
    port = 8000
    server = HTTPServer((host, port), Handler)
    print(f"Listening on http://{host}:{port}")
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        print("Shutting down")
        server.server_close()`;

function IconButton({ label, children, className = "", ...props }) {
  return (
    <button className={`icon-button ${className}`} aria-label={label} title={label} {...props}>
      {children}
    </button>
  );
}

function ConversationSidebar({ activeId, onSelect, onNew, onClose }) {
  const [query, setQuery] = useState("");
  const filteredGroups = useMemo(() => {
    const normalized = query.trim().toLowerCase();
    if (!normalized) return conversations;
    return conversations
      .map((group) => ({
        ...group,
        items: group.items.filter((item) => item.title.toLowerCase().includes(normalized)),
      }))
      .filter((group) => group.items.length);
  }, [query]);

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
          onChange={(event) => setQuery(event.target.value)}
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
  model,
  setModel,
  onClose,
  systemPrompt,
  setSystemPrompt,
  contextWindow,
  setContextWindow,
  modelLoaded,
}) {
  const [temperatureOverride, setTemperatureOverride] = useState(false);
  const [temperature, setTemperature] = useState(0.7);

  function resetDefaults() {
    setSystemPrompt("");
    if (!modelLoaded) setContextWindow("default");
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
            <select id="settings-model" value={model} onChange={(event) => setModel(event.target.value)}>
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

function HighlightedCode() {
  const [copied, setCopied] = useState(false);
  const highlightedLines = useMemo(
    () => hljs.highlight(code, { language: "python" }).value.split("\n"),
    [],
  );

  async function copyCode() {
    await navigator.clipboard.writeText(code);
    setCopied(true);
    window.setTimeout(() => setCopied(false), 1600);
  }

  return (
    <div className="code-block">
      <div className="code-toolbar">
        <span>python</span>
        <button onClick={copyCode} aria-label="Copy Python code">
          {copied ? <Check size={18} /> : <Copy size={18} />}
          <span>{copied ? "Copied" : "Copy"}</span>
        </button>
      </div>
      <pre><code>{highlightedLines.map((line, index) => (
        <span
          className="code-line"
          key={index}
          dangerouslySetInnerHTML={{ __html: line || "&nbsp;" }}
        />
      ))}</code></pre>
    </div>
  );
}

function ChatTranscript({
  empty,
  streaming,
  lastPrompt,
  model,
  setModel,
  contextWindow,
  setContextWindow,
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
                  {models.map((item) => <option key={item}>{item}</option>)}
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
          </div>
          <p className="preload-help">Context is fixed when the first message loads the model. Leave it on Ollama default to send no override.</p>
        </div>
      </div>
    );
  }

  return (
    <div className="transcript-inner">
      <article className="message user-message">
        <header><strong>You</strong><time>10:42 AM</time></header>
        <p>{lastPrompt || "Write a minimal Python HTTP server using only the standard library that responds with JSON at /api/health and logs each request."}</p>
      </article>

      <article className="message assistant-message">
        <header><strong>Ollama</strong><time>10:42 AM</time></header>
        <p>Below is a minimal HTTP server using only the Python standard library. It exposes <code>GET /api/health</code>, returning JSON and logs each request to stdout.</p>
        <HighlightedCode />
        <p>Save this as <code>server.py</code> and run it with <code>python server.py</code>. Then visit <code>http://localhost:8000/api/health</code> to get a JSON response.<span className={streaming ? "stream-cursor" : "stream-cursor stopped"} aria-hidden="true" /></p>
      </article>
    </div>
  );
}

function Composer({ streaming, setStreaming, onSend, empty, model, contextWindow }) {
  const [message, setMessage] = useState("");
  const inputRef = useRef(null);

  useEffect(() => {
    if (empty) inputRef.current?.focus();
  }, [empty]);

  function submit() {
    const value = message.trim();
    if (!value) return;
    onSend(value);
    setMessage("");
  }

  function onKeyDown(event) {
    if (event.key === "Enter" && !event.shiftKey) {
      event.preventDefault();
      submit();
    }
  }

  const contextLimit = contextWindow === "default" ? 8192 : Number(contextWindow);
  const usedTokens = empty ? 0 : 2048;
  const usedPercent = empty ? 0 : Math.min(100, Math.round((usedTokens / contextLimit) * 100));
  const contextLabel = contextWindow === "default"
    ? `${usedTokens.toLocaleString()} / Ollama default`
    : `${usedTokens.toLocaleString()} / ${contextLimit.toLocaleString()} tokens (${usedPercent}%)`;

  return (
    <div className="composer-wrap">
      <div className="composer focus-within">
        <div className="composer-row">
          <IconButton label="Attach a file" className="attach-button">
            <Paperclip size={22} />
          </IconButton>
          <textarea
            ref={inputRef}
            value={message}
            rows="1"
            onChange={(event) => setMessage(event.target.value)}
            onKeyDown={onKeyDown}
            placeholder={`Message ${model}`}
            aria-label="Message"
          />
          {streaming ? (
            <button className="stop-button" onClick={() => setStreaming(false)}>
              <Stop size={16} weight="fill" /><span>Stop</span>
            </button>
          ) : (
            <button className="send-button" onClick={submit} disabled={!message.trim()}>
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
  const [activeId, setActiveId] = useState("python-server");
  const [model, setModel] = useState("qwen3:8b");
  const [settingsOpen, setSettingsOpen] = useState(() => window.innerWidth >= 1200);
  const [sidebarOpen, setSidebarOpen] = useState(false);
  const [menuOpen, setMenuOpen] = useState(false);
  const [systemPrompt, setSystemPrompt] = useState("");
  const [contextWindow, setContextWindow] = useState("default");
  const [streaming, setStreaming] = useState(true);
  const [lastPrompt, setLastPrompt] = useState("");
  const settingsTriggerRef = useRef(null);
  const sidebarTriggerRef = useRef(null);

  useEffect(() => {
    function onKeyDown(event) {
      if (event.key !== "Escape") return;
      if (settingsOpen && window.innerWidth < 1200) {
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
  }, [settingsOpen, sidebarOpen]);

  useEffect(() => {
    const isSettingsOverlay = settingsOpen && window.innerWidth < 1200;
    const isSidebarOverlay = sidebarOpen && window.innerWidth <= 900;
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
  }, [settingsOpen, sidebarOpen]);

  function closeSidebar() {
    setSidebarOpen(false);
    window.setTimeout(() => sidebarTriggerRef.current?.focus(), 0);
  }

  function closeSettings() {
    setSettingsOpen(false);
    window.setTimeout(() => settingsTriggerRef.current?.focus(), 0);
  }

  function chooseConversation(id) {
    setActiveId(id);
    setLastPrompt("");
    setStreaming(id === "python-server");
    setSidebarOpen(false);
  }

  function newChat() {
    setActiveId(null);
    setLastPrompt("");
    setStreaming(false);
    setContextWindow("default");
    setSidebarOpen(false);
  }

  function sendMessage(value) {
    setActiveId("draft");
    setLastPrompt(value);
    setStreaming(true);
  }

  const overlaySettings = settingsOpen && window.innerWidth < 1200;

  return (
    <div className={`app-shell ${settingsOpen ? "settings-is-open" : ""}`}>
      <div className={`sidebar-layer ${sidebarOpen ? "open" : ""}`}>
        <ConversationSidebar
          activeId={activeId}
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
            <div className="terminal-button" aria-hidden="true"><TerminalWindow size={24} /></div>
            <div className="select-wrap model-select">
              <select aria-label="Active model" value={model} onChange={(event) => setModel(event.target.value)}>
                {models.map((item) => <option key={item}>{item}</option>)}
              </select>
              <CaretDown size={17} aria-hidden="true" />
            </div>
          </div>
          <div className="topbar-right">
            <div className="connected-status"><span /> <span className="connected-text">Connected</span></div>
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
            lastPrompt={lastPrompt}
            model={model}
            setModel={setModel}
            contextWindow={contextWindow}
            setContextWindow={setContextWindow}
          />
        </section>
        <Composer
          streaming={streaming}
          setStreaming={setStreaming}
          onSend={sendMessage}
          empty={!activeId}
          model={model}
          contextWindow={contextWindow}
        />
      </main>

      <div className={`settings-layer ${settingsOpen ? "open" : ""}`}>
        <ModelSettings
          model={model}
          setModel={setModel}
          onClose={closeSettings}
          systemPrompt={systemPrompt}
          setSystemPrompt={setSystemPrompt}
          contextWindow={contextWindow}
          setContextWindow={setContextWindow}
          modelLoaded={Boolean(activeId)}
        />
      </div>

      {(sidebarOpen || overlaySettings) && (
        <button
          className="drawer-backdrop"
          aria-label="Close open panel"
          onClick={() => { if (sidebarOpen) closeSidebar(); if (window.innerWidth < 1200 && settingsOpen) closeSettings(); }}
        />
      )}
    </div>
  );
}
