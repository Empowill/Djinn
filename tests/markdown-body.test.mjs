// Markdown (src/markdown-body.tsx), its djinn:// links and its svg, and the For agents blocks (src/agent-blocks.tsx).
// Run with `go tool task test-ui`.
import test from "node:test";
import assert from "node:assert/strict";

import { screens, tilasm, wish } from "./screens.mjs";

const { s, h } = await screens();

test("the For agents tab folds each block under its title and kind, with no mark", () => {
  const html = s.renderToStaticMarkup(
    h(s.AgentBlocks, {
      blocks: [
        {
          id: "b1",
          wishId: "w",
          taskId: "t1",
          kind: "report",
          title: "Where the oil is",
          content: "In **the cellar**.",
        },
        { id: "b2", wishId: "w", kind: "hand-off", title: "", content: "x" },
      ],
      codes: new Map([["t1", "W1"]]),
    }),
  );
  assert.match(html, /role="tabpanel" aria-labelledby="view-tab-agents"/);
  assert.match(html, /Nothing here waits for you: what does is a question\./);
  assert.match(
    html,
    /id="block-b1"><button class="fold-heading" aria-expanded="false">.*<h3>Where the oil is<\/h3><span class="eyebrow">report · About W1<\/span><\/button><\/article>/,
  );
  assert.match(
    html,
    /<h3>\(untitled\)<\/h3><span class="eyebrow">hand-off<\/span>/,
  );
  // Folded: the body is not rendered until opened (the e2e opens one).
  assert.doesNotMatch(html, /the cellar/);
  assert.doesNotMatch(html, /Mark read|Approve as it is|mark-toggle/);
  const none = s.renderToStaticMarkup(
    h(s.AgentBlocks, { blocks: [], codes: new Map() }),
  );
  assert.match(
    none,
    /<div class="empty-state"><svg class="empty-art empty-art-wide"[^>]*>.*<\/svg><p class="empty-state-text">No block for agents yet: what they hand each other is written here\.<\/p><\/div>/,
  );
});

test("a djinn:// link in Markdown stays a link that opens what it names in place: a tilasm in its wish's Tilasms tab", async () => {
  const wishId = "01a11833-a440-7479-a067-52615c91da71";
  const id = "01a1223a-ae45-728f-8c37-c005eee91edb";
  // In a block, a question, a decision or the brief: Markdown keeps the link, which does not leave the page.
  const html = s.renderToStaticMarkup(
    h(s.MarkdownBody, {
      text: `See [L01](djinn://tilasm/${id}), [the wish](djinn://wish/${wishId}) and [the docs](https://example.com).`,
    }),
  );
  assert.match(
    html,
    new RegExp(`<a href="djinn://tilasm/${id}" class="djinn-link">L01</a>`),
  );
  assert.match(
    html,
    new RegExp(
      `<a href="djinn://wish/${wishId}" class="djinn-link">the wish</a>`,
    ),
  );
  assert.match(html, /<a href="https:\/\/example.com" target="_blank"/);

  // Read as djinn open reads them.
  assert.deepEqual(
    s.parseDjinnLink(`DJINN://Talisman/${id.toUpperCase()}/?x#y`),
    {
      kind: "tilasm",
      id,
    },
  );
  for (const unknown of [
    "djinn://moon/" + id,
    "djinn://tilasm/L01",
    `djinn://tilasm/${id}/more`,
    `https://tilasm/${id}`,
  ])
    assert.equal(s.parseDjinnLink(unknown), undefined, unknown);

  // A click: the tilasm's wish on its Tilasms tab, the wish, or the window says it does not know the link.
  const transport = s.createRouterTransport(({ service }) => {
    service(s.TilasmService, {
      get: (req) => {
        if (req.tilasm?.ref.value !== id)
          throw new s.ConnectError("no tilasm", s.Code.NotFound);
        return { tilasm: tilasm(id, "L01", "Model", { wishId }) };
      },
    });
  });
  const shown = [];
  const djinn = {
    clients: s.createDjinn(transport, 10).clients,
    focus: { show: (focus) => shown.push(focus) },
  };
  const gone = "djinn://tilasm/01a1223a-ae45-728f-8c37-000000000000";
  for (const link of [
    `djinn://tilasm/${id}`,
    `djinn://wish/${wishId}`,
    gone,
    "djinn://moon/x",
  ])
    await s.openDjinnLink(djinn, link);
  assert.deepEqual(shown, [
    { wishId, tilasmId: id },
    { wishId },
    { unknownLink: gone },
    { unknownLink: "djinn://moon/x" },
  ]);

  // The wish's view, asked to open the tilasm: its Tilasms tab, the tilasm in the frame.
  const lamp = wish(wishId, "Ship the lamp", s.WishState.ACTIVE, 1);
  const store = s.createDjinn(
    s.createRouterTransport(({ service }) => {
      service(s.WishService, { list: () => ({ wishes: [lamp] }) });
      service(s.TaskService, { list: () => ({ tasks: [] }) });
      service(s.QuestionService, { list: () => ({ questions: [] }) });
      service(s.BlockService, { list: () => ({ blocks: [] }) });
      service(s.TilasmService, {
        list: () => ({
          tilasms: [
            tilasm("01a1223a-ae45-728f-8c37-c005eee91edc", "L02", "Flows", {
              wishId,
            }),
            tilasm(id, "L01", "Model", { wishId }),
          ],
        }),
      });
    }),
    10,
  );
  const close = store.store.open(wishId);
  await store.store.changed(wishId, [s.Change.TILASM]);
  close();
  const view = s.renderToStaticMarkup(
    h(
      s.DjinnProvider,
      { djinn: store },
      h(s.WishView, {
        wish: lamp,
        opening: { wishId, tilasmId: id },
        onToast() {},
      }),
    ),
  );
  assert.match(view, /id="view-tab-tilasms" aria-selected="true"/);
  assert.match(view, new RegExp(`<iframe[^>]*src="/tilasm/${id}/"`));
  assert.match(view, /<section class="tilasm-view" aria-label="Model">/);
});

