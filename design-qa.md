# Design QA — Ollama WebUI prototype

## Evidence

- Source visual truth: `docs/design/ollama-webui-option-1.png`
- Final implementation screenshot: `docs/design/implementation-desktop-final.png`
- Full comparison: `docs/design/compare-desktop-final.png`
- Focused chat comparison: `docs/design/compare-chat-focus.png`
- Focused settings comparison: `docs/design/compare-settings-focus.png`
- Responsive evidence: `docs/design/implementation-1024.png`,
  `docs/design/implementation-768.png`, `docs/design/implementation-mobile-375.png`,
  and `docs/design/implementation-mobile-landscape.png`
- Reference pixels: 1487 × 1058 RGB PNG.
- Implementation pixels: 1487 × 1058 RGB PNG.
- CSS viewport: 1487 × 1058 at device scale factor 1.
- Density normalization: none required; source and implementation use the same
  pixel dimensions and crop.
- Compared state: light theme, conversation selected, `qwen3:8b` selected,
  generation active, model-settings panel open, parameters inherited by default.

## Findings

No actionable P0, P1, or P2 differences remain.

- Fonts and typography: the implementation uses a local system humanist-sans
  stack and system monospace stack. Size, weight, wrapping, hierarchy, and code
  density closely match the raster target. A minor P3 difference remains because
  the exact font behind the generated mockup is unknowable and intentionally was
  not added as a network dependency.
- Spacing and layout rhythm: at the source viewport, the grid resolves to exactly
  330px / 867px / 290px. Header height, 666px reading column, full-width composer,
  settings spacing, dividers, and code block landmarks align with the target.
- Colors and visual tokens: off-white surfaces, neutral borders, blue focus/action,
  green connected state, and orange Stop action match the source. Semantic CSS
  tokens are used consistently.
- Image quality and asset fidelity: the target contains no photos, illustrations,
  avatars, or custom brand graphics. Phosphor supplies all interface icons with a
  consistent outline treatment; no emoji, placeholder imagery, handcrafted SVG,
  or CSS illustration substitutes are used.
- Copy and content: conversation, model, search, settings, code, context, and reset
  copy are coherent and match the selected design's purpose. The implementation
  deliberately adds an explicit `Model default` checkbox for Temperature. This is
  an accepted product clarification, because an active slider in the source would
  falsely imply an Ollama override.
- Interaction and accessibility: visible controls work; icon-only buttons have
  accessible names; drawers close with Escape/backdrop/X, trap keyboard focus, and
  return focus to their trigger; reduced motion collapses transitions; mobile form
  controls use 16px text; touch controls meet the 44px target.
- Responsiveness: no page-level horizontal or vertical overflow was measured at
  1024 × 768, 768 × 900, 375 × 844, or 844 × 375. At 900px and below, both side
  panes become accessible overlay drawers so chat remains the primary surface.
- Runtime: browser console contains only Vite/React development notices and no
  application errors.

## Focused comparison evidence

- Chat crop: message column origin, user-card width, assistant-label baseline,
  three-line response introduction, 34-line code block, copy affordance, and final
  response wrap were compared together at identical crop dimensions.
- Settings crop: selector height, labels, textarea height, dividers, helper text,
  Temperature group, and reset placement were compared together. The disabled
  inherited slider is an intentional semantic improvement described above.

## Comparison history

1. Initial pass
   - P2: left/center proportions drifted from the source and the code block was too
     short, plain, and internally scrolled.
   - Fix: matched the 330 / flexible / 290 grid; added focused Python highlighting,
     line numbers, and source-matched code height.
2. Second pass
   - P2: the exact 768px breakpoint retained the conversation rail, and an implicit
     grid minimum expanded the page beyond the viewport.
   - Fix: moved the responsive rail to a drawer and constrained the shell to a
     `minmax(0, 1fr)` row with clipped root overflow.
3. Third pass
   - P2: paragraph measures did not reproduce the source's reading rhythm; the code
     began too high.
   - Fix: constrained introductory prose independently from the wider code and
     composer surfaces, producing the source's line wrapping and vertical landmarks.
4. Final pass (`compare-desktop-final.png` plus focused comparisons)
   - No actionable P0/P1/P2 findings. Remaining exact-font variance and the explicit
     inherited-parameter control are accepted P3/intentional differences.

## Primary interactions tested

- Conversation search/filter and empty-search recovery.
- New chat and composer focus.
- Header/settings model selector synchronization.
- Stop to Send state transition.
- Code copy with transient Copied feedback.
- Conversation and settings drawers, backdrop/X/Escape dismissal, focus trap, and
  focus return.
- Reduced-motion media preference.
- Desktop, tablet, phone portrait, and phone landscape layouts.

## Follow-up polish

- P3: revisit the exact font only if a real llama.cpp design token or font source
  becomes available; do not infer it from the generated raster.
- Loading, disconnected, and request-error states should be designed when the Go
  and Ollama data flows are implemented, because this selected mock only defines
  the connected streaming state.

## Scoped update — pre-load context window

- User requirement: choose the context window before the first message loads the
  selected Ollama model.
- Desktop evidence: `docs/design/implementation-new-chat-context.png` at
  1440 × 1024.
- Mobile evidence: `docs/design/implementation-new-chat-context-mobile.png` at
  375 × 844.
- Data-flow evidence: the new-chat control and settings control are synchronized;
  selecting 32,768 updates the composer to `0 / 32,768 tokens`; sending the first
  message locks the control and updates usage to `2,048 / 32,768 tokens (6%)`.
- Default behavior: `Ollama default` remains the initial value and is described as
  sending no override.
- Accessibility/responsiveness: both controls have visible labels, 44px+ heights,
  16px mobile form text, keyboard-native selects, and no viewport overflow at
  1440 × 1024 or 375 × 844.
- Result: no actionable P0/P1/P2 issues. The empty-chat setup uses the selected
  prototype's existing type, spacing, border, surface, icon, and color tokens.

final result: passed
