// The empty states (src/empty-art.tsx) and the machine's screen (src/visuals.tsx). Run with `go tool task test-ui`.
import test from "node:test";
import assert from "node:assert/strict";

import { screens } from "./screens.mjs";

const { s, h } = await screens();

test("each empty state draws its drawing of the family above its sentence, the drawing hidden from readers, the pick kept for the session", () => {
  const arts = {
    calm: "empty-art-column",
    still: "empty-art-column",
    home: "empty-art-home",
    tasks: "empty-art-wide",
    decisions: "empty-art-wide",
    review: "empty-art-wide",
    agents: "empty-art-wide",
    tilasms: "empty-art-wide",
    nomatch: "empty-art-small",
    comments: "empty-art-note",
    terminal: "empty-art-terminal",
    noproject: "empty-art-terminal",
    clean: "empty-art-wide",
    noworktree: "empty-art-wide",
  };
  for (const [art, size] of Object.entries(arts)) {
    const html = s.renderToStaticMarkup(h(s.EmptyState, { art }, "The fact."));
    assert.match(
      html,
      new RegExp(
        `^<div class="empty-state"><svg class="empty-art ${size}" viewBox="[^"]+" fill="none" aria-hidden="true">.+</svg><p class="empty-state-text">The fact\\.</p></div>$`,
      ),
      art,
    );
    // No id to collide with another copy of the same drawing on the page.
    assert.doesNotMatch(html, / id=/, art);
  }
  // Small: beside its sentence, for a narrow place; after it, a button or a second line.
  assert.match(
    s.renderToStaticMarkup(
      h(
        s.EmptyState,
        { art: "nomatch", small: true, after: h("button", null, "Go") },
        "None.",
      ),
    ),
    /^<div class="empty-state small"><svg[^>]*>.+<\/svg><p class="empty-state-text">None\.<\/p><button>Go<\/button><\/div>$/,
  );
  // The variant is drawn once: the same at every call, whatever n asks.
  assert.equal(s.sessionPick(5), s.sessionPick(5));
  assert.equal(
    s.renderToStaticMarkup(h(s.QuestionsEmpty)),
    s.renderToStaticMarkup(h(s.QuestionsEmpty)),
  );
});

test("the machine's screen shows the seal's flame, and smoke leaves its top only while workers run", () => {
  const active = s.renderToStaticMarkup(h(s.Machine, { active: true }));
  assert.match(
    active,
    /<g class="screen-symbol"><path d="M228 192C[^"]+" style="fill:var\(--eye-flame\)"><\/path>/,
  );
  assert.match(active, /class="machine-smoke"/);
  const rest = s.renderToStaticMarkup(h(s.Machine, { active: false }));
  assert.match(rest, /class="screen-symbol"/);
  assert.doesNotMatch(rest, /machine-smoke/);
});
