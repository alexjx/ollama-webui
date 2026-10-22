import { useEffect, useId, useRef, useState } from "react";
import { MagnifyingGlass, X } from "@phosphor-icons/react";
import "./context-inspector.css";

function JSONDetails({ label, value }) {
  const [open, setOpen] = useState(false);
  return <details onToggle={(event) => setOpen(event.currentTarget.open)}>
    <summary>{label}</summary>
    {open && <pre tabIndex={0}>{JSON.stringify(value, null, 2)}</pre>}
  </details>;
}

function ModelMessage({ message, index, open = false }) {
  return <details className="context-message" open={open}>
    <summary>
      <strong>{index != null ? `${index + 1}. ` : ""}{message.role || "assistant"}</strong>
      {message.tool_name && <span> · {message.tool_name}</span>}
      <span className="context-message-preview">{message.content?.slice(0, 100) || (message.tool_calls?.length ? "Tool call" : "No text")}</span>
    </summary>
    {message.content && <pre tabIndex={0}>{message.content}</pre>}
    {message.thinking && <details><summary>Thinking</summary><pre tabIndex={0}>{message.thinking}</pre></details>}
    {message.tool_calls?.length > 0 && <JSONDetails label={`Tool calls (${message.tool_calls.length})`} value={message.tool_calls} />}
    {message.images?.length > 0 && <p>{message.images.length} image(s) sent. Image data is available in the raw request.</p>}
  </details>;
}

function InspectorDialog({ exchange, onClose }) {
  const dialogRef = useRef(null);
  const headingId = useId();
  useEffect(() => {
    const dialog = dialogRef.current;
    dialog.showModal();
    return () => dialog.close();
  }, []);
  const request = exchange?.request;
  function close(event) {
    event?.preventDefault();
    dialogRef.current.close();
    onClose();
  }
  return <dialog ref={dialogRef} className="context-inspector" aria-labelledby={headingId} onCancel={close}>
    <header className="context-inspector-header">
      <div><h2 id={headingId}>Model context</h2><p>Latest model round for this reply</p></div>
      <button type="button" aria-label="Close context inspector" onClick={close} autoFocus><X size={22} aria-hidden="true" /></button>
    </header>
    <div className="context-inspector-body">
      {!request ? <p>No model request was captured for this reply. Send a new message to inspect its actual context.</p> : <>
        <p className="context-inspector-meta">{request.model} · Round {exchange.turn} · {exchange.status}</p>
        <p>The messages below are the context sent to Ollama, including system instructions and any retained history or tool results. In Agent mode, this is the latest main-agent call, not every round or child task. Ollama applies its own model template and context limits.</p>
        {exchange.error && <p className="context-inspector-error">{exchange.error}</p>}
        <h3>Sent messages ({request.messages.length})</h3>
        <div className="context-message-list">
          {request.messages.map((message, index) => <ModelMessage key={index} message={message} index={index} open={index === request.messages.length - 1} />)}
        </div>
        <h3>Model response</h3>
        {exchange.response ? <ModelMessage message={exchange.response} open /> : <p>No response was received.</p>}
        <h3>Request details</h3>
        <p>Settings omitted from the request inherit Ollama defaults.</p>
        <JSONDetails label="Model settings" value={{ model: request.model, stream: request.stream, ...(request.think !== undefined ? { think: request.think } : {}), ...(request.options ? { options: request.options } : {}) }} />
        <JSONDetails label={`Available tools (${request.tools?.length || 0})`} value={request.tools || []} />
        <JSONDetails label="Raw request JSON" value={request} />
        <JSONDetails label="Raw response message JSON" value={exchange.response} />
      </>}
    </div>
  </dialog>;
}

export default function ContextInspector({ message }) {
  const [open, setOpen] = useState(false);
  const triggerRef = useRef(null);
  if (message.status === "streaming") return null;
  function close() {
    setOpen(false);
    triggerRef.current?.focus();
  }
  return <>
    <button ref={triggerRef} className="select-response-button context-inspector-trigger" type="button" onClick={() => setOpen(true)}>
      <MagnifyingGlass size={16} aria-hidden="true" />Inspect context
    </button>
    {open && <InspectorDialog exchange={message.metadata?.model_exchange} onClose={close} />}
  </>;
}
