// An azima's card (src/azima.tsx, src/azima-rank.tsx): its states, counters, description, proof, parts, rank. Run with
// `go tool task test-ui`.
import test from "node:test";
import assert from "node:assert/strict";

import { projectSync, screens, wish } from "./screens.mjs";

const { s, h, live } = await screens();

test("a cancelled part leaves its azima's counters and shows among its finished parts, Cancelled", () => {
  const at = (seconds) => ({ seconds: BigInt(seconds), nanos: 0 });
  const work = (code, status, end) => ({
    id: code,
    code,
    title: `Task ${code}`,
    status,
    partOf: "T1",
    dependsOn: [],
    createTime: at(1),
    endTime: at(end),
  });
  const parts = [
    work("W1", s.TaskStatus.DONE, 10),
    work("W2", s.TaskStatus.CANCELLED, 20),
  ];
  const card = (counts) =>
    s.renderToStaticMarkup(
      h(s.AzimaCard, {
        azima: {
          id: "T1",
          code: "T1",
          title: "Ship the lamp",
          kind: s.TaskKind.AZIMA,
          status: s.TaskStatus.PENDING,
          dependsOn: [],
          azima: { state: s.AzimaState.AWAITING_PROOF, ready: true, ...counts },
        },
        parts,
        tasks: new Map(parts.map((x) => [x.id, x])),
        focus: "W2",
        render: (task) => h(s.WishTask, { key: task.id, task, onStop() {} }),
      }),
    );
  // The server's counts (internal/plan.FillAzimas), then the card's own when it has none: 1/1, nothing waiting.
  for (const html of [
    card({ parts: 1, partsDone: 1, partsRunning: 0 }),
    card({}),
  ]) {
    assert.match(html, />1\/1</);
    assert.match(html, /1 done, 0 running, 0 waiting/);
    assert.match(html, /status-badge tone-cancelled[^>]*>.*?Cancelled/);
  }
  // An azima whose only part is cancelled has nothing to count: no bar, never "0/1 … 1 waiting".
  const only = s.renderToStaticMarkup(
    h(s.AzimaCard, {
      azima: {
        id: "T2",
        code: "T2",
        title: "Trim the wick",
        kind: s.TaskKind.AZIMA,
        dependsOn: [],
        azima: { state: s.AzimaState.OPEN, ready: true },
      },
      parts: [{ ...parts[1], partOf: "T2" }],
      tasks: new Map(),
      render: () => null,
    }),
  );
  assert.doesNotMatch(only, /azima-progress/);
  assert.doesNotMatch(only, /waiting/);
});

test("an azima whose only live work is a watcher reads Watching, teal with an eye, and still moves", () => {
  const at = (seconds) => ({ seconds: BigInt(seconds), nanos: 0 });
  const part = (code, status, provider) => ({
    id: code,
    code,
    title: `Task ${code}`,
    status,
    provider,
    partOf: "T1",
    dependsOn: [],
    createTime: at(1),
    startTime: at(2),
  });
  const parts = [
    part("W1", s.TaskStatus.DONE),
    part("W2", s.TaskStatus.RUNNING, s.Provider.WATCH),
  ];
  const azima = (state) => ({
    id: "T1",
    code: "T1",
    title: "Ship the lamp",
    kind: s.TaskKind.AZIMA,
    status: s.TaskStatus.PENDING,
    dependsOn: [],
    azima: { state, ready: true, parts: 2, partsDone: 1, partsRunning: 1 },
  });
  const card = (state) =>
    s.renderToStaticMarkup(
      h(s.AzimaCard, {
        azima: azima(state),
        parts,
        tasks: new Map(parts.map((x) => [x.id, x])),
        render: (task) => h(s.WishTask, { key: task.id, task, onStop() {} }),
      }),
    );
  // The server's state (internal/plan.FillAzimas): watching, never "In progress", in the watchers' teal.
  const html = card(s.AzimaState.WATCHING);
  assert.match(html, /azima-card tone-watching/);
  assert.match(html, /status-badge tone-watching[^>]*>.*?Watching/);
  assert.doesNotMatch(html, /In progress/);
  // A work part running beside it: the server says in progress, and so does the card.
  assert.match(card(s.AzimaState.IN_PROGRESS), /In progress/);
  // Among the azimas, it moves: first, with the ones in progress.
  assert.equal(s.azimaRank(azima(s.AzimaState.WATCHING)), 0);
  assert.equal(
    s.azimaRank({
      ...azima(s.AzimaState.WATCHING),
      azima: { state: s.AzimaState.WATCHING },
    }),
    0,
  );
});

