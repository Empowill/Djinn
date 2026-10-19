// The flying carpet (src/flight-plan.tsx): the active wishes merged, what they spent, the inbox. Run with `go tool task
// test-ui`.
import test from "node:test";
import assert from "node:assert/strict";

import { screens, usage, wish } from "./screens.mjs";

const { s, h } = await screens();

// two wishes, a question each, a worker running in the second, read from in-memory services.
function twoWishes() {
  const lamp = wish(
    "01a11833-a440-7479-a067-52615c91da71",
    "Ship the lamp",
    s.WishState.ACTIVE,
    1,
  );
  const oil = wish(
    "01a11833-a440-7479-a067-52615c91da72",
    "Find the oil",
    s.WishState.ACTIVE,
    2,
  );
  const tasks = {
    [lamp.id]: [
      {
        id: "t1",
        wishId: lamp.id,
        code: "W1",
        title: "Trim the wick",
        status: s.TaskStatus.DONE,
        usage: usage(10, 400, 38000, 12000, 0.42),
      },
    ],
    [oil.id]: [
      {
        id: "t2",
        wishId: oil.id,
        code: "W1",
        title: "Taste the oils",
        status: s.TaskStatus.RUNNING,
        model: "fixture-unpriced",
        usage: usage(1200, 300, 5000, 0, 0),
      },
      {
        id: "t3",
        wishId: oil.id,
        code: "W2",
        title: "Pour it",
        status: s.TaskStatus.WAITING,
        editQuestionId: "q2",
      },
    ],
  };
  const questions = {
    [lamp.id]: [
      {
        id: "q1",
        wishId: lamp.id,
        code: "Q01",
        text: "Brass or glass?",
        options: ["Brass", "Glass"],
      },
      {
        id: "q3",
        wishId: lamp.id,
        code: "Q02",
        text: "Tonight?",
        options: [],
        answer: { choice: s.Choice.YES, note: "" },
      },
    ],
    [oil.id]: [
      {
        id: "q2",
        wishId: oil.id,
        code: "Q01",
        text: "May W2 pour?",
        options: [],
      },
    ],
  };
  const transport = s.createRouterTransport(({ service }) => {
    service(s.WishService, { list: () => ({ wishes: [lamp, oil] }) });
    service(s.TaskService, {
      list: (req) => ({ tasks: tasks[req.wishId] ?? [] }),
    });
    service(s.QuestionService, {
      list: (req) => ({ questions: questions[req.wishId] ?? [] }),
    });
    service(s.BlockService, { list: () => ({ blocks: [] }) });
  });
  return { lamp, oil, djinn: s.createDjinn(transport, 10) };
}

test("the flying carpet merges the active wishes: their questions, the blocking one first, each with its wish", async () => {
  const { lamp, oil, djinn } = twoWishes();
  const closes = [djinn.store.open(lamp.id), djinn.store.open(oil.id)];
  await djinn.store.changed(lamp.id, [s.Change.WISH]);
  closes.forEach((close) => close());
  const html = s.renderToStaticMarkup(
    h(
      s.DjinnProvider,
      { djinn },
      h(s.FlightPlan, { wishes: [lamp, oil], onOpen() {}, onToast() {} }),
    ),
  );
  assert.match(html, /<h1>Flying carpet<\/h1>/);
  // Its live graphs are in the Hourglass (W542): none here.
  assert.doesNotMatch(html, /consumption-panel|resource-chart/);
  // W538: the Djinn is at hand as on a wish: Talk to the Djinn in the header opens its terminal, on no wish, and the
  // pill at the bottom writes to it, Cmd/Ctrl+I.
  assert.match(
    html,
    /<div class="topbar-actions"><div class="split-button"><button class="button secondary small" title="Opens the Djinn&#x27;s terminal"><svg[^]*?<span>Talk to the Djinn<\/span><\/button>/,
  );
  assert.doesNotMatch(html, /focused on this wish/);
  assert.match(
    html,
    /<div class="lead-message" data-eye-place="">[^]*?aria-keyshortcuts="[^"]*I"[^]*?<span>Write to the Djinn<\/span><\/button>/,
  );
  // The blocking question of the second wish comes before the free one of the first, each marked with its wish.
  const blocking = html.indexOf("May W2 pour?");
  const free = html.indexOf("Brass or glass?");
  assert.ok(blocking > 0 && free > blocking);
  assert.match(html, /Blocks W2/);
  assert.match(
    html,
    /class="wish-origin" title="Find the oil"><b>2<\/b>Find the oil/,
  );
  // What waits; the tasks and the decisions of both wishes in their own tabs: the done task part of no azima counts
  // with them, as in its wish's (W507).
  assert.match(html, /W2 waits for your answer to Q01 before it may edit\./);
  assert.match(
    html,
    /id="view-tab-tasks"[^>]*><span class="djinn-eye-perch" data-perch="tab:tasks" aria-hidden="true"><\/span>Tasks<span class="count">3<\/span>/,
  );
  assert.match(
    html,
    /id="view-tab-decisions"[^>]*><span class="djinn-eye-perch" data-perch="tab:decisions" aria-hidden="true"><\/span>Decisions<span class="count">1<\/span>/,
  );
  assert.doesNotMatch(html, /Tonight\?/);
  // On the right, the glance (Decision Q08): the worker that runs, and nothing else of the wishes, not what they
  // spent (each wish's page and the Tasks tab say it).
  assert.match(
    html,
    /<li class="glance-item"><button type="button" class="glance-row run" title="Running · W1 Taste the oils">/,
  );
  assert.doesNotMatch(html, /Trim the wick|spent-price/);
});