test("a fenced svg or standalone svg block renders safely in Markdown and AgentBlocks", () => {
  // 1. MarkdownBody with fenced ```svg
  const mdHtml = s.renderToStaticMarkup(
    h(s.MarkdownBody, {
      text: 'Diagram:\n\n```svg\n<svg viewBox="0 0 100 100"><circle cx="50" cy="50" r="40" /><script>alert(\'bad\')</script></svg>\n```',
    }),
  );
  assert.match(mdHtml, /<div class="dia-frame">/);
  assert.match(mdHtml, /<circle cx="50" cy="50" r="40"\s*\/?>/);
  assert.doesNotMatch(mdHtml, /<script/);
  assert.doesNotMatch(mdHtml, /alert/);

  // 2. sanitizeSvg security checks
  const dirty = `<svg viewBox="0 0 100 100" onload="alert('evil')" onclick="doEvil()">
    <script>alert('pwn')</script>
    <foreignObject><iframe src="https://attacker.com"></iframe></foreignObject>
    <a href="https://attacker.com" xlink:href="javascript:alert(1)"><text>Link</text></a>
    <a href="#safe-anchor"><rect width="10" height="10" /></a>
  </svg>`;
  const clean = s.sanitizeSvg(dirty);
  assert.doesNotMatch(clean, /<script/i);
  assert.doesNotMatch(clean, /alert/i);
  assert.doesNotMatch(clean, /onload/i);
  assert.doesNotMatch(clean, /onclick/i);
  assert.doesNotMatch(clean, /foreignObject/i);
  assert.doesNotMatch(clean, /iframe/i);
  assert.doesNotMatch(clean, /attacker\.com/i);
  assert.match(clean, /href="#safe-anchor"/);
  assert.match(clean, /<rect width="10" height="10"\s*\/?>/);

  // 3. SafeSvg component rendering
  const svgHtml = s.renderToStaticMarkup(h(s.SafeSvg, { source: clean }));
  assert.match(svgHtml, /<div class="dia-frame">/);
  assert.match(svgHtml, /<rect width="10" height="10"\s*\/?>/);
});
