import { useEffect, useRef, useState } from "react";
import { X, Plus, ArrowLeft } from "@phosphor-icons/react";
import { listTemplates, createTemplate, updateTemplate, deleteTemplate } from "./api";
import "./templates.css";
import { templateInput } from "./templates-editor";

const fields = [
  ["model", "Model"], ["mode", "Mode", ["chat", "agent"]],
  ["context_window", "Context window"],
  ["thinking_mode", "Thinking", ["off", "on", "low", "medium", "high", "max"]],
  ["temperature", "Temperature"], ["web_search_enabled", "Web search", ["true", "false"]],
];
const blank = { name: "", description: "", message: "", system_prompt: "", settings: null };
function draftOf(value = blank) {
  return { name: value.name || "", description: value.description || "", message: value.message || "", system_prompt: value.system_prompt || "", settings: value.settings ? { ...value.settings } : null };
}
function settingLabel(key, value) {
  if (value === null) return key === "context_window" ? "Application default" : "Ollama default";
  if (key === "web_search_enabled") return value ? "Enabled (Agent only)" : "Disabled";
  if (key === "context_window") return `${Number(value).toLocaleString()} tokens`;
  if (key === "mode") return value === "agent" ? "Agent" : "Chat";
  return String(value);
}

export default function TemplatesDialog({ onClose, onApply, initialTemplate = null, sourceMessages = [], models = [], currentSettings = {}, hasConversation = false, hasDraft = false, applyDisabled = false }) {
  const dialog = useRef(null);
  const alive = useRef(false);
  const listRequest = useRef(null);
  const nameInput = useRef(null);
  const initialFocus = useRef(null);
  const detailHeading = useRef(null);
  const [templates, setTemplates] = useState([]);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState("");
  const [revision, setRevision] = useState(0);
  const [query, setQuery] = useState("");
  const [selectedId, setSelectedId] = useState(null);
  const [editor, setEditor] = useState(initialTemplate ? { id: null, initial: draftOf(initialTemplate) } : null);
  const [draft, setDraft] = useState(() => draftOf(initialTemplate || blank));
  const [error, setError] = useState("");
  const [pending, setPending] = useState(false);
  const [confirmation, setConfirmation] = useState(null);
  const [insertion, setInsertion] = useState("append");
  const selected = templates.find((item) => item.id === selectedId);
  const dirty = editor && JSON.stringify(draft) !== JSON.stringify(editor.initial);

  useEffect(() => {
    alive.current = true;
    dialog.current.showModal();
    initialFocus.current?.focus();
    return () => { alive.current = false; };
  }, []);
  useEffect(() => {
    const controller = new AbortController();
    listRequest.current = controller;
    setLoading(true); setLoadError("");
    listTemplates(controller.signal).then((items) => {
      if (!controller.signal.aborted) setTemplates(items);
    }).catch((err) => { if (!controller.signal.aborted) setLoadError(err.message); })
      .finally(() => { if (!controller.signal.aborted) setLoading(false); });
    return () => controller.abort();
  }, [revision]);
  useEffect(() => { if (selectedId && !confirmation && !editor) detailHeading.current?.focus(); }, [selectedId, confirmation, editor]);
  useEffect(() => { if (editor && !confirmation) nameInput.current?.focus(); }, [editor, confirmation]);
  function navigate(action) {
    if (pending) return;
    if (dirty) setConfirmation({ kind: "discard", action });
    else { setError(""); action(); }
  }
  function edit(value = blank, id = null) {
    navigate(() => { const next = draftOf(value); setEditor({ id, initial: next }); setDraft(next); setConfirmation(null); });
  }
  function setField(key, value) { setDraft((old) => ({ ...old, [key]: value })); }
  function setSetting(key, value) {
    setDraft((old) => {
      const settings = { ...old.settings };
      if (value === "inherit") delete settings[key];
      else settings[key] = value === "default" ? null : key === "web_search_enabled" ? value === "true" : value;
      return { ...old, settings };
    });
  }
  async function save(event) {
    event.preventDefault();
    if (!draft.name.trim() || (!draft.message.trim() && !draft.system_prompt.trim())) {
      setError("Enter a name and at least a message or system instructions."); return;
    }
    setPending(true); setError("");
    try {
      const input = templateInput(draft);
      const saved = editor.id ? await updateTemplate(editor.id, input) : await createTemplate(input);
      if (!alive.current) return;
      listRequest.current?.abort();
      setLoading(false);
      setRevision((value) => value + 1);
      setTemplates((items) => [saved, ...items.filter((item) => item.id !== saved.id)]);
      setSelectedId(saved.id); setEditor(null); setQuery("");
    } catch (err) { if (alive.current) setError(err.message); }
    finally { if (alive.current) setPending(false); }
  }
  async function remove() {
    setPending(true); setError("");
    try {
      await deleteTemplate(selected.id);
      if (!alive.current) return;
      listRequest.current?.abort();
      setLoading(false);
      setRevision((value) => value + 1);
      setTemplates((items) => items.filter((item) => item.id !== selected.id));
      setSelectedId(null); setConfirmation(null);
    } catch (err) { if (alive.current) setError(err.message); }
    finally { if (alive.current) setPending(false); }
  }
  function apply(target) { setError(onApply(selected, target, insertion) || ""); }
  function requestClose(event) {
    event.preventDefault();
    event.stopPropagation();
    if (pending) return;
    if (confirmation) { setConfirmation(null); setError(""); }
    else navigate(onClose);
  }
  const filtered = templates.filter((item) => `${item.name} ${item.description}`.toLowerCase().includes(query.toLowerCase()));
  return <dialog ref={dialog} className="templates-dialog" aria-labelledby="templates-title" onCancel={requestClose} onKeyDown={(event) => { if (event.key === "Escape") requestClose(event); }}>
    <header className="templates-header"><div><h2 id="templates-title">Conversation templates</h2><p>Save prompts you use often. Review before sending.</p></div><button type="button" className="icon-button" aria-label="Close templates" disabled={pending} onClick={() => navigate(onClose)}><X size={22} /></button></header>
    {confirmation ? <section className="templates-confirm" role="alertdialog" aria-modal="true" aria-labelledby="template-confirm-title">
      <h3 id="template-confirm-title">{confirmation.kind === "delete" ? `Delete “${selected.name}”?` : "Discard unsaved changes?"}</h3>
      <p>{confirmation.kind === "delete" ? "This cannot be undone. Existing conversations will stay unchanged." : "Your changes to this template have not been saved."}</p>
      {error && <p role="alert" className="templates-error">{error}</p>}
      <div className="templates-actions"><button type="button" autoFocus disabled={pending} onClick={() => { setConfirmation(null); setError(""); }}>Cancel</button><button type="button" className="templates-danger" disabled={pending} onClick={() => { if (confirmation.kind === "delete") remove(); else { const action = confirmation.action; setConfirmation(null); setError(""); action(); } }}>{pending ? "Deleting…" : confirmation.kind === "delete" ? "Delete template" : "Discard changes"}</button></div>
    </section> : <div className={`templates-body ${editor || selected ? "templates-has-detail" : ""}`}>
      <aside className="templates-library" aria-label="Saved templates">
        <button type="button" className="templates-create" disabled={pending} onClick={() => edit()}><Plus size={18} /> Create template</button>
        <input ref={initialFocus} type="search" aria-label="Search templates" placeholder="Search templates…" value={query} onChange={(event) => setQuery(event.target.value)} disabled={pending} />
        {loading && <p role="status">Loading templates…</p>}
        {loadError && <div role="alert"><p>{loadError}</p><button type="button" onClick={() => setRevision((value) => value + 1)}>Try again</button></div>}
        {!loading && !loadError && !filtered.length && <p>{templates.length ? "No matching templates." : "No saved templates yet. Create one to get started."}</p>}
        <ul>{filtered.map((item) => <li key={item.id}><button type="button" disabled={pending} aria-current={!editor && selectedId === item.id ? "true" : undefined} onClick={() => navigate(() => { setSelectedId(item.id); setEditor(null); })}><strong>{item.name}</strong>{item.description && <span>{item.description}</span>}</button></li>)}</ul>
      </aside>
      <section className="templates-detail" aria-label={editor ? "Template editor" : "Template preview"}>
        {(editor || selected) && <button type="button" className="templates-back" disabled={pending} onClick={() => navigate(() => { setEditor(null); setSelectedId(null); requestAnimationFrame(() => initialFocus.current?.focus()); })}><ArrowLeft size={18} /> Back to templates</button>}
        {error && <p role="alert" className="templates-error">{error}</p>}
        {editor ? <form onSubmit={save}>
          <h3>{editor.id ? "Edit template" : "Create template"}</h3>
          <fieldset disabled={pending}>
            <label>Name<input ref={nameInput} required maxLength={120} value={draft.name} onChange={(event) => setField("name", event.target.value)} /></label>
            <label>Description<input maxLength={2000} value={draft.description} onChange={(event) => setField("description", event.target.value)} /></label>
            {sourceMessages.length > 0 && <label>Use a conversation message<select defaultValue="" onChange={(event) => { if (event.target.value !== "") setField("message", sourceMessages[Number(event.target.value)].content); }}><option value="">Select a user message…</option>{sourceMessages.map((message, index) => <option key={message.id || index} value={index}>{message.content.slice(0, 100) || "Empty message"}</option>)}</select></label>}
            <label>Message<textarea rows={6} value={draft.message} onChange={(event) => setField("message", event.target.value)} /></label>
            <label>System instructions <span className="templates-muted">New chats only</span><textarea rows={3} value={draft.system_prompt} onChange={(event) => setField("system_prompt", event.target.value)} /></label>
            <label className="templates-check"><input type="checkbox" checked={draft.settings !== null} onChange={(event) => setField("settings", event.target.checked ? Object.fromEntries(Object.entries(currentSettings).filter(([key, value]) => key !== "model" || value)) : null)} /> Include new-chat settings</label>
            {draft.settings !== null && <div className="templates-settings"><p>Unspecified settings inherit the current setup. These settings apply only to a new chat.</p>{fields.map(([key, label, choices]) => {
              const value = !(key in draft.settings) ? "inherit" : draft.settings[key] === null ? "default" : String(draft.settings[key]);
              const numeric = key === "temperature" || key === "context_window";
              return <label key={key}>{label}<select value={numeric && value !== "inherit" && value !== "default" ? "custom" : value} onChange={(event) => setSetting(key, event.target.value === "custom" ? key === "temperature" ? "0.7" : "8192" : event.target.value)}><option value="inherit">Current setup</option>{["context_window", "thinking_mode", "temperature"].includes(key) && <option value="default">{key === "context_window" ? "Application default" : "Ollama default"}</option>}{numeric ? <option value="custom">Custom value</option> : [...new Set(key === "model" ? [...models, ...(value !== "inherit" && value !== "default" ? [value] : [])] : choices)].map((choice) => <option key={choice} value={choice}>{choice === "true" ? "Enabled (Agent only)" : choice === "false" ? "Disabled" : choice}</option>)}</select>{numeric && value !== "inherit" && value !== "default" && <input aria-label={`${label} custom value`} type="number" required min={key === "temperature" ? 0 : 1} max={key === "temperature" ? 2 : undefined} step={key === "temperature" ? "any" : "1"} value={value} onChange={(event) => setSetting(key, event.target.value)} />}</label>;
            })}</div>}
          </fieldset>
          <div className="templates-actions"><button type="button" disabled={pending} onClick={() => navigate(() => setEditor(null))}>Cancel</button><button className="templates-primary" disabled={pending} type="submit">{pending ? "Saving…" : "Save template"}</button></div>
        </form> : selected ? <>
          <h3 tabIndex={-1} ref={detailHeading}>{selected.name}</h3>{selected.description && <p>{selected.description}</p>}
          <div className="templates-actions templates-management"><button type="button" onClick={() => edit(selected, selected.id)}>Edit</button><button type="button" onClick={() => edit({ ...selected, name: `${selected.name.slice(0, 113)} (copy)` })}>Duplicate</button><button type="button" onClick={() => { setError(""); setConfirmation({ kind: "delete" }); }}>Delete</button></div>
          {selected.message && <><h4>Message</h4><pre>{selected.message}</pre></>}
          {selected.system_prompt && <><h4>System instructions · New chats only</h4><pre>{selected.system_prompt}</pre></>}
          {selected.settings && <><h4>New-chat settings</h4><dl>{fields.filter(([key]) => key in selected.settings).map(([key, label]) => <div key={key}><dt>{label}</dt><dd>{settingLabel(key, selected.settings[key])}</dd></div>)}</dl></>}
          <p className="templates-muted">{!selected.system_prompt && "A new chat uses the model’s default system instructions. "}{hasConversation ? "Insert message adds only the message. Conversation settings stay the same." : "Insert message keeps your current setup. Use in new chat also applies saved instructions and settings."} Nothing is sent automatically.</p>
          {hasDraft && <label>Existing draft<select value={insertion} onChange={(event) => setInsertion(event.target.value)}><option value="append">Append template message</option><option value="replace">Replace draft text</option></select></label>}
          {applyDisabled && <p role="status">Wait for the active operation to finish before applying a template.</p>}
          <div className="templates-actions"><button type="button" disabled={applyDisabled || !selected.message.trim()} onClick={() => apply("current")}>Insert message</button><button type="button" className="templates-primary" disabled={applyDisabled} onClick={() => apply("new")}>Use in new chat</button></div>
        </> : <div className="templates-empty"><h3>Your reusable starting points</h3><p>Select a template to preview it, or create one for a task you repeat.</p></div>}
      </section>
    </div>}
  </dialog>;
}