test("what a wish spent shows one price, ≈ when an estimate is in it; the hover splits it, in money only", () => {
  const spent = s.spent([
    // Claude reports its cost: it is the price, whatever the table says.
    { usage: usage(10, 400, 38000, 12000, 0.42) },
    // Codex reports tokens: the table prices them (1200 × 5 + 300 × 30 + 5000 × 0.5) / 1M = $0.0175.
    {
      provider: s.Provider.CODEX,
      model: "gpt-5.5",
      usage: usage(1200, 300, 5000, 0, 0),
    },
    // An Antigravity estimate is at API prices, not the bill.
    {
      provider: s.Provider.ANTIGRAVITY,
      model: "gemini-3.8-flash-high",
      usage: usage(1000, 200, 0, 0, 0),
    },
    // A model the table does not know: no price.
    { model: "fixture-unpriced", usage: usage(100, 200, 0, 0, 0) },
  ]);
  assert.equal(spent.costUsd, 0.42);
  assert.equal(spent.priced, 2);
  assert.equal(spent.withoutPrice, 1);
  const total = spent.costUsd + spent.estimatedCostUsd;
  const money = (n) =>
    new Intl.NumberFormat("en", {
      style: "currency",
      currency: "USD",
      maximumFractionDigits: n < 1 ? 3 : 2,
    })
      .format(n)
      .replace(/[$.]/g, "\\$&");
  const html = s.renderToStaticMarkup(h(s.SpentLine, { spent }));
  const title = html.match(
    /<span class="spent-price" title="([^"]*)">([^<]*)</,
  );
  assert.ok(title, html);
  // One figure: the reported cost plus the estimate of the tasks that reported none, read ≈.
  assert.match(title[2], new RegExp(`^≈ ${money(total)}$`));
  const split = title[1].split("\n");
  assert.equal(split[0], "$0.42 reported");
  assert.match(
    split[1],
    new RegExp(
      `^${money(spent.estimatedCostUsd)} estimated, prices of 2026-01-01 \\(an estimate at API prices, not the bill\\)$`,
    ),
  );
  assert.equal(split[2], "1 task without a price in the table");
  // Several providers: each one's price, in the hover too.
  assert.ok(split.includes("claude: $0.42"), split.join(" | "));
  assert.ok(split.includes("codex: ≈ $0.018"), split.join(" | "));
  assert.ok(split.some((line) => line.startsWith("antigravity: ≈ $")));
  // Nothing of the split shows outside the hover; the tokens read as they did.
  const shown = html.replace(/title="[^"]*"/g, "");
  assert.doesNotMatch(shown, /reported|estimated|without a price|not the bill/);
  assert.match(
    shown,
    /<span>input 2\.3K · output 1\.1K · cache read 43K · cache written 12K<\/span>/,
  );
  assert.doesNotMatch(shown, /spent-providers/);

  // In tokens, nothing changes: the total, the detail, each provider's count, and no price.
  s.setUsageUnit("tokens");
  try {
    const counted = s.renderToStaticMarkup(h(s.SpentLine, { spent }));
    assert.match(counted, /<span class="spent-total">58\.4K tokens<\/span>/);
    assert.match(counted, /class="spent-providers"/);
    assert.doesNotMatch(counted, /\$|spent-price/);
  } finally {
    s.setUsageUnit("money");
  }
});

