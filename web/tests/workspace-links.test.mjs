import test from 'node:test';
import assert from 'node:assert/strict';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import MarkdownContent from '../src/markdown-content.js';
import { WorkspaceLinkContext, resolveWorkspacePath } from '../src/workspace-links.js';
const root = '/srv/workspace';
const render = (source) => renderToStaticMarkup(createElement(WorkspaceLinkContext.Provider,
  { value: { root, open() {} } }, createElement(MarkdownContent, null, source)));

test('normalizes only workspace paths and rejects traversal, schemes and root-prefix collisions', () => {
  for (const [input, expected] of [['/srv/workspace/a.txt', 'a.txt'], ['/srv/workspace/资料/结果.csv', '资料/结果.csv'], ['/srv/workspace/reports/a b.txt', 'reports/a b.txt'], ['/srv/workspace', ''], ['/srv/workspace/README.md', 'README.md']]) {
    assert.equal(resolveWorkspacePath(input, root), expected);
  }
  for (const input of ['and/or', '1/2', 'reports/a.txt', './reports/a.txt', 'README.md', '/srv/workspace/../secret', '/etc/passwd', '/srv/workspace-other/a', '../secret', './a/../../secret', '//evil/a', 'https://example.com/a', 'javascript:alert(1)', 'file:///srv/workspace/a', './a\\b', './a\0b', './a?x', './a#x']) {
    assert.equal(resolveWorkspacePath(input, root), null, input);
  }
  assert.equal(resolveWorkspacePath('./a.txt', undefined), null);
});

test('links prose paths, code spans and explicit local links preserving visible path and punctuation', () => {
  const html = render('结果：/srv/workspace/资料/结果.csv。 Read /srv/workspace/report.txt, then `/srv/workspace/reports/my file.txt` or [报告](</srv/workspace/资料/my file.txt>).');
  assert.equal((html.match(/class="workspace-path-link"/g) || []).length, 4);
  assert.match(html, />\/srv\/workspace\/资料\/结果.csv<\/a>。/);
  assert.match(html, />\/srv\/workspace\/report.txt<\/a>,/);
  assert.match(html, /<code>\/srv\/workspace\/reports\/my file.txt<\/code>/);
  assert.match(html, />报告<\/a>/);
  assert.match(html, /data-workspace-path="资料\/my file.txt"/);
});

test('preserves code blocks, external links, images, plain language and raw HTML safety', () => {
  const source = 'and/or 1/2 example.com\n\n```sh\ncat /srv/workspace/a.txt\n```\n\n[external /srv/workspace/a](https://example.com/a)\n\n![image](./image.png)\n\n<script>alert(1)</script>\n\n[bad](javascript:alert%281%29)';
  const html = render(source);
  assert.doesNotMatch(html, /workspace-path-link|<script|href="javascript:/);
  assert.match(html, /cat \/srv\/workspace\/a.txt\n/);
  assert.match(html, /href="https:\/\/example.com\/a"/);
  assert.match(html, /aria-label="Copy code"/);
  assert.match(html, /aria-label="Select code"/);
});

test('reference links resolve once, encoded traversal stays blocked, streamed fragments do not throw', () => {
  const html = render('[file][ref]\n\n[ref]: /srv/workspace/reports/a.txt\n\n[bad](/srv/workspace/%2e%2e/secret)');
  assert.equal((html.match(/workspace-path-link/g) || []).length, 1);
  assert.match(html, /data-workspace-path="reports\/a.txt"/);
  for (const value of ['`./reports/', '[file](./reports/', '/srv/workspace/', '```\n./a']) assert.doesNotThrow(() => render(value));
});

test('never promotes relative paths or slash-separated text, including code spans and link targets', () => {
  const html = render('and/or ./reports/a.txt reports/a.txt\n\n`and/or` `1/2` `reports/a.txt` `./reports/a.txt` `README.md`\n\n[relative](./reports/a.txt) [reference][local]\n\n[local]: reports/a.txt');
  assert.doesNotMatch(html, /workspace-path-link|data-workspace-path/);
  assert.match(html, /<code>and\/or<\/code>/);
  assert.match(html, /href="\.\/reports\/a.txt"/);
  assert.match(html, /href="reports\/a.txt"/);
});

test('workspace prefix follows configuration and respects directory boundaries', () => {
  assert.equal(resolveWorkspacePath('/custom/root/a.txt', '/custom/root/'), 'a.txt');
  assert.equal(resolveWorkspacePath('/custom/root', '/custom/root/'), '');
  assert.equal(resolveWorkspacePath('/workspace/a.txt', '/custom/root'), null);
  assert.equal(resolveWorkspacePath('/custom/root-other/a.txt', '/custom/root'), null);
});
