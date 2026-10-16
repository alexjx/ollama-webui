import assert from 'node:assert/strict';
import test from 'node:test';
import { templateInput } from '../src/templates-editor.js';

const draft = (settings) => ({ name: ' Test ', description: '', message: 'Hello', system_prompt: '', settings });
test('numeric editor text is converted only at save without changing the draft', () => {
  const value = draft({ temperature: '0.65', context_window: '8192' });
  assert.deepEqual(templateInput(value).settings, { temperature: .65, context_window: 8192 });
  assert.equal(value.settings.temperature, '0.65');
  assert.equal(templateInput(draft({ temperature: '0.' })).settings.temperature, 0);
});
test('clearing a custom number cannot silently save zero', () => {
  for (const value of ['', ' ', 'invalid', 'Infinity']) {
    assert.throws(() => templateInput(draft({ temperature: value })), /needs a number/);
  }
  assert.throws(() => templateInput(draft({ context_window: '1.5' })), /positive whole number/);
  assert.throws(() => templateInput(draft({ context_window: '0' })), /positive whole number/);
  assert.throws(() => templateInput(draft({ temperature: '2.1' })), /between 0 and 2/);
});
test('explicit defaults and inherited settings remain distinct', () => {
  assert.equal(templateInput(draft(null)).settings, null);
  assert.deepEqual(templateInput(draft({ temperature: null })).settings, { temperature: null });
  assert.deepEqual(templateInput(draft({})).settings, {});
});
