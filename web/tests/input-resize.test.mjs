import test from "node:test";
import assert from "node:assert/strict";
import { clampInputHeight } from "../src/input-resize.js";

test("message input respects its minimum and half-viewport ceiling", () => {
  assert.equal(clampInputHeight(12, 900), 48);
  assert.equal(clampInputHeight(240, 900), 240);
  assert.equal(clampInputHeight(2000, 900), 450);
  assert.equal(clampInputHeight(450, 360), 180);
});

test("very short windows keep the minimum within the half-viewport limit", () => {
  assert.equal(clampInputHeight(48, 80), 40);
  assert.equal(clampInputHeight(10, 80), 40);
});