test("an azima whose work is done awaits its proof: its own label and tone, what it needs, the pill apart, and the flying carpet lists what a person can give", async () => {
  const wishId = "01a11833-a440-7479-a067-52615c91da71";
  const lamp = wish(wishId, "Ship the lamp", s.WishState.ACTIVE, 1);
  const need = (box, needs, provers, reviewer = "") => ({
    box,
    needs,
    provers,
    reviewer,
  });
  const azima = (code, title, state, extra = {}) => ({
    id: code,
    wishId,
    code,
    title,
    kind: s.TaskKind.AZIMA,
    status: s.TaskStatus.PENDING,
    dependsOn: [],
    proofNeeds: [],
    azima: { state, ready: true, parts: 1, partsDone: 1 },
    ...extra,
  });
  const tasks = [
    azima("T1", "Lay the ground", s.AzimaState.DONE, {
      status: s.TaskStatus.DONE,
    }),
    azima("T3", "The interface", s.AzimaState.AWAITING_PROOF, {
      proofNeeds: [
        need(
          "Clément has reviewed the switch.",
          "Clément's review",
          [s.Prover.REVIEW],
          "Clément",
        ),
      ],
    }),
    azima("T6", "Native e2e", s.AzimaState.AWAITING_PROOF, {
      proofNeeds: [
        need("The same scenario runs on macOS.", "a Mac", [s.Prover.MAC]),
      ],
    }),
    azima("T7", "The orchestrator", s.AzimaState.IN_PROGRESS),
    {
      id: "w1",
      wishId,
      code: "W1",
      title: "Switch",
      status: s.TaskStatus.DONE,
      partOf: "T3",
    },
    {
      id: "w2",
      wishId,
      code: "W2",
      title: "Drive",
      status: s.TaskStatus.DONE,
      partOf: "T6",
    },
  ];
  const byId = new Map(tasks.map((task) => [task.id, task]));
  const card = s.renderToStaticMarkup(
    h(s.AzimaCard, {
      azima: tasks[2],
      parts: [tasks[5]],
      tasks: byId,
      render: () => null,
    }),
  );
  assert.match(card, /azima-card tone-proof/);
  assert.match(
    card,
    /<span class="status-badge tone-proof" title="Its work is done: to validate\n• The same scenario runs on macOS\. Needs a Mac\n[^"]*then validate it: it is done\.">.*<span>To validate<\/span>/,
  );
  // What it needs is on its badge's hover and in the card opened, not in its heading.
  assert.doesNotMatch(card, /azima-needs|>a Mac</);
  assert.doesNotMatch(card, /tone-waiting/);

  const transport = s.createRouterTransport(({ service }) => {
    service(s.WishService, { list: () => ({ wishes: [lamp] }) });
    service(s.TaskService, { list: () => ({ tasks }) });
    service(s.QuestionService, { list: () => ({ questions: [] }) });
    service(s.BlockService, { list: () => ({ blocks: [] }) });
  });
  const djinn = s.createDjinn(transport, 10);
  const close = djinn.store.open(wishId);
  await djinn.store.changed(wishId, [s.Change.WISH]);
  close();
  // The wish's head counts the azimas awaiting their proof apart from the done ones.
  const page = s.renderToStaticMarkup(
    h(s.DjinnProvider, { djinn }, h(s.WishView, { wish: lamp, onToast() {} })),
  );
  assert.match(
    page,
    /title="1 of 4 azimas done, 2 to validate"[^>]*>.*?<b>1<\/b>done · <b class="tone-proof">2<\/b> to validate \/ 4 azimas<\/span>/,
  );
  // The flying carpet lists the proof a person can give among what waits for them, never as work; a Mac's is not.
  const plan = s.renderToStaticMarkup(
    h(
      s.DjinnProvider,
      { djinn },
      h(s.FlightPlan, { wishes: [lamp], onOpen() {}, onToast() {} }),
    ),
  );
  const yourMove = plan.slice(plan.indexOf('id="action-center"'));
  assert.match(
    yourMove,
    /<h3>Proofs you can give<span class="count">1<\/span><\/h3>/,
  );
  assert.match(
    yourMove,
    /status-badge tone-proof" title="Needs Clément&#x27;s review"><svg[^]*?<span>Clément&#x27;s review<\/span>/,
  );
  assert.match(yourMove, /T3: Clément has reviewed the switch\./);
  assert.doesNotMatch(plan, /The same scenario runs on macOS/);
  assert.match(
    plan,
    /id="view-tab-tasks"[^>]*><span class="djinn-eye-perch" data-perch="tab:tasks" aria-hidden="true"><\/span>Tasks<span class="count">2<\/span>/,
  );
  const fp = s.flightPlan([lamp], {
    [wishId]: { tasks, questions: [], blocks: [], loaded: true },
  });
  assert.deepEqual(
    fp.azimas.map((x) => x.item.azima.code),
    ["T7", "T3", "T6", "T1"],
  );
  assert.equal(fp.moving.length + fp.waiting.length, 0);
});

test("an azima whose status is done while its state is in progress shows in progress: the card and the wish pill", async () => {
  const wishId = "01a11833-a440-7479-a067-52615c91da71";
  const lamp = wish(wishId, "Ship the lamp", s.WishState.ACTIVE, 1);
  const azimaTask = {
    id: "t1",
    wishId,
    code: "T1",
    title: "Lay the ground",
    kind: s.TaskKind.AZIMA,
    status: s.TaskStatus.DONE,
    dependsOn: [],
    proofNeeds: [],
    azima: {
      state: s.AzimaState.IN_PROGRESS,
      partsRunning: 1,
      parts: 2,
      partsDone: 1,
    },
  };
  const part1 = {
    id: "w1",
    wishId,
    code: "W1",
    title: "Foundations",
    status: s.TaskStatus.DONE,
    partOf: "t1",
  };
  const part2 = {
    id: "w2",
    wishId,
    code: "W2",
    title: "Plumbing",
    status: s.TaskStatus.RUNNING,
    partOf: "t1",
  };
  const tasks = [azimaTask, part1, part2];
  const byId = new Map(tasks.map((task) => [task.id, task]));

  // The card shows in progress with its tone and badge, never done.
  const card = s.renderToStaticMarkup(
    h(s.AzimaCard, {
      azima: azimaTask,
      parts: [part1, part2],
      tasks: byId,
      render: () => null,
    }),
  );
  assert.match(card, /azima-card tone-running/);
  assert.match(
    card,
    /<span class="status-badge tone-running">.*?<span>In progress<\/span><\/span>/,
  );
  assert.doesNotMatch(card, /tone-done/);
  assert.doesNotMatch(card, />Done<\/span>/);

  const transport = s.createRouterTransport(({ service }) => {
    service(s.WishService, { list: () => ({ wishes: [lamp] }) });
    service(s.TaskService, { list: () => ({ tasks }) });
    service(s.QuestionService, { list: () => ({ questions: [] }) });
    service(s.BlockService, { list: () => ({ blocks: [] }) });
  });
  const djinn = s.createDjinn(transport, 10);
  const close = djinn.store.open(wishId);
  await djinn.store.changed(wishId, [s.Change.WISH]);
  close();

  // The wish's head pill counts 0 done of 1 azimas, not 1 done.
  const page = s.renderToStaticMarkup(
    h(s.DjinnProvider, { djinn }, h(s.WishView, { wish: lamp, onToast() {} })),
  );
  assert.match(page, /title="0 of 1 azimas done"/);
  assert.match(page, /<b>0<\/b>\/ 1 azimas<\/span>/);
  assert.doesNotMatch(page, /title="1 of 1 azimas done"/);
  assert.doesNotMatch(page, /<b>1<\/b>\/ 1 azimas<\/span>/);
});

// An opened azima, as the Tasks tab shows it: built from what the lamp sends, its parts as the tab renders any task.
function openedAzima(extra = {}, parts = []) {
  const at = (seconds) => ({ seconds: BigInt(seconds), nanos: 0 });
  const azima = {
    id: "T05",
    code: "T05",
    title: "Split the orchestrator",
    kind: s.TaskKind.AZIMA,
    status: s.TaskStatus.PENDING,
    dependsOn: [],
    createTime: at(1),
    azima: { state: s.AzimaState.OPEN, ready: true },
    ...extra,
  };
  const tasks = new Map([azima, ...parts].map((x) => [x.id, x]));
  return s.renderToStaticMarkup(
    h(s.AzimaCard, {
      azima,
      parts,
      tasks,
      focus: azima.id,
      render: (task) =>
        h("article", { key: task.id, className: "part" }, task.code),
    }),
  );
}

test("an azima's description is Markdown, rendered: opened, and as a draft", () => {
  const text = "Split the **orchestrator**:\n\n- the scheduler\n- the `runner`";
  const markdown = (html) => {
    assert.match(html, /<div class="ac-markdown">/);
    assert.match(html, /<strong>orchestrator<\/strong>/);
    assert.match(html, /<li>the scheduler<\/li>/);
    assert.match(html, /<code>runner<\/code>/);
    assert.doesNotMatch(html, /\*\*/);
  };
  markdown(openedAzima({ description: text }));
  markdown(
    s.renderToStaticMarkup(
      h(s.DraftAzimaCard, {
        azima: {
          id: "T31",
          code: "T31",
          title: "Spread the work",
          kind: s.TaskKind.AZIMA,
          status: s.TaskStatus.PENDING,
          draft: true,
          description: text,
          dependsOn: [],
          azima: { state: s.AzimaState.DRAFT },
        },
        focus: "T31",
      }),
    ),
  );
});

