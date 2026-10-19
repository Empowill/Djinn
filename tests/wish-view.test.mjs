// A wish's view (src/wish-view.tsx): its questions, its tasks, its reports, what merges and what investigates. Run with
// `go tool task test-ui`.
import test from "node:test";
import assert from "node:assert/strict";

import { screens, setupMockDom, wish } from "./screens.mjs";

const { s, h } = await screens();

// The three tests that follow a state everywhere workers are counted share these: a time, the side panel with the one
// wish counting the tasks given, and the wish's view with the flying carpet, both read from a server holding them.
const at = (seconds) => ({ seconds: BigInt(seconds), nanos: 0 });

const sidebar = (lamp, list, collapsed = false) =>
  s.renderToStaticMarkup(
    h(s.WishSidebar, {
      wishes: [lamp],
      projects: [],
      counts: { [lamp.id]: { questions: 0, ...s.runningOf(list) } },
      onSelectPlan() {},
      selectedWishId: "",
      selectedProjectId: "",
      collapsed,
      onSelectWish() {},
      onSelectProject() {},
      onMove() {},
      onNewProject() {},
    }),
  );

const page = async (lamp, list) => {
  const transport = s.createRouterTransport(({ service }) => {
    service(s.WishService, { list: () => ({ wishes: [lamp] }) });
    service(s.TaskService, { list: () => ({ tasks: list }) });
    service(s.QuestionService, { list: () => ({ questions: [] }) });
    service(s.BlockService, { list: () => ({ blocks: [] }) });
  });
  const djinn = s.createDjinn(transport, 10);
  const close = djinn.store.open(lamp.id);
  await djinn.store.changed(lamp.id, [s.Change.WISH]);
  close();
  const view = s.renderToStaticMarkup(
    h(s.DjinnProvider, { djinn }, h(s.WishView, { wish: lamp, onToast() {} })),
  );
  const plan = s.renderToStaticMarkup(
    h(
      s.DjinnProvider,
      { djinn },
      h(s.FlightPlan, { wishes: [lamp], onOpen() {}, onToast() {} }),
    ),
  );
  return { view, plan };
};

test("a wish's reports are notes for agents, with no section nor count of their own, and its question links the one it names", async () => {
  const wishId = "01a130b1-398a-7072-be60-ce928cb7241e";
  const dew = wish(wishId, "Dew", s.WishState.ACTIVE, 1);
  const transport = s.createRouterTransport(({ service }) => {
    service(s.WishService, { list: () => ({ wishes: [dew] }) });
    service(s.TaskService, {
      list: () => ({
        tasks: [
          {
            id: "t1",
            wishId,
            code: "W1",
            title: "Study the payroll API",
            status: s.TaskStatus.DONE,
          },
        ],
      }),
    });
    service(s.QuestionService, {
      list: () => ({
        questions: [
          {
            id: "q4",
            wishId,
            code: "Q04",
            text: s.dew.QUESTION,
            options: s.dew.options(wishId),
            context: s.dew.CONTEXT,
            recommendation: s.dew.RECOMMENDATION,
            rounds: [],
          },
        ],
      }),
    });
    service(s.BlockService, {
      list: () => ({
        blocks: [
          {
            id: "b1",
            wishId,
            taskId: "t1",
            kind: "report",
            title: s.dew.REPORT_TITLE,
            content: s.dew.REPORT,
          },
          {
            id: "b2",
            wishId,
            kind: "hand-off",
            title: "Lexicon",
            content: "x",
          },
        ],
      }),
    });
  });
  const djinn = s.createDjinn(transport, 10);
  const close = djinn.store.open(wishId);
  await djinn.store.changed(wishId, [s.Change.WISH]);
  close();
  const html = s.renderToStaticMarkup(
    h(s.DjinnProvider, { djinn }, h(s.WishView, { wish: dew, onToast() {} })),
  );
  // No section for the reports: neither its heading nor a card, and the report is not shown on the wish.
  assert.doesNotMatch(
    html,
    /wish-reports|id="reports"|report-card|Open the report/,
  );
  assert.doesNotMatch(html, /<h2>Reports/);
  assert.doesNotMatch(html, /What is left to know/);
  // The question names it: its card links it.
  assert.match(
    html,
    /question-report-link.*Report: Payroll for acme: feasibility/,
  );
  // The For agents tab holds it with the other blocks.
  assert.match(
    html,
    /id="view-tab-agents" aria-selected="false" class="discreet"><span class="djinn-eye-perch" data-perch="tab:agents" aria-hidden="true"><\/span>For agents<span class="count">2<\/span>/,
  );
});

