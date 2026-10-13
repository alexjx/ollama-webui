export function clampInputHeight(height, viewportHeight) {
  const maximum = viewportHeight / 2;
  return Math.min(maximum, Math.max(Math.min(48, maximum), height));
}