test("the flying carpet hides its empty sections", async () => {
  const lamp = wish(
    "01a11833-a440-7479-a067-52615c91da71",
    "Ship the lamp",
    s.WishState.ACTIVE,
    1,
  );
  const transport = s.createRouterTransport(({ service }) => {
    service(s.WishService, { list: () => ({ wishes: [lamp] }) });
    service(s.TaskService, { list: () => ({ tasks: [] }) });
    service(s.QuestionService, { list: () => ({ questions: [] }) });
    service(s.BlockService, { list: () => ({ blocks: [] }) });
  });
  const djinn = s.createDjinn(transport, 10);
  const close = djinn.store.open(lamp.id);
  await djinn.store.changed(lamp.id, [s.Change.WISH]);
  close();
  const html = s.renderToStaticMarkup(
    h(
      s.DjinnProvider,
      { djinn },
      h(s.FlightPlan, { wishes: [lamp], onOpen() {}, onToast() {} }),
    ),
  );
  assert.doesNotMatch(html, /Your move|Who runs now|Spent/);
  // Said once, in the questions column, beside the Djinn asleep.
  assert.equal(
    html.match(
      /Nothing waits for you, and nothing runs: I rest above my lamp\./g,
    )?.length,
    1,
  );
  assert.match(
    html,
    /<div class="empty-state question-none"><svg class="empty-art empty-art-column"/,
  );
});

test("an inbox card says its kind and the fact, what Djinn will do, and what each choice changes under it; Dismiss is the last choice", async () => {
  const lamp = wish(
    "01a11833-a440-7479-a067-52615c91da71",
    "Ship the lamp",
    s.WishState.ACTIVE,
    1,
  );
  const item = {
    id: "01a12079-0000-7000-8000-000000000001",
    source: "babysit-mr",
    projectId: "p1",
    key: "https://gitlab.example.com/acme/gong/-/merge_requests/12",
    text: "Babysit !12 · Fix the wick\nhttps://gitlab.example.com/acme/gong/-/merge_requests/12",
    state: s.InboxState.NEW,
    route: {
      request: "Babysit !12",
      options: [
        {
          kind: s.RouteKind.NEW,
          title: "Babysit !12",
          projectIds: ["p1"],
          reason: "the skill babysit-mr makes this wish",
          template: {
            skill: "babysit-mr",
            projectId: "p1",
            watch: "mrwatch -mr 12",
          },
        },
        { kind: s.RouteKind.FILE, wishId: lamp.id, title: lamp.title },
      ],
    },
  };
  const transport = s.createRouterTransport(({ service }) => {
    service(s.WishService, { list: () => ({ wishes: [lamp] }) });
    service(s.ProjectService, {
      list: () => ({ projects: [{ id: "p1", name: "gong" }] }),
    });
    service(s.InboxService, {
      list: () => ({ items: [item] }),
      sources: () => ({
        sources: [
          {
            name: "gong/babysit-mr",
            skill: "babysit-mr",
            project: "gong",
            plugged: true,
            every: "5m0s",
          },
        ],
      }),
    });
    service(s.TaskService, { list: () => ({ tasks: [] }) });
    service(s.QuestionService, { list: () => ({ questions: [] }) });
    service(s.BlockService, { list: () => ({ blocks: [] }) });
  });
  const djinn = s.createDjinn(transport, 10);
  await djinn.store.changed("", [
    s.Change.WISH,
    s.Change.PROJECT,
    s.Change.INBOX,
  ]);
  const html = s.renderToStaticMarkup(
    h(
      s.DjinnProvider,
      { djinn },
      h(s.FlightPlan, { wishes: [lamp], onOpen() {}, onToast() {} }),
    ),
  );
  assert.match(html, /Inbox/);
  // 1. Its kind, then the fact.
  assert.match(
    html,
    /<span class="inbox-kind">Request<\/span><span class="inbox-source">read by babysit-mr on this machine<\/span>/,
  );
  assert.match(html, /<h3>Babysit !12 · Fix the wick<\/h3>/);
  // 2. What Djinn will do with the recommended destination.
  assert.match(
    html,
    /<p class="inbox-will">If you hand it to Djinn, a wish follows it with the skill babysit-mr: its watcher starts at once/,
  );
  // 3. Under each choice, what it changes; Dismiss last, with its own.
  const made = html.indexOf(
    "New wish “Babysit !12”, in gong, from the skill babysit-mr",
  );
  const filed = html.indexOf("File it in “Ship the lamp”");
  const dismissed = html.indexOf(">Dismiss<");
  assert.ok(made > 0 && filed > made && dismissed > filed);
  assert.match(
    html,
    /from the skill babysit-mr<\/p><p class="inbox-effect">Its watcher starts at once and tells the lead each change; workers, and so tokens, only when the lead starts them\.<\/p>/,
  );
  assert.match(
    html,
    /File it in “Ship the lamp”<\/p><p class="inbox-effect">Its lead receives it as a block and decides; nothing starts by itself\.<\/p>/,
  );
  assert.match(
    html,
    /<strong class="option-letter">C<\/strong><p>Dismiss<\/p><p class="inbox-effect">It will not come back here; nothing is said or marked at the source\.<\/p>/,
  );
  assert.match(html, /Rub the lamp · A/);
  // The sources are not listed in the inbox: their project's panel holds them.
  assert.doesNotMatch(html, /inbox-source-row|Plug in|Unplug/);
});