test("a wish's screen puts its questions first, proposes to grant it when ready, and leaves its blocks to the agents' tab", async () => {
  const wishId = "01a11833-a440-7479-a067-52615c91da71";
  const ready = wish(wishId, "Ship the lamp", s.WishState.ACTIVE, 1, {
    ready: true,
  });
  const transport = s.createRouterTransport(({ service }) => {
    service(s.WishService, { list: () => ({ wishes: [ready] }) });
    service(s.TaskService, {
      list: () => ({
        tasks: [
          {
            id: "t1",
            wishId,
            code: "W1",
            title: "Trim the wick",
            status: s.TaskStatus.DONE,
          },
          {
            id: "t2",
            wishId,
            code: "W2",
            title: "Light the wick",
            status: s.TaskStatus.FAILED,
            error: "exit code 1",
            continuing: true,
          },
        ],
      }),
    });
    service(s.QuestionService, {
      list: () => ({
        questions: [
          {
            id: "q1",
            wishId,
            code: "Q01",
            text: "Light it tonight?",
            options: [],
            answer: { choice: s.Choice.YES, note: "" },
          },
          {
            id: "q2",
            wishId,
            code: "Q02",
            text: "Which oil?",
            options: ["Olive", "Paraffin"],
            rounds: [{ kind: s.RoundKind.ENLIGHTEN, note: "Burn time?" }],
          },
        ],
      }),
    });
    service(s.BlockService, {
      list: () => ({
        blocks: [
          {
            id: "b1",
            wishId,
            kind: "section",
            title: "Lexicon",
            content: "A **wick** carries the oil.",
          },
          {
            id: "b2",
            wishId,
            kind: "decision",
            title: "Olive oil only",
            content: "It smells good.",
          },
        ],
      }),
    });
  });
  const djinn = s.createDjinn(transport, 10);
  const close = djinn.store.open(wishId);
  await djinn.store.changed(wishId, [s.Change.WISH]);
  close();
  const html = s.renderToStaticMarkup(
    h(s.DjinnProvider, { djinn }, h(s.WishView, { wish: ready, onToast() {} })),
  );
  assert.match(html, /My wish is granted/);
  // The tasks have a tab of their own, with their count: the wish's tab does not show them.
  assert.match(
    html,
    /role="tab" id="view-tab-main" aria-selected="true" class="active"><span class="djinn-eye-perch" data-perch="tab:main" aria-hidden="true"><\/span>Wish<\/button>/,
  );
  assert.match(
    html,
    /role="tab" id="view-tab-tasks" aria-selected="false" class=""><span class="djinn-eye-perch" data-perch="tab:tasks" aria-hidden="true"><\/span>Tasks<span class="count">2<\/span>/,
  );
  assert.doesNotMatch(html, /Trim the wick|Light the wick/);
  // No notes: the blocks for agents have the last tab, discreet, with their count: not the decision.
  assert.doesNotMatch(
    html,
    /Lexicon|wick<\/strong> carries|id="block-b1"|>Notes</,
  );
  assert.match(
    html,
    /id="view-tab-tilasms".*role="tab" id="view-tab-agents" aria-selected="false" class="discreet"><span class="djinn-eye-perch" data-perch="tab:agents" aria-hidden="true"><\/span>For agents<span class="count">1<\/span><\/button><\/div>/,
  );
  // The answered question and the decision block are in the Decisions tab, apart, with no mark.
  assert.match(
    html,
    /role="tab" id="view-tab-decisions" aria-selected="false" class=""><span class="djinn-eye-perch" data-perch="tab:decisions" aria-hidden="true"><\/span>Decisions<span class="count">2<\/span>/,
  );
  assert.doesNotMatch(html, /Light it tonight\?/);
  // Nothing waits for an answer: no yes/no card waiting.
  assert.doesNotMatch(html, /placeholder="Your answer"/);
  // The bar at the top lists what waits for you: here, the grant, which it links to.
  assert.match(
    html,
    /<nav class="attention-bar" aria-label="What waits for you">/,
  );
  assert.match(html, /1 thing waits for you/);
  assert.match(html, /id="grant-01a11833/);
  // The status language, in words: the wish waits, one task of two is done, a question is being investigated, apart.
  assert.match(html, /count-pill tone-done" title="1 of 2 tasks done"/);
  assert.match(
    html,
    /<h2>Being investigated<span class="count">1<\/span><\/h2>/,
  );
  assert.match(html, /1 question being investigated by the Djinn/);
  // Folded to one line, what you asked with it.
  assert.match(html, /question-card investigating {2}folded/);
  assert.match(html, /You asked: Burn time\?/);
  // No mark on what the wish shows, nor on the folded question; the History tab sits beside Decisions.
  assert.doesNotMatch(html, /Approve as it is|Mark read/);
  assert.match(
    html,
    /role="tab" id="view-tab-history" aria-selected="false" class=""><span class="djinn-eye-perch" data-perch="tab:history" aria-hidden="true"><\/span>History<\/button>/,
  );
});

test("work that merges stays in sight, shows as merging apart from the workers everywhere, then folds once committed", async () => {
  const wishId = "01a11833-a440-7479-a067-52615c91da72";
  const lamp = wish(wishId, "Ship the lamp", s.WishState.ACTIVE, 1);
  const tasks = (state) => [
    {
      id: "t1",
      wishId,
      code: "T1",
      title: "Lay the ground",
      kind: s.TaskKind.AZIMA,
      status: s.TaskStatus.PENDING,
      dependsOn: [],
      proofNeeds: [],
      // No counts from the server: the card counts its parts as the server would.
      azima: { state: s.AzimaState.IN_PROGRESS, ready: true },
    },
    {
      id: "w1",
      wishId,
      code: "W1",
      title: "Dig",
      status: s.TaskStatus.DONE,
      partOf: "t1",
      dependsOn: [],
      createTime: at(1),
      endTime: at(10),
      integration: { state: s.IntegrationState.COMMITTED, branch: "feat/x" },
    },
    {
      id: "w8",
      wishId,
      code: "W8",
      title: "Pour",
      status: s.TaskStatus.DONE,
      partOf: "t1",
      dependsOn: [],
      createTime: at(2),
      endTime: at(20),
      integration: { state, branch: "feat/x" },
    },
    {
      id: "w9",
      wishId,
      code: "W9",
      title: "Build",
      status: s.TaskStatus.PENDING,
      partOf: "t1",
      dependsOn: ["w8"],
      createTime: at(3),
      waitReason: "waits for W8 to be committed",
    },
  ];
  const azimaCard = (list) => {
    const byId = new Map(list.map((x) => [x.id, x]));
    return s.renderToStaticMarkup(
      h(s.AzimaCard, {
        azima: list[0],
        parts: list.slice(1),
        tasks: byId,
        nest: s.mergeNest(list),
        render: (task) =>
          h(s.WishTask, {
            key: task.id,
            task,
            codes: new Map(list.map((x) => [x.id, x.code])),
            onStop() {},
          }),
      }),
    );
  };
  // W8 merges: among the moving parts, in sight, blue; only W1 is behind the fold. W9 links to it.
  const merging = tasks(s.IntegrationState.PENDING);
  const card = azimaCard(merging);
  const fold = card.indexOf('class="fold-line"');
  assert.ok(fold > 0);
  assert.match(card.slice(0, fold), /id="task-w8"/);
  assert.match(card.slice(0, fold), /id="task-w9"/);
  assert.doesNotMatch(card, /id="task-w1"/);
  assert.match(card, />Show 1 finished</);
  assert.match(card, /wish-task tone-running[^"]*" id="task-w8"/);
  assert.match(
    card,
    /waits for <button type="button" class="text-button agent-code task-link">W8<\/button> to be committed/,
  );
  // Its bar: W1 done, W8 running, W9 waiting.
  assert.match(card, /1 done, 1 running, 1 waiting/);
  assert.match(card, /class="azima-bar-running" style="width:33\.3/);
  // Committed, W8 folds with the finished, and runs no more.
  const committed = tasks(s.IntegrationState.COMMITTED);
  const after = azimaCard(committed);
  assert.doesNotMatch(after, /id="task-w8"/);
  assert.match(after, />Show the 2 finished</);
  assert.match(after, /2 done, 0 running, 1 waiting/);

  // The side panel and its icon rail show it merging, apart from the workers that run (W577: a merge waiting is no
  // worker, and the load bar never counted it); the wish is blue all the same, even folded to its icons.
  assert.match(
    sidebar(lamp, merging),
    /aria-label="1 merging: its work waits to merge, no worker runs"/,
  );
  assert.doesNotMatch(sidebar(lamp, merging), /aria-label="1 running"/);
  // Folded, the work that merges lights the blue dot under the wish's icon.
  assert.match(sidebar(lamp, merging, true), /wish-act tone-running/);
  assert.doesNotMatch(sidebar(lamp, committed), /running/);
  assert.doesNotMatch(sidebar(lamp, committed, true), /tone-running/);

  // The head of the wish counts it as running; the flying carpet's glance shows workers only, and none runs for it.
  // Merging, no worker runs for it (W577): its own pill, the wish blue all the same.
  const now = await page(lamp, merging);
  assert.match(
    now.view,
    /count-pill tone-running" title="1 merging: its work waits to merge, no worker runs"/,
  );
  assert.doesNotMatch(now.view, /title="1 running"/);
  assert.doesNotMatch(now.plan, /glance-dot run/);
  assert.doesNotMatch(now.plan, /1 running/);
  const later = await page(lamp, committed);
  assert.doesNotMatch(later.view, /count-pill tone-running/);
  assert.doesNotMatch(later.plan, /glance-dot run/);
});

// An investigator, the worker Enlighten me starts on a question (TaskRole.INVESTIGATOR), is violet while it works, as
// the button that started it, and counted apart from the workers that run, blue: in the side panel's second line, the
// head of the wish, the flying carpet and its task row.
test("an investigator at work shows violet, apart from the running workers, wherever workers are counted", async () => {
  const wishId = "01a14419-ec14-7ab3-837e-8b26cb20e7e1";
  const lamp = wish(wishId, "Ship the lamp", s.WishState.ACTIVE, 1);
  const worker = {
    id: "w1",
    wishId,
    code: "W1",
    title: "Pour",
    status: s.TaskStatus.RUNNING,
    dependsOn: [],
    createTime: at(1),
    startTime: at(2),
  };
  const investigator = {
    id: "w2",
    wishId,
    code: "W2",
    title: "Q1: enlighten",
    status: s.TaskStatus.RUNNING,
    role: s.TaskRole.INVESTIGATOR,
    question: "Q1",
    dependsOn: [],
    createTime: at(3),
    startTime: at(4),
  };
  const both = [worker, investigator];
  const alone = [investigator];
  const pill = (tone, label) =>
    new RegExp(`count-pill tone-${tone}" title="${label}"`);
  const sign = (tone, label) =>
    new RegExp(
      `wish-sign tone-${tone}" title="${label}"[^>]*><span class="wish-sign-dot">`,
    );
  const investigating =
    "1 investigating: a worker finds out more on a question, for you";
  // The second line counts the worker blue and the investigator violet, apart, each a full dot, the worker first.
  const side = sidebar(lamp, both);
  assert.match(side, sign("running", "1 running"));
  assert.match(side, sign("investigating", investigating));
  assert.ok(
    side.indexOf("wish-sign tone-running") <
      side.indexOf("wish-sign tone-investigating"),
  );
  // The investigator alone: violet, and no worker runs.
  const sideAlone = sidebar(lamp, alone);
  assert.match(sideAlone, sign("investigating", investigating));
  assert.doesNotMatch(sideAlone, /tone-running/);
  // Nothing investigates: no violet count.
  assert.doesNotMatch(sidebar(lamp, [worker]), /tone-investigating/);

  // Its task row is violet and says it investigates; the worker's stays blue.
  const row = (task) =>
    s.renderToStaticMarkup(
      h(s.WishTask, { task, codes: new Map(), onStop() {} }),
    );
  assert.match(row(investigator), /wish-task tone-investigating/);
  assert.match(row(investigator), /<span>Investigating<\/span>/);
  assert.match(row(worker), /wish-task tone-running/);

  // The head of the wish and the flying carpet's glance count one running worker, not two; the glance shows the
  // investigator violet.
  const now = await page(lamp, both);
  assert.match(now.view, pill("running", "1 running"));
  assert.doesNotMatch(now.view, /title="2 running"/);
  assert.match(now.plan, /glance-count">1 running · 1 investigating</);
  assert.match(now.plan, /glance-dot investigate/);
  const only = await page(lamp, alone);
  assert.doesNotMatch(only.view, /count-pill tone-running/);
  assert.match(only.plan, /glance-count">1 investigating</);
  assert.doesNotMatch(only.plan, /glance-dot run/);
});

test("a failed merge an agent fixes shows orange, naming its correction, everywhere; red once no correction runs", async () => {
  const wishId = "01a13c45-f3a8-7295-855a-73c0445d7c88";
  const lamp = wish(wishId, "Ship the lamp", s.WishState.ACTIVE, 1);
  // W1's merge conflicted; W2 corrects it, in the state given.
  const tasks = (status) => [
    {
      id: "w1",
      wishId,
      code: "W1",
      title: "Pour",
      status: s.TaskStatus.DONE,
      dependsOn: [],
      createTime: at(1),
      endTime: at(10),
      integration: {
        state: s.IntegrationState.CONFLICT,
        branch: "feat/x",
        reason: "a.go conflicts",
        correctedBy: "w2",
        attempts: 1,
      },
    },
    {
      id: "w2",
      wishId,
      code: "W2",
      title: "Correct the merge of W1",
      status,
      dependsOn: [],
      createTime: at(11),
      correction: {
        attempt: 1,
        failure: { state: s.IntegrationState.CONFLICT, taskIds: ["w1"] },
      },
    },
  ];
  const fixing = tasks(s.TaskStatus.RUNNING);
  const failed = tasks(s.TaskStatus.FAILED);
  const card = (list) =>
    s.renderToStaticMarkup(
      h(s.WishTask, {
        task: list[0],
        codes: new Map(list.map((x) => [x.id, x.code])),
        fixedBy: s.fixerOf(list[0], list)?.code,
        onStop() {},
        async onSend() {},
      }),
    );
  // The card: orange with a wrench, its words and its work line naming W2, a link to its card.
  const now = card(fixing);
  assert.match(now, /wish-task tone-fixing/);
  assert.match(
    now,
    /<span class="status-badge tone-fixing">[\s\S]*?<span>Merge being fixed by W2<\/span><\/span>/,
  );
  assert.match(
    now,
    /work-fixing">Conflict, not committed: a\.go conflicts, being fixed by <button type="button" class="text-button agent-code task-link">W2<\/button><\/p>/,
  );
  assert.doesNotMatch(now, /tone-failed|Merge failed/);
  // W2 failed: no correction runs, red as before.
  const red = card(failed);
  assert.match(red, /wish-task tone-failed/);
  assert.match(red, /<span>Merge failed<\/span>/);
  assert.match(
    red,
    /work-failed">Conflict, not committed: a\.go conflicts, corrected by W2</,
  );

  // The side panel counts it apart, orange.
  const detail = "1 merge being fixed: an agent corrects it, nothing to do";
  assert.match(
    sidebar(lamp, fixing),
    new RegExp(
      `wish-sign tone-fixing" title="${detail}"[^>]*><svg[^>]*lucide-wrench`,
    ),
  );
  assert.doesNotMatch(sidebar(lamp, failed), /tone-fixing/);

  // The head of the wish, and the flying carpet.
  const live = await page(lamp, fixing);
  assert.match(
    live.view,
    new RegExp(`count-pill tone-fixing" title="${detail}"`),
  );
  assert.match(live.view, /<b>1<\/b>being fixed<\/span>/);
  // The flying carpet's glance shows its correction at work apart, a repair worker: a wrench in place of its dot,
  // blue as any worker at work, saying what it repairs (W137).
  assert.match(
    live.plan,
    /<button type="button" class="glance-row glance-repair run" title="Running · W2 Correct the merge of W1"><span class="glance-mark run" role="img" aria-label="repair worker · Running"[^>]*><svg[^>]*lucide-wrench[\s\S]*?<span class="glance-code">W2<\/span><span class="glance-title">settles the conflict of W1<span class="glance-after">Running<\/span>/,
  );
  assert.doesNotMatch(live.plan, /glance-dot run/);
  assert.match(live.plan, /glance-count">1 repair</);
  const dead = await page(lamp, failed);
  assert.doesNotMatch(dead.view, /tone-fixing/);
  assert.doesNotMatch(dead.plan, /tone-fixing|glance-dot run|glance-repair/);
});

test("an open question that can wait stays visible in WishView's questions column, never folded, and out of the attention bar", async () => {
  const wishId = "01a11833-a440-7479-a067-52615c91da70";
  const lamp = wish(wishId, "Djinn sur Wails", s.WishState.ACTIVE, 1);
  const transport = s.createRouterTransport(({ service }) => {
    service(s.WishService, {
      list: () => ({ wishes: [lamp] }),
      watch: () => new ReadableStream(),
    });
    service(s.ProjectService, {
      list: () => ({ projects: [] }),
    });
    service(s.TaskService, {
      list: () => ({ tasks: [], total: 0 }),
    });
    service(s.QuestionService, {
      list: () => ({
        questions: [
          {
            id: "q86",
            code: "Q86",
            text: "The push checks of feat/wails-go stay red. What should Djinn do?",
            options: ["Check again", "Push unchecked", "Leave held"],
            before: "",
            wishId,
          },
        ],
        total: 1,
      }),
    });
    service(s.BlockService, {
      list: () => ({ blocks: [], total: 0 }),
    });
    service(s.InboxService, {
      list: () => ({ items: [] }),
      sources: () => ({ sources: [] }),
    });
  });
  const djinn = s.createDjinn(transport, 10);
  const close = djinn.store.open(wishId);
  await djinn.store.changed(wishId, [s.Change.QUESTION]);
  close();
  const html = s.renderToStaticMarkup(
    h(s.DjinnProvider, { djinn }, h(s.WishView, { wish: lamp, onToast() {} })),
  );

  // 1. Nothing holds up the wish: no attention bar.
  assert.doesNotMatch(html, /attention-bar|attention-item/);

  // 2. The questions column shows it, open as the first, with the can-wait tone; no card holds the column.
  assert.doesNotMatch(html, /action-center/);
  assert.match(html, /class="question-column decisions-section"/);
  assert.match(html, /question-card question-row open current can-wait/);
  assert.match(html, /<span class="question-id">Q86<\/span>/);
  assert.match(html, /status-badge tone-later[^>]*>.*<span>Can wait<\/span>/);
  assert.match(html, /The push checks of feat\/wails-go stay red/);

  // 3. Question options and actions are rendered, not folded out of sight.
  assert.match(html, /Check again/);
  assert.doesNotMatch(html, /question-fold/);
  assert.doesNotMatch(html, /question-card[^"]*folded/);
});

test("a card does not re-render when another card changes in a list", async () => {
  const container = setupMockDom();
  const wishId = "01a118fa-4308-736a-9ff9-5fc373a4294f";
  const activeWish = wish(wishId, "Ship the lamp", s.WishState.ACTIVE, 1);

  // 1. In WishView, when question Q02 changes, Q01 does not re-render.
  let questions = [
    { id: "q1", wishId, code: "Q01", text: "Question 1", options: ["A", "B"] },
    { id: "q2", wishId, code: "Q02", text: "Question 2", options: ["X", "Y"] },
  ];
  const transport = s.createRouterTransport(({ service }) => {
    service(s.WishService, {
      list: () => ({ wishes: [activeWish] }),
      describe: () => ({ wish: activeWish }),
      watch: async function* () {},
    });
    service(s.TaskService, { list: () => ({ tasks: [] }) });
    service(s.QuestionService, { list: () => ({ questions }) });
    service(s.BlockService, { list: () => ({ blocks: [] }) });
  });

  const djinn = s.createDjinn(transport, 10);
  const close = djinn.store.open(wishId);
  await djinn.store.changed(wishId, [s.Change.WISH]);

  const questionRenders = {};
  const origQuestionType = s.WishQuestion.type;
  s.WishQuestion.type = function SpiedWishQuestion(props) {
    questionRenders[props.question.id] =
      (questionRenders[props.question.id] || 0) + 1;
    return origQuestionType(props);
  };

  const root = s.createRoot(container);
  s.act(() => {
    root.render(
      h(
        s.DjinnProvider,
        { djinn },
        h(s.WishView, { wish: activeWish, onToast() {} }),
      ),
    );
  });

  assert.equal(questionRenders.q1, 1, "Q1 renders once initially");
  assert.equal(questionRenders.q2, 1, "Q2 renders once initially");

  // Modify question 2 and notify the store.
  questions = [
    { id: "q1", wishId, code: "Q01", text: "Question 1", options: ["A", "B"] },
    {
      id: "q2",
      wishId,
      code: "Q02",
      text: "Question 2 modified",
      options: ["X", "Y"],
    },
  ];
  await s.act(async () => {
    await djinn.store.changed(wishId, [s.Change.QUESTION]);
  });

  assert.equal(questionRenders.q1, 1, "Q1 does not re-render when Q2 changes");
  assert.equal(questionRenders.q2, 2, "Q2 re-renders when Q2 changes");
  s.WishQuestion.type = origQuestionType;
  close();

  // 2. In TaskSections, when task T02 changes, T01 does not re-render.
  const taskRenders = {};
  const origTaskType = s.WishTask.type;
  s.WishTask.type = function SpiedWishTask(props) {
    taskRenders[props.task.id] = (taskRenders[props.task.id] || 0) + 1;
    return origTaskType(props);
  };

  const task1 = {
    id: "t1",
    wishId,
    code: "W1",
    title: "Task 1",
    status: s.TaskStatus.RUNNING,
  };
  const task2 = {
    id: "t2",
    wishId,
    code: "W2",
    title: "Task 2",
    status: s.TaskStatus.RUNNING,
  };

  const onStop = () => {};
  const onSend = async () => {};
  const renderTask = (t) =>
    h(s.WishTask, { key: t.id, task: t, onStop, onSend });

  const taskRoot = s.createRoot(setupMockDom());
  s.act(() => {
    taskRoot.render(
      h(s.TaskSections, {
        moving: [task1, task2],
        azimas: [],
        doneAzimas: [],
        render: renderTask,
      }),
    );
  });

  assert.equal(taskRenders.t1, 1, "T1 renders once initially");
  assert.equal(taskRenders.t2, 1, "T2 renders once initially");

  const task2Updated = { ...task2, status: s.TaskStatus.DONE };
  s.act(() => {
    taskRoot.render(
      h(s.TaskSections, {
        moving: [task1, task2Updated],
        azimas: [],
        doneAzimas: [],
        render: renderTask,
      }),
    );
  });

  assert.equal(taskRenders.t1, 1, "T1 does not re-render when T2 changes");
  assert.equal(taskRenders.t2, 2, "T2 re-renders when T2 changes");
  s.WishTask.type = origTaskType;
  s.act(() => {
    root.unmount();
    taskRoot.unmount();
  });

  for (const h of process._getActiveHandles()) {
    if (
      h &&
      typeof h.unref === "function" &&
      h.constructor?.name === "MessagePort"
    ) {
      h.unref();
    }
  }
});

// Nothing moves under the reader (AGENTS.md, Interface): on a wish's page, a question asked later renders after the
// older ones, even one a task waits for, and a revision keeps the question in its place, open, dated in grey.
test("a new question renders after the older ones, a revision keeps the order and the open question", async () => {
  const container = setupMockDom();
  const wishId = "01a1443f-0000-7000-8000-000000000047";
  const lamp = wish(wishId, "Ship the lamp", s.WishState.ACTIVE, 1);
  const at = (iso) => ({ seconds: BigInt(Date.parse(iso) / 1000), nanos: 0 });
  const asked = (id, code, text, iso, extra = {}) => ({
    id,
    wishId,
    code,
    text,
    options: ["Yes", "No"],
    createTime: at(iso),
    ...extra,
  });
  let questions = [
    asked("q1", "Q01", "Brass?", "2026-10-16T10:24:00Z"),
    asked("q2", "Q02", "Wick?", "2026-10-16T10:25:00Z"),
  ];
  let tasks = [];
  const transport = s.createRouterTransport(({ service }) => {
    service(s.WishService, {
      list: () => ({ wishes: [lamp] }),
      describe: () => ({ wish: lamp }),
      watch: async function* () {},
    });
    service(s.TaskService, { list: () => ({ tasks }) });
    service(s.QuestionService, { list: () => ({ questions }) });
    service(s.BlockService, { list: () => ({ blocks: [] }) });
  });
  const djinn = s.createDjinn(transport, 10);
  const close = djinn.store.open(wishId);
  await djinn.store.changed(wishId, [s.Change.WISH]);
  const root = s.createRoot(container);
  s.act(() => {
    root.render(
      h(
        s.DjinnProvider,
        { djinn },
        h(s.WishView, { wish: lamp, onToast() {} }),
      ),
    );
  });
  const nodes = (node, out = []) => {
    out.push(node);
    for (const child of node.childNodes ?? []) nodes(child, out);
    return out;
  };
  const textOf = (node) =>
    node.nodeType === 3
      ? node.nodeValue
      : node.childNodes.length
        ? node.childNodes.map(textOf).join("")
        : (node.textContent ?? "");
  const cards = () =>
    nodes(container).filter((n) => /^question-/.test(n.attributes?.id ?? ""));
  const ids = () => cards().map((n) => n.attributes.id.slice(9));
  const open = () =>
    cards()
      .filter((n) => / current /.test(` ${n.attributes.class} `))
      .map((n) => n.attributes.id.slice(9));
  const date = (id) =>
    textOf(
      nodes(cards().find((n) => n.attributes.id === `question-${id}`)).find(
        (n) => n.attributes?.class === "question-asked",
      ),
    );
  assert.deepEqual(ids(), ["q1", "q2"]);
  assert.deepEqual(open(), ["q1"]);
  assert.match(date("q1"), /^asked \d\d\/\d\d at \d\d:\d\d( [AP]M)?$/);

  // Asked later, a question a task waits for, needed before the merge: it still comes last.
  questions = [
    ...questions,
    asked("q3", "Q03", "May W1 edit?", "2026-10-16T10:26:00Z", {
      before: "before the merge",
    }),
  ];
  tasks = [
    {
      id: "t1",
      wishId,
      code: "W1",
      title: "Polish",
      status: s.TaskStatus.WAITING,
      editQuestionId: "q3",
    },
  ];
  await s.act(async () => {
    await djinn.store.changed(wishId, [s.Change.QUESTION, s.Change.TASK]);
  });
  assert.deepEqual(ids(), ["q1", "q2", "q3"]);
  assert.deepEqual(open(), ["q1"]);

  // Revised, the first question keeps its place, open, its date saying when.
  questions = [
    asked("q1", "Q01", "Brass or copper?", "2026-10-16T10:24:00Z", {
      revision: 1,
      rounds: [
        { kind: s.RoundKind.REVISE, createTime: at("2026-10-16T10:31:00Z") },
      ],
    }),
    ...questions.slice(1),
  ];
  await s.act(async () => {
    await djinn.store.changed(wishId, [s.Change.QUESTION]);
  });
  assert.deepEqual(ids(), ["q1", "q2", "q3"]);
  assert.deepEqual(open(), ["q1"]);
  assert.match(
    date("q1"),
    /^asked \d\d\/\d\d at \d\d:\d\d( [AP]M)? · revised at \d\d:\d\d( [AP]M)?$/,
  );
  s.act(() => root.unmount());
  close();
  // React's scheduler port would keep the run alive, as above.
  for (const handle of process._getActiveHandles())
    if (handle?.constructor?.name === "MessagePort") handle.unref();
});

// History tab (L27, Q81, W261): sits beside Tasks and Decisions, shows the wish commands and the lead journal
// for that wish, and its count matches only what its tab shows (never BlockService's total across blocks).
test("History tab shows the wish commands and lead journal for that wish and counts only its entries", async () => {
  const container = setupMockDom();
  const wishId = "01a14ba9-36fd-7e8a-92b9-3ffb1b873300";
  const lamp = wish(wishId, "Ship the lamp", s.WishState.ACTIVE, 1);
  const blocks = [
    { id: "b1", wishId, kind: "decision", title: "Pick brass", content: "Yes" },
    { id: "b2", wishId, kind: "report", title: "Audit", content: "Clean" },
    { id: "b3", wishId, kind: "section", title: "Plan", content: "Steps" },
  ];
  const commands = [
    {
      id: "c1",
      at: at(1000),
      method: "/plan.v1.WishService/Make",
    },
    {
      id: "c2",
      at: at(2000),
      method: "/plan.v1.QuestionService/Answer",
    },
  ];
  const leadBatches = [
    {
      id: "lb1",
      startTime: at(1200),
      endTime: at(1800),
      agent: "claude",
      model: "claude-opus-4-6",
      sessionId: "sess-abc123456789",
      wishIds: [wishId],
      events: ["wish Created: Ship the lamp"],
      commands: ["task spawn", "question ask"],
      note: "Spawned W1 and asked Q1; waiting on Q1 before W2.",
      usage: {
        inputTokens: 1000n,
        outputTokens: 200n,
        costUsd: 0.042,
        costSource: "reported",
      },
    },
  ];
  const transport = s.createRouterTransport(({ service }) => {
    service(s.WishService, {
      list: () => ({ wishes: [lamp] }),
      describe: () => ({ wish: lamp }),
      snapshot: () => ({ export: { wish: lamp, commands, leadBatches } }),
      watch: async function* () {},
    });
    service(s.TaskService, { list: () => ({ tasks: [] }) });
    service(s.QuestionService, { list: () => ({ questions: [] }) });
    service(s.BlockService, {
      list: () => ({ blocks, total: 40 }),
    });
  });
  const djinn = s.createDjinn(transport, 10);
  const close = djinn.store.open(wishId);
  await djinn.store.changed(wishId, [s.Change.WISH]);
  const root = s.createRoot(container);
  try {
    s.act(() => {
      root.render(
        h(
          s.DjinnProvider,
          { djinn },
          h(s.WishView, { wish: lamp, onToast() {} }),
        ),
      );
    });
    const nodes = (node, out = []) => {
      out.push(node);
      for (const child of node.childNodes ?? []) nodes(child, out);
      return out;
    };
    const textOf = (node) =>
      node.nodeType === 3
        ? node.nodeValue
        : node.childNodes.length
          ? node.childNodes.map(textOf).join("")
          : (node.textContent ?? "");
    const props = (el) =>
      el[Object.keys(el).find((k) => k.startsWith("__reactProps$"))];
    const settle = async () => {
      for (let i = 0; i < 4; i++)
        await s.act(() => new Promise((r) => setTimeout(r, 0)));
    };
    const historyTab = () =>
      nodes(container).find((n) => n.attributes?.id === "view-tab-history");
    const countNode = () =>
      nodes(historyTab()).find((n) =>
        /\bcount\b/.test(n.attributes?.class ?? ""),
      );
    const section = () =>
      nodes(container).find((n) =>
        /\bwish-journal\b/.test(n.attributes?.class ?? ""),
      );
    const rows = () =>
      nodes(section()).filter(
        (n) =>
          n.tagName === "TR" &&
          !/\blead-journal-note-row\b/.test(n.attributes?.class ?? ""),
      );

    // Before opening History (with 40 total blocks): tab says "History" with no lying count.
    assert.ok(historyTab(), "History tab sits beside Tasks and Decisions");
    assert.equal(textOf(historyTab()), "History");
    assert.equal(countNode(), undefined);
    assert.equal(section(), undefined);

    // Selecting History fetches the snapshot and shows both the lead journal and the wish commands.
    s.act(() => props(historyTab()).onClick());
    await settle();
    assert.ok(section(), "History tab renders .wish-journal");
    assert.equal(rows().length, 3);
    assert.equal(textOf(countNode()), "3");
    assert.match(textOf(section()), /claude-opus-4-6/);
    assert.match(textOf(section()), /sess-abc/);
    assert.match(textOf(section()), /task spawnquestion ask/);
    assert.match(
      textOf(section()),
      /Spawned W1 and asked Q1; waiting on Q1 before W2\./,
    );
    assert.match(textOf(section()), /\$0\.042/);
    assert.match(textOf(section()), /question answer/);
    assert.match(textOf(section()), /wish make/);
  } finally {
    s.act(() => root.unmount());
    close();
    for (const handle of process._getActiveHandles())
      if (handle?.constructor?.name === "MessagePort") handle.unref();
  }
});
