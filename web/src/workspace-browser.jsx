import { useEffect, useRef, useState } from "react";
import { ArrowLeft, ArrowClockwise, Check, Copy, DownloadSimple, File, Folder, FolderOpen, LinkSimple, X } from "@phosphor-icons/react";
import { copyText } from "./copy-text";
import { listWorkspace, previewWorkspaceFile } from "./api";
import { formatFileSize, workspaceBreadcrumbs, workspaceFileURL } from "./workspace";

function CopyPreview({ preview }) {
  const [status, setStatus] = useState("");
  return <>
    <button type="button" className="workspace-download" disabled={status === "copying"}
      aria-label={preview.truncated ? "Copy text preview" : "Copy file content"}
      title={preview.truncated ? "Copy the displayed text (first 256 KB)" : "Copy file content"}
      onClick={async () => {
        setStatus("copying");
        setStatus(await copyText(preview.text ?? "") ? "copied" : "error");
      }}>
      {status === "copied" ? <Check size={19} aria-hidden="true" /> : <Copy size={19} aria-hidden="true" />}
      <span>{status === "copied" ? "Copied" : preview.truncated ? "Copy preview" : "Copy"}</span>
    </button>
    <span className="workspace-copy-status" role="status">{status === "copied" ? "Copied to clipboard." : status === "error" ? "Could not copy. Select the text and press Ctrl+C or ⌘C." : ""}</span>
  </>;
}