test("an azima's title renames in place from its card, open or draft: a pencil, then a field in its heading", () => {
  const azima = {
    id: "T36",
    code: "T36",
    title: "feat/x reaches main",
    kind: s.TaskKind.AZIMA,
    status: s.TaskStatus.PENDING,
    dependsOn: [],
    azima: { state: s.AzimaState.OPEN, ready: true },
  };
  const draft = {
    ...azima,
    draft: true,
    azima: { state: s.AzimaState.DRAFT },
  };
  const card = (Card, props) =>
    s.renderToStaticMarkup(
      h(Card, { tasks: new Map(), parts: [], render: () => null, ...props }),
    );
  for (const [Card, a] of [
    [s.AzimaCard, azima],
    [s.DraftAzimaCard, draft],
  ]) {
    // Without a way to save it, no pencil.
    assert.doesNotMatch(card(Card, { azima: a }), /azima-rename/);
    const shown = card(Card, { azima: a, onRename() {} });
    assert.match(
      shown,
      /<button type="button" class="icon-button azima-rename" title="Rename this azima" aria-label="Rename this azima">/,
    );
    assert.match(shown, /<strong[^>]*>feat\/x reaches main<\/strong>/);
    const editing = card(Card, { azima: a, onRename() {}, renaming: true });
    assert.match(
      editing,
      /<span class="agent-code">T36<\/span><input type="text" class="azima-title-edit" aria-label="Title of the azima" maxLength="500" autofocus="" value="feat\/x reaches main"\/>/,
    );
    // The field takes the heading's place, and the pencil's.
    assert.doesNotMatch(editing, /azima-rename|aria-expanded/);
  }
});

test("an opened azima: its description, then its proof, on the left; its parts on the right, one per row", () => {
  const at = (seconds) => ({ seconds: BigInt(seconds), nanos: 0 });
  const part = (code, status) => ({
    id: code,
    code,
    title: `Part ${code}`,
    status,
    dependsOn: [],
    createTime: at(1),
  });
  const parts = [
    part("W1", s.TaskStatus.RUNNING),
    part("W2", s.TaskStatus.DONE),
  ];
  const described = openedAzima({ description: "Split it in **two**." }, parts);
  // Left, the description; right, what moves, one per row, the finished folded under it.
  assert.match(
    described,
    /<div class="azima-body with-side"><div class="azima-side"><div class="azima-description"><div class="ac-markdown">[\s\S]*?<\/div><\/div><\/div><div class="azima-parts"><div class="azima-part-list"><article class="part">W1<\/article><\/div><button[^>]*class="fold-line"/,
  );
  assert.doesNotMatch(described, /card-grid|task-grid/);
  // Awaiting its proof: what it needs, under the description.
  const proving = openedAzima(
    {
      description: "Split it in **two**.",
      azima: { state: s.AzimaState.AWAITING_PROOF, ready: true },
      proofNeeds: [
        { box: "Runs on macOS", needs: "a Mac", provers: [s.Prover.MAC] },
      ],
    },
    parts,
  );
  assert.match(
    proving,
    /<div class="azima-side"><div class="azima-description">[\s\S]*?<div class="azima-proof">[\s\S]*?Runs on macOS[\s\S]*?<\/div><\/div><div class="azima-parts">/,
  );
  // With no description, its parts take the whole width.
  const bare = openedAzima({}, parts);
  assert.match(
    bare,
    /<div class="azima-body"><div class="azima-parts"><div class="azima-part-list">/,
  );
  assert.doesNotMatch(bare, /azima-side/);
});

test("draft azimas: fold, card with description, actions, and flying carpet", () => {
  const at = (seconds) => ({ seconds: BigInt(seconds), nanos: 0 });
  const draft = (code, title, extra = {}) => ({
    id: code,
    code,
    title,
    kind: s.TaskKind.AZIMA,
    status: s.TaskStatus.PENDING,
    draft: true,
    description: "Explore multi-machine distribution.",
    dependsOn: [],
    createTime: at(1),
    azima: { state: s.AzimaState.DRAFT },
    ...extra,
  });
  const regular = (code, title, extra = {}) => ({
    id: code,
    code,
    title,
    kind: s.TaskKind.AZIMA,
    status: s.TaskStatus.PENDING,
    draft: false,
    dependsOn: [],
    createTime: at(1),
    azima: { state: s.AzimaState.OPEN, ready: true },
    ...extra,
  });
  const work = (code, status) => ({
    id: code,
    code,
    title: `Task ${code}`,
    status,
    dependsOn: [],
    createTime: at(1),
  });

  const d1 = draft("T31", "Spread work over other machines", {
    description: "Anticipate spreading work across nodes before launch.",
  });
  const d2 = draft("T32", "Cold storage backup", { description: "" });
  const openAzima = regular("T01", "Core pipeline");
  const runningWork = work("W01", s.TaskStatus.RUNNING);

  const allTasks = [d1, d2, openAzima, runningWork];
  const byId = new Map(allTasks.map((t) => [t.id, t]));

  // 1. Flying carpet: drafts are collected in drafts, NOT in moving, waiting, or azimas.
  const myWish = wish("w1", "Multi-machine", s.WishState.ACTIVE, 1);
  const plan = s.flightPlan([myWish], {
    [myWish.id]: {
      wish: myWish,
      tasks: allTasks,
      questions: [],
      blocks: [],
      allowances: [],
      runs: [],
      loaded: true,
    },
  });
  assert.equal(plan.drafts.length, 2);
  assert.deepEqual(
    plan.drafts.map((x) => x.item.code),
    ["T31", "T32"],
  );
  // Drafts are not moving tasks:
  assert.deepEqual(
    plan.moving.map((x) => x.item.code),
    ["W01"],
  );
  // Drafts are not in active azimas list:
  assert.deepEqual(
    plan.azimas.map((x) => x.item.azima.code),
    ["T01"],
  );

  // 2. DraftAzimaCard rendered directly: shows draft badge, code, title.
  let openedCode = "";
  let movedCode = "";
  const cardComponent = (az) =>
    h(s.DraftAzimaCard, {
      key: az.id,
      azima: az,
      tasks: byId,
      onOpen: () => {
        openedCode = az.code;
      },
      onMove: () => {
        movedCode = az.code;
      },
      focus: az.id,
    });

  const cardHtml = s.renderToStaticMarkup(cardComponent(d1));
  assert.match(cardHtml, /azima-card tone-later open/);
  assert.match(cardHtml, /<span class="agent-code">T31<\/span>/);
  assert.match(cardHtml, /<strong title="Spread work over other machines">/);
  assert.match(
    cardHtml,
    /Anticipate spreading work across nodes before launch\./,
  );
  assert.match(cardHtml, />Open this azima<\/button>/);
  assert.match(cardHtml, />Move<\/button>/);

  // When card has no description: shows muted "No description yet."
  const emptyCardHtml = s.renderToStaticMarkup(cardComponent(d2));
  assert.match(emptyCardHtml, /No description yet\./);

  // Clicking open and move triggers callbacks:
  const liveCard = live(s.DraftAzimaCard, {
    azima: d1,
    tasks: byId,
    onOpen: () => {
      openedCode = d1.code;
    },
    onMove: () => {
      movedCode = d1.code;
    },
    focus: d1.id,
  });
  const buttons = liveCard.all("button");
  const openBtn = buttons.find((b) => b.props.children === "Open this azima");
  const moveBtn = buttons.find((b) => b.props.children === "Move");
  assert.ok(openBtn && moveBtn);
  openBtn.props.onClick();
  assert.equal(openedCode, "T31");
  moveBtn.props.onClick();
  assert.equal(movedCode, "T31");

  // 3. TaskSections fold: "Later: 2 drafts"
  const sectionsHtml = s.renderToStaticMarkup(
    h(s.TaskSections, {
      moving: s.movingTasks(allTasks),
      azimas: s.azimaGroups(allTasks),
      doneAzimas: [],
      drafts: s.draftAzimas(allTasks),
      showDrafts: false,
      renderDraft: (draft) => cardComponent(draft),
      render: () => null,
    }),
  );
  assert.match(sectionsHtml, /Later: 2 drafts/);
  // When fold is closed, cards are not rendered
  assert.doesNotMatch(sectionsHtml, /Anticipate spreading work/);

  // When fold is open: cards are rendered
  const openSectionsHtml = s.renderToStaticMarkup(
    h(s.TaskSections, {
      moving: s.movingTasks(allTasks),
      azimas: s.azimaGroups(allTasks),
      doneAzimas: [],
      drafts: s.draftAzimas(allTasks),
      showDrafts: true,
      renderDraft: (draft) => cardComponent(draft),
      render: () => null,
    }),
  );
  assert.match(openSectionsHtml, /Hide drafts/);
  assert.match(
    openSectionsHtml,
    /Anticipate spreading work across nodes before launch\./,
  );
});

