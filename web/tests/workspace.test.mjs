import assert from "node:assert/strict";
import test from "node:test";
import { listWorkspace, previewWorkspaceFile } from "../src/api.js";
import { workspaceFileURL, workspaceBreadcrumbs } from "../src/workspace.js";

test("workspace paths with Unicode and URL punctuation remain exact for browsing, previews and downloads", async (t) => {
  const path = "reports/中文 #1 & final?.txt";
  const controller = new AbortController();
  const calls = [];
  t.mock.method(globalThis, "fetch", async (input, options) => {
    const url = new URL(input, "http://webui.local");
    assert.equal(url.searchParams.get("path"), path);
    assert.equal(options.signal, controller.signal);
    calls.push(url);
    return Response.json({ path, entries: [], nextOffset: null });
  });
  await listWorkspace(path, 500, controller.signal);
  await previewWorkspaceFile(path, controller.signal);
  assert.equal(calls[0].searchParams.get("offset"), "500");
  assert.equal(calls[1].pathname, "/api/workspace/preview");
  for (const download of [true, false]) {
    const url = new URL(workspaceFileURL(path, download), "http://webui.local");
    assert.equal(url.searchParams.get("path"), path);
    assert.equal(url.searchParams.get("download"), download ? "1" : "0");
    assert.equal(url.hash, "");
  }
});

test("workspace breadcrumbs keep root and exact parent paths", () => {
  assert.deepEqual(workspaceBreadcrumbs(""), [{ name: "Workspace", path: "" }]);
  assert.deepEqual(workspaceBreadcrumbs("notes/中文"), [{ name: "Workspace", path: "" }, { name: "notes", path: "notes" }, { name: "中文", path: "notes/中文" }]);
});

test("workspace errors expose a readable reason for retry feedback", async (t) => {
  t.mock.method(globalThis, "fetch", async () => Response.json({ error: { message: "Workspace item no longer exists." } }, { status: 404 }));
  await assert.rejects(previewWorkspaceFile("removed.txt"), (error) => error.status === 404 && error.message === "Workspace item no longer exists.");
});

test("workspace navigation distinguishes a file target from other directory errors", async (t) => {
  for (const [status, code] of [[400, "not_directory"], [400, "invalid_path"], [403, "symlink_not_supported"], [404, "workspace_not_found"]]) {
    const fetch = t.mock.method(globalThis, "fetch", async () => Response.json({ error: { code, message: "Workspace lookup failed." } }, { status }));
    await assert.rejects(listWorkspace("reports/result.txt"), (error) => error.status === status && error.code === code && error.message === "Workspace lookup failed.");
    fetch.mock.restore();
  }
});

test("workspace mutations preserve exact paths, upload bytes and conflict errors", async (t) => {
  const { createWorkspaceFolder, moveWorkspaceItem, uploadWorkspaceFile } = await import("../src/api.js");
  const calls = [];
  const bytes = new Blob([new Uint8Array([0, 255, 13, 10])]);
  t.mock.method(globalThis, "fetch", async (url, options) => {
    calls.push({ url, options });
    return Response.json({ path: "中文 #?.bin" });
  });
  await createWorkspaceFolder("中文 #?");
  await moveWorkspaceItem("中文 #?.bin", "中文 #?/中文 #?.bin");
  await uploadWorkspaceFile("中文 #?.bin", bytes);
  assert.deepEqual(JSON.parse(calls[0].options.body), { path: "中文 #?" });
  assert.deepEqual(JSON.parse(calls[1].options.body), { path: "中文 #?.bin", destination: "中文 #?/中文 #?.bin" });
  assert.equal(new URL(calls[2].url, "http://local").searchParams.get("path"), "中文 #?.bin");
  assert.equal(calls[2].options.body, bytes);
  assert.ok(calls.every(({ options }) => options.method === "POST"));
  t.mock.method(globalThis, "fetch", async () => Response.json({ error: { code: "workspace_conflict", message: "An item already exists." } }, { status: 409 }));
  await assert.rejects(moveWorkspaceItem("a", "b"), (error) => error.status === 409 && error.message === "An item already exists.");
});

test("workspace deletion preserves the exact path and handles an empty success response", async (t) => {
  const { deleteWorkspaceItem } = await import("../src/api.js");
  const path = "资料/中文 #1 & final?.txt";
  const mock = t.mock.method(globalThis, "fetch", async (url, options) => {
    const target = new URL(url, "http://local");
    assert.equal(target.pathname, "/api/workspace/file");
    assert.equal(target.searchParams.get("path"), path);
    assert.equal(options.method, "DELETE");
    return new Response(null, { status: 204 });
  });
  assert.equal(await deleteWorkspaceItem(path), null);
  mock.mock.mockImplementation(async () => Response.json({ error: { code: "workspace_not_found", message: "Workspace item no longer exists." } }, { status: 404 }));
  await assert.rejects(deleteWorkspaceItem(path), (error) => error.status === 404 && error.code === "workspace_not_found");
});