test("the inbox hides when nothing waits, however many sources the skills declare; a plugged-in source that fails gets a card", async () => {
  const lamp = wish(
    "01a11833-a440-7479-a067-52615c91da71",
    "Ship the lamp",
    s.WishState.ACTIVE,
    1,
  );
  const render = async (sources) => {
    const transport = s.createRouterTransport(({ service }) => {
      service(s.WishService, { list: () => ({ wishes: [lamp] }) });
      service(s.ProjectService, { list: () => ({ projects: [] }) });
      service(s.InboxService, {
        list: () => ({ items: [] }),
        sources: () => ({ sources }),
      });
      service(s.TaskService, { list: () => ({ tasks: [] }) });
      service(s.QuestionService, { list: () => ({ questions: [] }) });
      service(s.BlockService, { list: () => ({ blocks: [] }) });
    });
    const djinn = s.createDjinn(transport, 10);
    await djinn.store.changed("", [
      s.Change.WISH,
      s.Change.PROJECT,
      s.Change.INBOX,
    ]);
    return s.renderToStaticMarkup(
      h(
        s.DjinnProvider,
        { djinn },
        h(s.FlightPlan, { wishes: [lamp], onOpen() {}, onToast() {} }),
      ),
    );
  };
  const unplugged = {
    name: "djinn/babysit-pr",
    skill: "babysit-pr",
    project: "djinn",
    watch: "sh .agents/skills/babysit-pr/inbox.sh",
    every: "5m0s",
  };
  const fine = {
    name: "gong/mentions",
    skill: "mentions",
    project: "gong",
    watch: "mentions --new",
    every: "1h0m0s",
    plugged: true,
    lastRun: { endTime: { seconds: 1_760_000_000n, nanos: 0 }, items: 2 },
  };
  const broke = {
    ...unplugged,
    plugged: true,
    summary: "The pull requests assigned to you, read with gh.",
    lastRun: {
      endTime: { seconds: 1_760_000_600n, nanos: 0 },
      failingSince: { seconds: 1_760_000_000n, nanos: 0 },
      failures: 7,
      error:
        "exit status 4: gh: To get started with GitHub CLI, please run: gh auth login",
    },
  };

  assert.doesNotMatch(await render([]), /Inbox/);
  // Declared, unplugged or running well: nothing to decide, no inbox.
  assert.doesNotMatch(await render([unplugged, fine]), /Inbox|inbox/);

  const html = await render([broke, fine]);
  assert.match(html, /<span class="count">1<\/span>/);
  assert.equal(html.split("inbox-source-card").length - 1, 1);
  // 1. Its kind, since when and how many runs, then the fact and the error.
  assert.match(
    html,
    /<span class="inbox-kind">Djinn(?:'|&#x27;)s configuration<\/span><span class="inbox-when">since [^<]+<\/span><span class="inbox-when">7 failed runs in a row<\/span>/,
  );
  assert.match(
    html,
    /<h3>The source djinn\/babysit-pr fails on this machine<\/h3><pre class="inbox-error">exit status 4: gh: To get started/,
  );
  assert.match(
    html,
    /It reads: The pull requests assigned to you, read with gh\./,
  );
  // 2. What Djinn does meanwhile.
  assert.match(html, /Djinn runs it again every 5m on its own/);
  // 3. The two ways out: fix it, recommended, with no button; unplug it.
  assert.match(
    html,
    /<div class="option selected inbox-fix"><strong class="option-letter">A<\/strong><p>Fix the cause on this machine<\/p><p class="inbox-effect">[^<]+<\/p><span class="option-recommended">Recommended<\/span><\/div>/,
  );
  assert.match(
    html,
    /<button class="option"><strong class="option-letter">B<\/strong><p>Unplug it on this machine<\/p><p class="inbox-effect">Its command stops and this card goes/,
  );

  // A source plugged in before its skill broke cannot run: its card says why, and how to fix it.
  const cannot = await render([
    {
      name: "gong/noisy",
      skill: "noisy",
      project: "gong",
      plugged: true,
      error: "metadata.djinn.source.every: 10s is too often",
    },
  ]);
  assert.match(
    cannot,
    /<h3>The source gong\/noisy cannot run on this machine<\/h3><pre class="inbox-error">metadata.djinn.source.every: 10s is too often<\/pre>/,
  );
  assert.match(cannot, /<p>Fix its skill<\/p>/);
  assert.doesNotMatch(cannot, /failed run/);
});

test("a project with something to fix takes one line in the inbox, under the cards: its red gear, its name and count, the first thing to fix from the first paint, and the button that opens its panel", async () => {
  const lamp = wish(
    "01a11833-a440-7479-a067-52615c91da71",
    "Ship the lamp",
    s.WishState.ACTIVE,
    1,
  );
  const render = async (projects) => {
    const transport = s.createRouterTransport(({ service }) => {
      service(s.WishService, { list: () => ({ wishes: [lamp] }) });
      service(s.ProjectService, { list: () => ({ projects }) });
      service(s.InboxService, {
        list: () => ({ items: [] }),
        sources: () => ({ sources: [] }),
      });
      service(s.TaskService, { list: () => ({ tasks: [] }) });
      service(s.QuestionService, { list: () => ({ questions: [] }) });
      service(s.BlockService, { list: () => ({ blocks: [] }) });
    });
    const djinn = s.createDjinn(transport, 10);
    await djinn.store.changed("", [
      s.Change.WISH,
      s.Change.PROJECT,
      s.Change.INBOX,
    ]);
    return s.renderToStaticMarkup(
      h(
        s.DjinnProvider,
        { djinn },
        h(s.FlightPlan, {
          wishes: [lamp],
          onOpen() {},
          onOpenProject() {},
          onToast() {},
        }),
      ),
    );
  };
  // All well: no line, no inbox.
  assert.doesNotMatch(
    await render([{ id: "p1", name: "lamp", toFix: 0 }]),
    /Inbox|inbox/,
  );
  const first =
    "The skill dolmen-news offers an inbox source this machine has not decided on: plug it in for its items to come here, or unplug it to say no.";
  const html = await render([
    { id: "p1", name: "lamp", toFix: 0 },
    { id: "p2", name: "dolmen", toFix: 2, firstFix: first },
  ]);
  assert.match(html, /<span class="count">1<\/span>/);
  assert.equal(html.split('inbox-project"').length - 1, 1);
  assert.match(
    html,
    /<article class="question-card inbox-card inbox-project"><span class="inbox-project-gear" aria-hidden="true"><svg[^>]*lucide-settings[^]*?<\/svg><span class="project-flame"/,
  );
  assert.match(
    html,
    /<span class="inbox-kind">Project dolmen<\/span><span class="inbox-project-count">2 things to fix<\/span>/,
  );
  // The first thing to fix as Djinn says it, from the first paint: no sentence comes later to grow the line under the
  // reader. Then the button that opens the panel.
  assert.ok(html.includes(`<p>${first}</p>`), html);
  assert.match(
    html,
    /<button class="button small secondary">Open its configuration<\/button><\/article>/,
  );
  // A project that says none: a sentence that sends to the panel.
  assert.match(
    await render([{ id: "p2", name: "dolmen", toFix: 1 }]),
    /<p>Djinn waits for you on its configuration/,
  );
});