test("azimas are ordered in a stable 6-tier order and laid out in a single column", () => {
  const at = (seconds) => ({ seconds: BigInt(seconds), nanos: 0 });
  const azima = (code, title, extra) => ({
    id: code,
    code,
    title,
    kind: s.TaskKind.AZIMA,
    status: s.TaskStatus.PENDING,
    dependsOn: [],
    proofNeeds: [],
    createTime: at(100),
    ...extra,
  });
  const work = (code, status, partOf, start, end) => ({
    id: code,
    code,
    title: `Task ${code}`,
    status,
    partOf,
    dependsOn: [],
    createTime: at(100),
    startTime: start ? at(start) : undefined,
    endTime: end ? at(end) : undefined,
  });

  // Six tiers of azimas:
  // Tier 0: Moving (parts running or state in progress)
  const aMoving1 = azima("T02", "Moving orchestrator", {
    startTime: at(150),
    azima: {
      state: s.AzimaState.IN_PROGRESS,
      ready: true,
      parts: 3,
      partsDone: 1,
      partsRunning: 1,
    },
  });
  const aMoving2 = azima("T01", "Moving builder", {
    azima: {
      state: s.AzimaState.OPEN,
      ready: true,
      parts: 2,
      partsDone: 0,
      partsRunning: 1,
    },
  });
  // Tier 1: Awaiting proof
  const aProof1 = azima("T04", "Validate release", {
    azima: {
      state: s.AzimaState.AWAITING_PROOF,
      ready: true,
      parts: 2,
      partsDone: 2,
    },
  });
  const aProof2 = azima("T03", "Review design", {
    azima: {
      state: s.AzimaState.AWAITING_PROOF,
      ready: true,
      parts: 1,
      partsDone: 1,
    },
  });
  // Tier 2: Ready not started
  const aReady1 = azima("T06", "Ready pipeline B", {
    azima: { state: s.AzimaState.OPEN, ready: true },
  });
  const aReady2 = azima("T05", "Ready pipeline A", {
    azima: { state: s.AzimaState.OPEN, ready: true },
  });
  // Tier 3: Blocked
  const aBlocked1 = azima("T08", "Blocked consumer", {
    azima: { state: s.AzimaState.OPEN, ready: false },
  });
  const aBlocked2 = azima("T07", "Blocked worker", {
    azima: { state: s.AzimaState.OPEN, ready: false },
  });
  // Tier 4: Draft
  const aDraft1 = azima("T31", "Draft expansion", {
    draft: true,
    azima: { state: s.AzimaState.DRAFT },
  });
  const aDraft2 = azima("T30", "Draft caching", {
    draft: true,
  });
  // Tier 5: Done
  const aDone1 = azima("T10", "Done setup", {
    status: s.TaskStatus.DONE,
    startTime: at(110),
    endTime: at(140),
    azima: {
      state: s.AzimaState.DONE,
      ready: true,
      parts: 1,
      partsDone: 1,
    },
  });
  const aDone2 = azima("T09", "Done foundation", {
    status: s.TaskStatus.DONE,
    startTime: at(105),
    endTime: at(125),
    azima: {
      state: s.AzimaState.DONE,
      ready: true,
      parts: 1,
      partsDone: 1,
    },
  });

  // Parts for T02 (1 done, 1 running, 1 waiting)
  const w1 = work("W01", s.TaskStatus.DONE, "T02", 150, 180);
  const w2 = work("W02", s.TaskStatus.RUNNING, "T02", 185);
  const w3 = work("W03", s.TaskStatus.WAITING, "T02");
  // Part for T01 (1 running)
  const w4 = work("W04", s.TaskStatus.RUNNING, "T01", 160);

  // 1. Verify azimaRank for each tier
  assert.equal(s.azimaRank(aMoving1), 0);
  assert.equal(s.azimaRank(aMoving2), 0);
  assert.equal(s.azimaRank(aProof1), 1);
  assert.equal(s.azimaRank(aProof2), 1);
  assert.equal(s.azimaRank(aReady1), 2);
  assert.equal(s.azimaRank(aReady2), 2);
  assert.equal(s.azimaRank(aBlocked1), 3);
  assert.equal(s.azimaRank(aBlocked2), 3);
  assert.equal(s.azimaRank(aDraft1), 4);
  assert.equal(s.azimaRank(aDraft2), 4);
  assert.equal(s.azimaRank(aDone1), 5);
  assert.equal(s.azimaRank(aDone2), 5);

  // Azimas in shuffled input order
  const allTasks = [
    aDone1,
    aBlocked1,
    aReady1,
    aProof1,
    aMoving1,
    aDraft1,
    w1,
    w2,
    w3,
    w4,
    aDraft2,
    aMoving2,
    aProof2,
    aReady2,
    aBlocked2,
    aDone2,
  ];

  // 2. azimaGroups orders non-drafts strictly by azimaRank, then compareCodes
  const groups = s.azimaGroups(allTasks);
  const groupCodes = groups.map((g) => g.azima.code);
  assert.deepEqual(groupCodes, [
    // Tier 0 (moving, ordered by code)
    "T01",
    "T02",
    // Tier 1 (awaiting proof, ordered by code)
    "T03",
    "T04",
    // Tier 2 (ready, ordered by code)
    "T05",
    "T06",
    // Tier 3 (blocked, ordered by code)
    "T07",
    "T08",
    // Tier 5 (done, ordered by code)
    "T09",
    "T10",
  ]);

  // Drafts are collected separately, ordered by code
  const drafts = s.draftAzimas(allTasks);
  assert.deepEqual(
    drafts.map((d) => d.code),
    ["T30", "T31"],
  );

  // 3. Flying carpet azimas are sorted by azimaRank, then compareCodes
  const testWish = wish("w_test", "Azima layout wish", s.WishState.ACTIVE, 1);
  const plan = s.flightPlan([testWish], {
    [testWish.id]: {
      wish: testWish,
      tasks: allTasks,
      questions: [],
      blocks: [],
      allowances: [],
      runs: [],
      loaded: true,
    },
  });
  assert.deepEqual(
    plan.azimas.map((x) => x.item.azima.code),
    ["T01", "T02", "T03", "T04", "T05", "T06", "T07", "T08", "T09", "T10"],
  );
  assert.deepEqual(
    plan.drafts.map((x) => x.item.code),
    ["T30", "T31"],
  );

  // 4. Layout: TaskSections renders in a single column (.azima-list), never .azima-grid
  const byId = new Map(allTasks.map((t) => [t.id, t]));
  const card = (task) =>
    h(s.WishTask, {
      key: task.id,
      task,
      onStop() {},
      async onSend() {},
      async onDone() {},
    });
  const renderAzima = ({ azima, parts }) =>
    h(s.AzimaCard, {
      key: azima.id,
      azima,
      parts,
      tasks: byId,
      render: card,
    });
  const renderDraft = (draft) =>
    h(s.DraftAzimaCard, {
      key: draft.id,
      azima: draft,
      tasks: byId,
      onOpen() {},
      onMove() {},
    });

  const activeAzimas = groups.filter((g) => !s.azimaFinished(g.azima));
  const doneAzimas = groups.filter((g) => s.azimaFinished(g.azima));

  const sectionsHtml = s.renderToStaticMarkup(
    h(s.TaskSections, {
      moving: s.movingTasks(allTasks),
      azimas: activeAzimas,
      doneAzimas,
      drafts,
      showDone: true,
      showDrafts: true,
      renderAzima,
      renderDraft,
      render: card,
    }),
  );

  // Must have .azima-list and never .azima-grid
  assert.match(sectionsHtml, /<div class="azima-list">/);
  assert.doesNotMatch(sectionsHtml, /azima-grid/);

  // The active azimas appear in single column in exact stable order
  const activeOrder = [
    ...sectionsHtml.matchAll(/<span class="agent-code">(T\d+)<\/span>/g),
  ].map((m) => m[1]);
  assert.deepEqual(activeOrder, [
    "T01",
    "T02",
    "T03",
    "T04",
    "T05",
    "T06",
    "T07",
    "T08",
    "T30",
    "T31",
    "T09",
    "T10",
  ]);

  // Fold order: drafts fold appears before done fold
  const closedFoldsHtml = s.renderToStaticMarkup(
    h(s.TaskSections, {
      moving: s.movingTasks(allTasks),
      azimas: activeAzimas,
      doneAzimas,
      drafts,
      showDone: false,
      showDrafts: false,
      renderAzima,
      renderDraft,
      render: card,
    }),
  );
  const draftsFoldIndex = closedFoldsHtml.indexOf("Later: 2 drafts");
  const doneFoldIndex = closedFoldsHtml.indexOf("Show the 2 finished");
  assert.ok(
    draftsFoldIndex > 0 && doneFoldIndex > draftsFoldIndex,
    "drafts fold must appear before done fold",
  );

  // 5. Azima row details: code, goal, state, progress bar, counts, parts text, time
  const t2CardHtml = s.renderToStaticMarkup(
    h(s.AzimaCard, {
      azima: aMoving1,
      parts: [w1, w2, w3],
      tasks: byId,
      render: card,
      focus: aMoving1.id,
    }),
  );

  // Code, goal (title), state badge
  assert.match(t2CardHtml, /<span class="agent-code">T02<\/span>/);
  assert.match(t2CardHtml, /Moving orchestrator/);
  assert.match(
    t2CardHtml,
    /<span class="status-badge tone-running">[\s\S]*?<span>In progress<\/span><\/span>/,
  );

  // Progress: segmented bar and its counts (1/3); the parts by state and the time only on the bar's hover, never
  // said twice in the heading.
  assert.match(t2CardHtml, /<span class="azima-bar-done" style="width:33\.33/);
  assert.match(
    t2CardHtml,
    /<span class="azima-bar-running" style="width:33\.33/,
  );
  assert.match(
    t2CardHtml,
    /<span class="azima-progress" title="1 of 3 parts done\n1 done, 1 running, 1 waiting\n[^"]+ · Running since [^"]+">/i,
  );
  assert.match(t2CardHtml, /<span class="azima-progress-counts">1\/3<\/span>/);
  const t2Heading = t2CardHtml.match(
    /<button[^>]*class="azima-heading"[^>]*>[\s\S]*?<\/button>/,
  )[0];
  assert.doesNotMatch(t2Heading, /azima-progress-parts|task-time/);
  assert.doesNotMatch(t2Heading, />1 done, 1 running, 1 waiting</);

  // 6. Test azimaTime function directly
  const nowMs = 200_000;
  const runningTime = s.azimaTime(aMoving1, [w1, w2, w3], nowMs);
  assert.ok(runningTime.text.length > 0);
  assert.match(runningTime.title, /running since/i);

  const doneTime = s.azimaTime(aDone1, [], nowMs);
  assert.equal(doneTime.text, "30s");
  assert.match(doneTime.title, /ran from .* to/i);
});

test("azima counters and states in the window are right before any azima is opened on a wish with more tasks than one page", async () => {
  const at = (seconds) => ({ seconds: BigInt(seconds), nanos: 0 });
  const wishId = "01a11833-a440-7479-a067-52615c91da71";
  const lamp = wish(wishId, "Large wish", s.WishState.ACTIVE, 1, {
    createTime: at(1),
  });

  const azima1 = {
    id: "A1",
    wishId,
    code: "T1",
    title: "Lay the ground",
    kind: s.TaskKind.AZIMA,
    status: s.TaskStatus.DONE,
    azima: {
      state: s.AzimaState.DONE,
      ready: true,
      parts: 180,
      partsDone: 180,
      partsRunning: 0,
    },
    dependsOn: [],
    createTime: at(1),
  };
  const azima2 = {
    id: "A2",
    wishId,
    code: "T2",
    title: "The orchestrator",
    kind: s.TaskKind.AZIMA,
    status: s.TaskStatus.RUNNING,
    azima: {
      state: s.AzimaState.IN_PROGRESS,
      ready: true,
      parts: 250,
      partsDone: 200,
      partsRunning: 5,
    },
    dependsOn: ["T1"],
    createTime: at(1),
  };

  // 1. AzimaCard rendered directly before any parts are loaded (parts: [])
  // A2 is in progress with 200/250 parts done, 5 running, 45 waiting
  const cardInProgress = s.renderToStaticMarkup(
    h(s.AzimaCard, {
      azima: azima2,
      parts: [],
      tasks: new Map([[azima2.id, azima2]]),
      render: () => null,
    }),
  );
  assert.equal(s.azimaFinished(azima2), false);
  assert.match(cardInProgress, /class="azima-progress"/);
  assert.match(cardInProgress, /200\/250<\/span>/);
  assert.match(
    cardInProgress,
    /class="azima-progress" title="200 of 250 parts done\n200 done, 5 running, 45 waiting"/,
  );
  assert.match(cardInProgress, /status-badge tone-running/);

  // A1 is done with 180/180 parts done, parts: []
  const cardDone = s.renderToStaticMarkup(
    h(s.AzimaCard, {
      azima: azima1,
      parts: [],
      tasks: new Map([[azima1.id, azima1]]),
      render: () => null,
    }),
  );
  assert.equal(s.azimaFinished(azima1), true);
  assert.match(cardDone, /class="azima-progress"/);
  assert.match(cardDone, /180\/180<\/span>/);
  assert.match(cardDone, /status-badge tone-done/);

  // 2. Full window WishView with only page 1 loaded from the server
  // (e.g. 50 tasks out of 430 total)
  const page1Tasks = [azima1, azima2];
  for (let i = 1; i <= 48; i++) {
    page1Tasks.push({
      id: `W${i}`,
      wishId,
      code: `W${i}`,
      title: `Task ${i}`,
      kind: s.TaskKind.WORKER,
      status: i <= 20 ? s.TaskStatus.DONE : s.TaskStatus.PENDING,
      partOf: "A2",
      dependsOn: [],
      createTime: at(1),
    });
  }

  const transport = s.createRouterTransport(({ service }) => {
    service(s.WishService, { list: () => ({ wishes: [lamp] }) });
    service(s.ProjectService, { list: () => ({ projects: [] }) });
    service(s.TaskService, {
      list: () => ({
        tasks: page1Tasks,
        total: 430,
        nextPageToken: "page-2",
      }),
    });
    service(s.QuestionService, { list: () => ({ questions: [] }) });
    service(s.BlockService, { list: () => ({ blocks: [] }) });
  });

  const djinn = s.createDjinn(transport, 10);
  await djinn.store.changed("", [s.Change.WISH, s.Change.PROJECT]);
  const close = djinn.store.open(wishId);
  await djinn.store.changed(wishId, [s.Change.TASK]);
  close();

  const view = s.renderToStaticMarkup(
    h(s.DjinnProvider, { djinn }, h(s.WishView, { wish: lamp, onToast() {} })),
  );

  // Azima pill: 1 done out of 2 azimas
  assert.match(
    view,
    /title="1 of 2 azimas done"[^>]*>.*?<b>1<\/b>\s*\/ 2 azimas<\/span>/,
  );
  // Work pill: 380 done out of 430 total tasks across the wish
  assert.match(
    view,
    /title="380 of 430 tasks done"[^>]*>.*?<b>380<\/b>\s*\/ 430 done<\/span>/,
  );

  // 3. Tasks tab / TaskSections: azimas partition into active and done using server state,
  // and AzimaCard in progress renders 200/250 right away before opening
  const groups = s.azimaGroups(page1Tasks);
  const active = groups.filter((x) => !s.azimaFinished(x.azima));
  const done = groups.filter((x) => s.azimaFinished(x.azima));
  assert.equal(active.length, 1);
  assert.equal(done.length, 1);
  assert.equal(active[0].azima.code, "T2");
  assert.equal(done[0].azima.code, "T1");

  const tasksTab = s.renderToStaticMarkup(
    h(s.TaskSections, {
      moving: [],
      azimas: active,
      doneAzimas: done,
      fold: wishId,
      showDone: false,
      renderAzima: ({ azima, parts }) =>
        h(s.AzimaCard, {
          key: azima.id,
          azima,
          parts,
          tasks: new Map(page1Tasks.map((t) => [t.id, t])),
          render: () => null,
        }),
      render: () => null,
    }),
  );
  assert.match(tasksTab, /200\/250<\/span>/);
});

test("the azimas of a wish show in their rank, each with the grip that drags it (W134)", () => {
  const azima = (code, rank) => ({
    id: code,
    code,
    title: `Azima ${code}`,
    kind: s.TaskKind.AZIMA,
    status: s.TaskStatus.PENDING,
    dependsOn: [],
    rank,
    azima: { state: s.AzimaState.OPEN, ready: true },
  });
  // T3 ranked first, T1 second; T2 and T10, never ranked, after them by code, whatever their state.
  const tasks = [
    azima("T1", 2),
    azima("T2", 0),
    azima("T3", 1),
    azima("T10", 0),
  ];
  tasks[1].azima = { state: s.AzimaState.IN_PROGRESS, ready: true };
  const groups = s.azimaGroups(tasks);
  assert.deepEqual(
    groups.map((g) => g.azima.code),
    ["T3", "T1", "T2", "T10"],
  );
  const html = s.renderToStaticMarkup(
    s.createElement(s.RankedAzimaList, {
      groups,
      onRank() {},
      render: ({ azima, parts }, grip) =>
        s.createElement(s.AzimaCard, {
          key: azima.id,
          azima,
          parts,
          grip,
          tasks: new Map(),
          render: () => null,
        }),
    }),
  );
  assert.match(html, /class="azima-list ranked"/);
  const grips = [
    ...html.matchAll(
      /class="azima-grip" title="([^"]+)"[^>]*draggable="true"/g,
    ),
  ].map((m) => m[1].split(" ")[1]);
  assert.deepEqual(grips, ["T3", "T1", "T2", "T10"]);
  assert.match(html, /title="1\. T3 \(drag, or Alt\+Arrow, to change its rank/);
  // The grip comes first in the card's heading row.
  assert.match(
    html,
    /<div class="azima-row"><button type="button" class="azima-grip"/,
  );
});

