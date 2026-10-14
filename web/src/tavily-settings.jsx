import { useEffect, useRef, useState } from "react";
import { webSearchSettingsInput, webSearchStatus } from "./web-search.js";

export default function TavilySettings({ settings, onSave, onTest }) {
  const [token, setToken] = useState("");
  const [enabled, setEnabled] = useState(settings?.enabled ?? false);
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const dirtyRef = useRef(false);
  const operationRef = useRef(null);
  const mountedRef = useRef(true);

  useEffect(() => {
    if (!dirtyRef.current) setEnabled(settings?.enabled ?? false);
  }, [settings?.enabled]);
  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
      // Saving is already a server mutation. Let its public status reach App
      // after closing; only the disposable test search can be cancelled here.
      if (operationRef.current?.action === "test") operationRef.current.abort();
    };
  }, []);

  function editToken(value) {
    setToken(value);
    setError("");
    setNotice("");
  }

  async function perform(action) {
    if (operationRef.current) return;
    const controller = new AbortController();
    controller.action = action;
    operationRef.current = controller;
    setBusy(action);
    setError("");
    setNotice("");
    try {
      if (action === "test") {
        const result = await onTest(controller.signal);
        if (mountedRef.current && !controller.signal.aborted) setNotice(`Test search succeeded: ${result.result_count} ${result.result_count === 1 ? "result" : "results"}.`);
      } else {
        const result = await onSave(action === "remove"
          ? { enabled: false, remove_token: true }
          : webSearchSettingsInput(enabled, token), controller.signal);
        if (mountedRef.current && !controller.signal.aborted) {
          dirtyRef.current = false;
          setEnabled(result.enabled);
          setToken("");
          setNotice(action === "remove" ? "Tavily token removed. Web search is disabled." : "Web search settings saved.");
        }
      }
    } catch (failure) {
      if (mountedRef.current && !controller.signal.aborted) setError(failure.message);
    } finally {
      if (operationRef.current === controller) {
        operationRef.current = null;
        if (mountedRef.current && !controller.signal.aborted) setBusy("");
      }
    }
  }

  const unavailable = !settings || Boolean(busy);
  return (
    <section className="settings-section web-search-settings" aria-labelledby="web-search-title">
      <div className="system-section-heading">
        <div><h3 id="web-search-title">Web search</h3><p>Tavily search for Agent mode.</p></div>
        <span className={`web-search-status ${settings?.enabled && settings?.configured ? "enabled" : ""}`}>{webSearchStatus(settings)}</span>
      </div>
      <form onSubmit={(event) => { event.preventDefault(); perform("save"); }} autoComplete="off">
        <label htmlFor="tavily-api-token">Tavily API token</label>
        <input id="tavily-api-token" type="password" autoComplete="off" spellCheck="false" value={token} disabled={unavailable} placeholder={settings?.configured ? "Leave blank to keep the saved token" : "Enter your Tavily API token"} aria-describedby="tavily-token-help" onChange={(event) => editToken(event.target.value)} />
        <p id="tavily-token-help">{settings?.configured ? "A token is saved on this server. Leave this field blank to keep it." : "Save your token on this server to allow agents to search the web."}</p>
        <label className="web-search-toggle"><input type="checkbox" checked={enabled} disabled={unavailable} onChange={(event) => { dirtyRef.current = true; setEnabled(event.target.checked); setError(""); setNotice(""); }} />Enable web search in Agent mode</label>
        <div className="web-search-actions">
          <button className="web-search-save" type="submit" disabled={unavailable || (enabled && !settings?.configured && !token.trim())}>{busy === "save" ? "Saving…" : "Save"}</button>
          <button type="button" onClick={() => perform("test")} disabled={unavailable || !settings?.enabled || !settings?.configured}>{busy === "test" ? "Searching…" : "Test search"}</button>
          <button type="button" onClick={() => perform("remove")} disabled={unavailable || !settings?.configured}>{busy === "remove" ? "Removing…" : "Remove token"}</button>
        </div>
        <p>Test search uses the saved, enabled token for one basic Tavily search. Save changes first.</p>
        {error && <p className="settings-error" role="alert">{error}</p>}
        {notice && <p className="web-search-notice" role="status">{notice}</p>}
      </form>
    </section>
  );
}
