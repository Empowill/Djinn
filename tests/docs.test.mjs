// The Grimoire (src/docs.tsx): the documentation's page. Run with `go tool task test-ui`.
import test from "node:test";
import assert from "node:assert/strict";

import { resources, screens } from "./screens.mjs";

const { s, h } = await screens();
const { hourglassDjinn } = resources(s);

// The Grimoire is a page of the window, as the Hourglass (W165): its header, then the site in a frame that fills the
// page, in the window's theme and language, under the window's header rather than its own; never a dialog.
test("the Grimoire is a page of the window, its site in a frame, no dialog", (t) => {
  // The page the window has: its theme and its text size, which the frame's address carries.
  const page = globalThis.document;
  globalThis.document = {
    documentElement: { dataset: { theme: "light", textSize: "compact" } },
  };
  t.after(() => (globalThis.document = page));
  const { djinn } = hourglassDjinn();
  const html = s.renderToStaticMarkup(h(s.DjinnProvider, { djinn }, h(s.Docs)));
  assert.equal(s.GRIMOIRE, "grimoire");
  assert.match(
    html,
    /^<div class="wish-view grimoire"><header class="topbar page-header"><div class="breadcrumbs"><strong>Grimoire<\/strong><\/div>/,
  );
  assert.match(
    html,
    /<iframe class="grimoire-frame" title="Grimoire" src="\/docs\/\?theme=light&amp;lang=en&amp;embedded=1&amp;text=compact"><\/iframe><\/div>$/,
  );
  assert.doesNotMatch(html, /role="dialog"|modal|aria-modal/);
});