test("an azima row starts with its status badge: state -> code -> title", () => {
  const azima = {
    id: "a1",
    code: "T1",
    title: "Ship the lamp",
    kind: s.TaskKind.AZIMA,
    azima: { state: s.AzimaState.IN_PROGRESS, partsRunning: 1 },
  };
  const azimaMarkup = s.renderToStaticMarkup(
    h(s.AzimaCard, {
      azima,
      parts: [],
      tasks: new Map(),
      render() {
        return null;
      },
    }),
  );
  const azimaHeading = azimaMarkup.match(
    /<button[^>]*class="azima-heading"[^>]*>([\s\S]*?)<\/button>/,
  )?.[1];
  assert.ok(azimaHeading, "azima heading exists");
  const azimaStatusIdx = azimaHeading.indexOf('class="status-badge');
  const azimaCodeIdx = azimaHeading.indexOf('class="agent-code"');
  const azimaTitleIdx = azimaHeading.indexOf("<strong");
  assert.ok(azimaStatusIdx !== -1, "azima status badge is present in heading");
  assert.ok(azimaCodeIdx !== -1, "azima agent code is present in heading");
  assert.ok(azimaTitleIdx !== -1, "azima title is present in heading");
  assert.ok(azimaStatusIdx < azimaCodeIdx, "azima status comes before code");
  assert.ok(azimaCodeIdx < azimaTitleIdx, "azima code comes before title");
});