export default function WorkspaceBrowser({ onClose, initialPath = "" }) {
  const dialogRef = useRef(null);
  const closeRef = useRef(null);
  const selectedTriggerRef = useRef(null);
  const listRequest = useRef(null);
  const initialTargetRef = useRef(initialPath);
  const [path, setPath] = useState(initialPath);
  const [listing, setListing] = useState(null);
  const [loading, setLoading] = useState(true);
  const [listError, setListError] = useState("");
  const [selected, setSelected] = useState(null);
  const [preview, setPreview] = useState(null);
  const [previewError, setPreviewError] = useState("");
  const [imageError, setImageError] = useState(false);
  const [previewLoading, setPreviewLoading] = useState(false);
  const [revision, setRevision] = useState(0);
  const previewHeadingRef = useRef(null);

  useEffect(() => {
    const dialog = dialogRef.current;
    dialog.showModal();
    closeRef.current?.focus();
    return () => { dialog.close(); listRequest.current?.abort(); };
  }, []);

  async function loadDirectory(folder, offset = 0) {
    listRequest.current?.abort();
    const controller = new AbortController();
    listRequest.current = controller;
    setLoading(true);
    setListError("");
    if (!offset) setListing(null);
    try {
      const result = await listWorkspace(folder, offset, controller.signal);
      if (controller.signal.aborted) return;
      setListing((current) => offset && current?.path === result.path
        ? { ...result, entries: [...new Map([...current.entries, ...result.entries].map((entry) => [entry.path, entry])).values()] }
        : result);
    } catch (error) {
      if (controller.signal.aborted) return;
      if (folder === initialTargetRef.current && error.code === "not_directory") {
        // Preview the target directly: its entry may be beyond the first list page.
        initialTargetRef.current = null;
        const separator = folder.lastIndexOf("/");
        setPath(separator < 0 ? "" : folder.slice(0, separator));
        setSelected({ path: folder, name: folder.slice(separator + 1), type: "file" });
      } else setListError(error.message);
    } finally {
      if (!controller.signal.aborted) setLoading(false);
    }
  }

  useEffect(() => {
    loadDirectory(path);
    return () => listRequest.current?.abort();
  }, [path, revision]);

  useEffect(() => {
    if (!selected) return;
    const controller = new AbortController();
    setPreview(null);
    setPreviewError("");
    setImageError(false);
    setPreviewLoading(true);
    previewHeadingRef.current?.focus();
    previewWorkspaceFile(selected.path, controller.signal).then((result) => {
      if (!controller.signal.aborted) setPreview(result);
    }).catch((error) => {
      if (!controller.signal.aborted) setPreviewError(error.message);
    }).finally(() => {
      if (!controller.signal.aborted) setPreviewLoading(false);
    });
    return () => controller.abort();
  }, [selected, revision]);

  function navigate(nextPath) {
    initialTargetRef.current = null;
    setSelected(null);
    if (nextPath === path) return;
    setListing(null);
    setPath(nextPath);
  }

  function backToFiles() {
    setSelected(null);
    window.requestAnimationFrame(() => {
      if (selectedTriggerRef.current?.isConnected) selectedTriggerRef.current.focus();
      else closeRef.current?.focus();
    });
  }

  const selectedSize = preview?.path === selected?.path ? preview?.size : selected?.size;

  return (
    <dialog ref={dialogRef} className="workspace-dialog" aria-labelledby="workspace-title" aria-describedby="workspace-description" onClose={onClose}>
      <div className="workspace-browser">
        <header className="workspace-heading">
          <div><h2 id="workspace-title"><FolderOpen size={23} aria-hidden="true" />Workspace</h2><p id="workspace-description">Files shared by Agent sessions · Read-only</p></div>
          <button type="button" className="icon-button" aria-label="Close workspace" ref={closeRef} onClick={() => dialogRef.current.close()}><X size={23} /></button>
        </header>
        <div className="workspace-toolbar">
          <nav aria-label="Workspace folders" className="workspace-breadcrumbs">
            {workspaceBreadcrumbs(path).map((part, index, parts) => <span key={part.path}>
              {index > 0 && <span aria-hidden="true">/</span>}
              <button type="button" aria-current={index === parts.length - 1 ? "page" : undefined} onClick={() => navigate(part.path)}>{part.name}</button>
            </span>)}
          </nav>
          <button type="button" className="icon-button" aria-label="Refresh workspace" title="Refresh files and preview" disabled={loading || previewLoading} onClick={() => setRevision((value) => value + 1)}><ArrowClockwise size={21} /></button>
        </div>
        <div className={`workspace-body ${selected ? "has-selection" : ""}`}>
          <section className="workspace-list-pane" aria-label="Workspace files" aria-busy={loading}>
            {listError && <div className="workspace-feedback" role="alert"><p>{listError}</p><button type="button" onClick={() => loadDirectory(path)}>Try again</button></div>}
            {loading && <p className="workspace-feedback" role="status">Loading files…</p>}
            {listing && <ul className="workspace-file-list">
              {listing.entries.map((entry) => {
                const readable = entry.type === "file" || entry.type === "directory";
                const EntryIcon = entry.type === "directory" ? Folder : entry.type === "symlink" ? LinkSimple : File;
                return <li key={entry.path}>
                  <button type="button" className={`workspace-entry ${selected?.path === entry.path ? "selected" : ""}`} disabled={!readable || loading}
                    title={readable ? entry.name : "Links and special files cannot be opened"}
                    aria-label={`${entry.type === "directory" ? "Open folder" : "Preview file"}: ${entry.name}`}
                    aria-current={selected?.path === entry.path ? "true" : undefined}
                    onClick={(event) => {
                      if (entry.type === "directory") navigate(entry.path);
                      else { selectedTriggerRef.current = event.currentTarget; setSelected(entry); }
                    }}>
                    <EntryIcon size={21} aria-hidden="true" />
                    <span className="workspace-entry-name">{entry.name}</span>
                    <span className="workspace-entry-size">{entry.type === "file" ? formatFileSize(entry.size) : entry.type === "directory" ? "Folder" : entry.type === "symlink" ? "Link" : "Special"}</span>
                  </button>
                </li>;
              })}
            </ul>}
            {listing?.entries.length === 0 && !loading && <p className="workspace-feedback">This folder is empty.</p>}
            {listing?.nextOffset != null && <button type="button" className="workspace-more" disabled={loading} onClick={() => loadDirectory(path, listing.nextOffset)}>Load more files</button>}
          </section>
          <section className="workspace-preview" aria-label="File preview" aria-busy={previewLoading && !!selected}>
            {!selected ? <div className="workspace-preview-empty"><File size={36} aria-hidden="true" /><p>Select a file to preview it.</p><span>Text and images open here. Other files can be downloaded.</span></div> : <>
              <header className="workspace-preview-heading">
                <button type="button" className="icon-button workspace-files-back" aria-label="Back to files" onClick={backToFiles}><ArrowLeft size={20} /></button>
                <div><h3 ref={previewHeadingRef} tabIndex={-1}>{selected.name}</h3><span>{Number.isFinite(selectedSize) ? formatFileSize(selectedSize) : ""}</span></div>
                {!previewLoading && preview?.path === selected.path && preview.kind === "text" && <CopyPreview key={`${selected.path}:${revision}`} preview={preview} />}
                <a className="workspace-download" href={workspaceFileURL(selected.path)} download aria-label={`Download ${selected.name}`}><DownloadSimple size={19} aria-hidden="true" /><span>Download</span></a>
              </header>
              {previewLoading && <p className="workspace-feedback" role="status">Loading preview…</p>}
              {previewError && <p className="workspace-feedback error" role="alert">{previewError}</p>}
              {preview?.path === selected.path && preview.kind === "text" && <>
                {preview.truncated && <p className="workspace-preview-note">Showing the first 256 KB. Download for the full file.</p>}
                <pre className="workspace-text-preview" tabIndex={0} aria-label={`Contents of ${selected.name}`}><code>{preview.text}</code></pre>
                {!preview.text && <p className="workspace-feedback">This file is empty.</p>}
              </>}
              {preview?.path === selected.path && preview.kind === "image" && (imageError ? <p className="workspace-feedback error" role="alert">Could not display this image. Refresh or download the file.</p> : <div className="workspace-image-preview"><img key={`${selected.path}:${revision}`} src={`${workspaceFileURL(selected.path, false)}&v=${revision}`} alt={selected.name} onError={() => setImageError(true)} /></div>)}
              {preview?.path === selected.path && preview.kind === "unsupported" && <p className="workspace-feedback">Preview isn’t available for this file type. Download it to open it.</p>}
            </>}
          </section>
        </div>
      </div>
    </dialog>
  );
}
