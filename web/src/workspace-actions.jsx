import { useEffect, useRef, useState } from "react";
import { createWorkspaceFolder, listWorkspace, moveWorkspaceItem } from "./api";
import { workspaceBreadcrumbs } from "./workspace";

export function workspaceChildPath(folder, name) {
  if (!name || name === "." || name === ".." || /[/\\\x00]/.test(name)) throw new Error("Enter a name without slashes; . and .. are not allowed.");
  return folder ? `${folder}/${name}` : name;
}

export default function WorkspaceAction({ action, folder, onClose, onSuccess }) {
  const dialogRef = useRef(null);
  const inputRef = useRef(null);
  const [name, setName] = useState(action.entry?.name || "");
  const [destination, setDestination] = useState(folder);
  const [listing, setListing] = useState(null);
  const [loading, setLoading] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [revision, setRevision] = useState(0);
  const moving = action.kind === "move";
  const title = moving ? `Move ${action.entry.name}` : action.kind === "rename" ? `Rename ${action.entry.name}` : "New folder";
  useEffect(() => {
    const trigger = document.activeElement;
    dialogRef.current.showModal();
    inputRef.current?.select();
    return () => { dialogRef.current?.close(); if (trigger?.isConnected) trigger.focus(); };
  }, []);
  useEffect(() => {
    if (!moving) return;
    const controller = new AbortController();
    setLoading(true); setListing(null); setError("");
    // Fetch every bounded list page so folders after the first page remain reachable.
    (async () => {
      let offset = 0;
      const entries = [];
      do {
        const result = await listWorkspace(destination, offset, controller.signal);
        entries.push(...result.entries.filter((entry) => entry.type === "directory"));
        offset = result.nextOffset;
      } while (offset != null);
      if (!controller.signal.aborted) setListing(entries);
    })().catch((err) => { if (!controller.signal.aborted) setError(err.message); })
      .finally(() => { if (!controller.signal.aborted) setLoading(false); });
    return () => controller.abort();
  }, [moving, destination, revision]);
  async function submit(event) {
    event.preventDefault();
    if (busy) return;
    setError("");
    try {
      const target = workspaceChildPath(moving ? destination : folder, moving ? action.entry.name : name);
      setBusy(true);
      if (action.kind === "folder") await createWorkspaceFolder(target);
      else await moveWorkspaceItem(action.entry.path, target);
      onSuccess(action.kind === "folder" ? "Folder created." : moving ? "Item moved." : "Item renamed.");
      dialogRef.current.close();
    } catch (err) { setError(err.message); }
    finally { setBusy(false); }
  }
  return <dialog ref={dialogRef} className="workspace-action-dialog" aria-labelledby="workspace-action-title" onClose={(event) => { event.stopPropagation(); onClose(); }} onCancel={(event) => { event.stopPropagation(); if (busy) event.preventDefault(); }}>
    <form onSubmit={submit}>
      <h3 id="workspace-action-title">{title}</h3>
      {moving ? <>
        <p>Choose a destination folder.</p>
        <nav className="workspace-breadcrumbs" aria-label="Destination folders">
          {workspaceBreadcrumbs(destination).map((part) => <button key={part.path} type="button" disabled={busy} onClick={() => setDestination(part.path)}>{part.name}</button>)}
        </nav>
        <div className="workspace-destinations" aria-busy={loading}>
          {loading && <p role="status">Loading folders…</p>}
          {listing?.filter((entry) => entry.path !== action.entry.path).map((entry) => <button className="workspace-entry" type="button" key={entry.path} disabled={busy} onClick={() => setDestination(entry.path)} aria-label={`Destination folder: ${entry.name}`}>{entry.name} /</button>)}
          {listing && !listing.some((entry) => entry.path !== action.entry.path) && <p>No subfolders here.</p>}
        </div>
        {!loading && !listing && <button type="button" className="workspace-download" onClick={() => setRevision((value) => value + 1)}>Retry folders</button>}
      </> : <label className="workspace-name-label">{action.kind === "folder" ? "Folder name" : "Name"}<input ref={inputRef} autoFocus value={name} disabled={busy} onChange={(event) => setName(event.target.value)} required /></label>}
      <p className="workspace-action-note">Existing items will not be overwritten.</p>
      {error && <p className="error" role="alert">{error}</p>}
      <footer><button className="workspace-download" type="button" disabled={busy} onClick={() => dialogRef.current.close()}>Cancel</button><button className="workspace-download" disabled={busy || (moving && (loading || !listing || destination === folder))}>{busy ? "Saving…" : moving ? "Move here" : action.kind === "folder" ? "Create folder" : "Save"}</button></footer>
    </form>
  </dialog>;
}