test("the wish shows its push strategy and azima cards show branch, sync and PR in per-azima mode", () => {
  // 1. Effective strategy resolution
  const wDefault = { id: "w1", pushStrategy: s.PushStrategy.UNSPECIFIED };
  const pDefault = [{ id: "p1", pushStrategy: s.PushStrategy.UNSPECIFIED }];
  const pAzima = [{ id: "p1", pushStrategy: s.PushStrategy.AZIMA }];
  const wWish = { id: "w1", pushStrategy: s.PushStrategy.WISH };
  const wAzima = { id: "w1", pushStrategy: s.PushStrategy.AZIMA };

  assert.equal(
    s.effectivePushStrategy(wDefault, pDefault),
    s.PushStrategy.WISH,
  );
  assert.equal(s.effectivePushStrategy(wDefault, pAzima), s.PushStrategy.AZIMA);
  assert.equal(s.effectivePushStrategy(wWish, pAzima), s.PushStrategy.WISH);
  assert.equal(s.effectivePushStrategy(wAzima, pDefault), s.PushStrategy.AZIMA);

  // 2. PushStrategySelector component
  let chosenStrategy = null;
  const selectorHtml = s.renderToStaticMarkup(
    h(s.PushStrategySelector, {
      wish: wDefault,
      projects: pAzima,
      onChange: (strat) => {
        chosenStrategy = strat;
      },
    }),
  );
  assert.match(selectorHtml, /class="wish-push-strategy"/);
  assert.match(selectorHtml, /aria-label="Push strategy"/);
  assert.match(selectorHtml, /Per wish<\/button>/);
  assert.match(
    selectorHtml,
    /<button[^>]*class="active"[^>]*>Per azima<\/button>/,
  );

  // Test live clicking
  const liveSelector = live(s.PushStrategySelector, {
    wish: wDefault,
    projects: pAzima,
    onChange: (strat) => {
      chosenStrategy = strat;
    },
  });
  const buttons = liveSelector.all("button");
  const wishBtn = buttons.find((b) => b.props.children === "Per wish");
  assert.ok(wishBtn);
  wishBtn.props.onClick();
  assert.equal(chosenStrategy, s.PushStrategy.WISH);

  // 3. Azima branch naming helper
  const taskT27 = {
    id: "a27",
    code: "T27",
    title: "Djinn stays fast",
    azima: {},
  };
  assert.equal(s.azimaBranchName(taskT27), "djinn/T27-djinn-stays-fast");

  // 4. AzimaCard in default (per-wish) mode does not show branch, sync or PR
  const defaultCardHtml = s.renderToStaticMarkup(
    h(s.AzimaCard, {
      azima: {
        ...taskT27,
        azima: {
          branch: "djinn/T27-djinn-stays-fast",
          sync: { ahead: 1, behind: 0 },
        },
      },
      parts: [],
      tasks: new Map(),
      render: () => null,
      isPerAzima: false,
    }),
  );
  assert.doesNotMatch(defaultCardHtml, /T27-djinn-stays-fast/);
  assert.doesNotMatch(defaultCardHtml, /azima-actions/);

  // 5. AzimaCard in per-azima mode with branch, the main it lands in, sync, push button, and PR
  let pushedAzima = null;
  let openedPrUrl = null;
  const azimaPerAzima = {
    ...taskT27,
    azima: {
      branch: "djinn/T27-djinn-stays-fast",
      baseBranch: "main",
      cutFrom: "0123456789abcdef0123456789abcdef01234567",
      sync: {
        ahead: 2,
        behind: 0,
        branch: "djinn/T27-djinn-stays-fast",
        remote: "origin",
        target: "djinn/T27-djinn-stays-fast",
      },
      pr: {
        state: s.AzimaPrState.OPEN,
        url: "https://github.com/Empowill/Djinn/pull/42",
      },
    },
  };

  const perAzimaHtml = s.renderToStaticMarkup(
    h(s.AzimaCard, {
      azima: azimaPerAzima,
      parts: [],
      tasks: new Map(),
      render: () => null,
      isPerAzima: true,
      onPush: (a) => {
        pushedAzima = a;
      },
      onOpenPr: (url) => {
        openedPrUrl = url;
      },
    }),
  );

  // Closed, its heading says nothing of its branch, neither shown nor on hover: only its line out of sync names it.
  assert.match(perAzimaHtml, /<strong title="Djinn stays fast">/);
  assert.doesNotMatch(perAzimaHtml, /azima-landing/);
  assert.doesNotMatch(
    perAzimaHtml.match(
      /<button type="button" class="azima-heading".*?<\/button>/,
    )[0],
    /T27-djinn-stays-fast/,
  );

  // Opened, its branch, the commit of main it was cut from, and that it lands in main, across the card.
  const openedHtml = s.renderToStaticMarkup(
    h(s.AzimaCard, {
      azima: azimaPerAzima,
      parts: [],
      tasks: new Map(),
      render: () => null,
      isPerAzima: true,
      focus: "a27",
    }),
  );
  assert.match(
    openedHtml,
    /<div class="azima-body"><p class="azima-landing">.*?<code>djinn\/T27-djinn-stays-fast<\/code><span>cut from main at 01234567<\/span><span>lands in main<\/span><\/p>/,
  );
  // Not cut yet: it will be when its first part starts. Never stacked on another azima's branch.
  const notCutHtml = s.renderToStaticMarkup(
    h(s.AzimaCard, {
      azima: {
        ...taskT27,
        azima: {
          state: s.AzimaState.OPEN,
          branch: "djinn/T27-djinn-stays-fast",
        },
      },
      parts: [],
      tasks: new Map(),
      render: () => null,
      isPerAzima: true,
      focus: "a27",
    }),
  );
  assert.match(
    notCutHtml,
    /<span>cut from main when its first part starts<\/span><span>lands in main<\/span>/,
  );
  // Per wish, no branch of its own: no line.
  assert.doesNotMatch(
    s.renderToStaticMarkup(
      h(s.AzimaCard, {
        azima: azimaPerAzima,
        parts: [],
        tasks: new Map(),
        render: () => null,
        focus: "a27",
      }),
    ),
    /azima-landing/,
  );

  // Sync state and push button: the short line of the side panel.
  assert.match(
    perAzimaHtml,
    /<div class="sync-line" title="2 commits ahead of origin\/djinn\/T27-djinn-stays-fast"><span class="sync-branch">djinn\/T27-djinn-stays-fast<\/span><span class="sync-counts"><span class="sync-ahead">↑2<\/span><\/span><button type="button" class="icon-button sync-push" title="Push djinn\/T27-djinn-stays-fast"/,
  );
  assert.doesNotMatch(perAzimaHtml, /button accent/);

  // PR link with state "PR open"
  assert.match(
    perAzimaHtml,
    /<a href="https:\/\/github\.com\/Empowill\/Djinn\/pull\/42" class="azima-pr-link"/,
  );
  assert.match(perAzimaHtml, /PR open/);

  // Test live click on push and PR
  const liveAzimaCard = live(s.AzimaCard, {
    azima: azimaPerAzima,
    parts: [],
    tasks: new Map(),
    render: () => null,
    isPerAzima: true,
    onPush: (a) => {
      pushedAzima = a;
    },
    onOpenPr: (url) => {
      openedPrUrl = url;
    },
  });
  const fakeEvent = { stopPropagation() {}, preventDefault() {} };
  liveAzimaCard.one(s.SyncLine).onPush();
  assert.equal(pushedAzima?.id, "a27");
  assert.equal(
    liveAzimaCard.one(s.SyncLine).busy,
    "Pushing djinn/T27-djinn-stays-fast…",
  );

  const prLink = liveAzimaCard
    .all("a")
    .find((a) => a.props.className === "azima-pr-link");
  assert.ok(prLink);
  prLink.props.onClick(fakeEvent);
  assert.equal(openedPrUrl, "https://github.com/Empowill/Djinn/pull/42");

  // 6. Test pushed state (clean sync) and other PR states (PROPOSED, MERGED, and badge without URL)
  const cleanAndProposedHtml = s.renderToStaticMarkup(
    h(s.AzimaCard, {
      azima: {
        ...taskT27,
        azima: {
          branch: "djinn/T27-djinn-stays-fast",
          sync: { ahead: 0, behind: 0, target: "origin" },
          pr: { state: s.AzimaPrState.PROPOSED },
        },
      },
      parts: [],
      tasks: new Map(),
      render: () => null,
      isPerAzima: true,
    }),
  );
  assert.match(
    cleanAndProposedHtml,
    /<span class="azima-pushed-status">Pushed<\/span>/,
  );
  assert.match(
    cleanAndProposedHtml,
    /<span class="azima-pr-badge">PR proposed<\/span>/,
  );
  assert.doesNotMatch(cleanAndProposedHtml, /sync-line/);

  // Its project's syncs tell how its branch stands, over what the task recorded.
  const fromProjectHtml = s.renderToStaticMarkup(
    h(s.AzimaCard, {
      azima: { ...taskT27, azima: { branch: "djinn/T27-djinn-stays-fast" } },
      sync: projectSync("djinn/T27-djinn-stays-fast", 1, 3, {
        azimaId: "a27",
        azimaCode: "T27",
        azimaTitle: "Djinn stays fast",
      }),
      parts: [],
      tasks: new Map(),
      render: () => null,
      isPerAzima: true,
      onPush() {},
    }),
  );
  assert.match(
    fromProjectHtml,
    /<span class="sync-branch">djinn\/T27-djinn-stays-fast<\/span><span class="sync-counts"><span class="sync-ahead">↑1<\/span><span class="sync-behind">↓3<\/span><\/span>/,
  );

  const mergedHtml = s.renderToStaticMarkup(
    h(s.AzimaCard, {
      azima: {
        ...taskT27,
        azima: {
          sync: { ahead: 0, behind: 0, branch: "main" },
          pr: { state: s.AzimaPrState.MERGED },
        },
      },
      parts: [],
      tasks: new Map(),
      render: () => null,
      isPerAzima: true,
    }),
  );
  assert.match(mergedHtml, /<span class="azima-pushed-status">Pushed<\/span>/);
  assert.match(mergedHtml, /<span class="azima-pr-badge">PR merged<\/span>/);
  assert.doesNotMatch(mergedHtml, /azima-merged/);

  // Its work all in main (W192): a small mark beside its code, how Djinn found it on hover, per azima or not.
  const markedHtml = (merged) =>
    s.renderToStaticMarkup(
      h(s.AzimaCard, {
        azima: { ...taskT27, azima: { merged } },
        parts: [],
        tasks: new Map(),
        render: () => null,
      }),
    );
  assert.match(
    markedHtml({ requestUrl: "https://github.com/acme/lamp/pull/7" }),
    /<span class="agent-code">T27<\/span><span class="azima-merged" role="img" aria-label="Merged into main" title="Merged into main\nAll its work is in it: its pull request merged, https:\/\/github.com\/acme\/lamp\/pull\/7. A task added to it clears this mark."><svg[^>]*lucide-git-merge/,
  );
  assert.match(
    markedHtml({ mainBranch: "master", mainSha: "53698aea52673bbd" }),
    /aria-label="Merged into master" title="Merged into master\nAll its work is in it: every commit of its branch and of its tasks, at 53698aea. A task/,
  );

  // A done azima whose commits main holds already, its remote branch left behind (W163): landed, never waiting for a
  // push, its sync to forget.
  const landed = projectSync("hotfix-tenant", 0, 0, {
    azimaId: "a27",
    azimaCode: "T27",
    azimaTitle: "Tenants apart",
    landed: true,
  });
  let forgotten;
  const landedCard = {
    azima: {
      ...taskT27,
      azima: { branch: "hotfix-tenant", baseBranch: "main" },
    },
    sync: landed,
    parts: [],
    tasks: new Map(),
    render: () => null,
    isPerAzima: true,
    onPush() {},
    onForgetSync: (sync) => {
      forgotten = sync;
    },
  };
  const landedHtml = s.renderToStaticMarkup(h(s.AzimaCard, landedCard));
  assert.match(
    landedHtml,
    /<span class="azima-pushed-status" title="Every commit of hotfix-tenant is in main already: nothing waits for a push">Landed in main<\/span><button type="button" class="icon-button azima-forget-sync" title="Forget the sync of hotfix-tenant/,
  );
  assert.doesNotMatch(landedHtml, /sync-line|>Pushed</);
  live(s.AzimaCard, landedCard)
    .all("button")
    .find((b) => b.props.className === "icon-button azima-forget-sync")
    .props.onClick(fakeEvent);
  assert.equal(forgotten?.branch, "hotfix-tenant");
  assert.equal(
    s.syncTitle(landed),
    "Branch of the azima T27 Tenants apart, wish Lamp\nLanded: main holds its commits already, nothing to push",
  );

  // 7. Verify T27: AzimaCard is memoized (React.memo)
  assert.equal(typeof s.AzimaCard, "object");
  assert.equal(s.AzimaCard.$$typeof, Symbol.for("react.memo"));
});
