// The questions column (src/question-column.tsx): its groups, its keys, the questions asleep. Run with `go tool task
// test-ui`.
import test from "node:test";
import assert from "node:assert/strict";

import { screens, wish } from "./screens.mjs";

const { s, h, live } = await screens();

// The questions column (Decision Q08, mockup L04 v2): grouped by wish on the flying carpet, one open at a time, the
// arrows from one to the next across the groups, stopping at both ends.
test("the questions column groups the open questions by wish, the first open in full, the others two-line rows", () => {
  const lamp = wish("w1", "Ship the lamp", s.WishState.ACTIVE, 1);
  const wick = wish("w2", "Trim the wick", s.WishState.ACTIVE, 2);
  const calm = wish("w3", "Polish the brass", s.WishState.ACTIVE, 3);
  const open = (w, id, code, text, extra = {}) => ({
    wish: w,
    item: {
      id,
      code,
      text,
      options: ["Olive", "Paraffin"],
      recommendation: "B: brighter.",
      rounds: [],
      revision: 0,
      wishId: w.id,
      ...extra,
    },
    blocking: [],
  });
  // As the flying carpet orders them, the most urgent first: the groups follow the wishes' rank all the same.
  const long = `## Cost\n\n${"Olive is dearer. ".repeat(40)}\n\n## Versions\n\n### Round 0\n\nThe first ask.\n\n| A | B |\n| - | - |\n| 1 | 2 |\n\n\`\`\`sh\ngo tool task test\n\`\`\``;
  const questions = [
    open(wick, "q3", "Q03", "Which wick?", { context: long }),
    open(lamp, "q1", "Q01", "Which oil?", { context: long }),
    open(lamp, "q2", "Q02", "Which glass?", { revision: 2 }),
  ];
  const groups = s.questionGroups([lamp, wick, calm], questions);
  assert.deepEqual(
    groups.map((g) => [g.wish.id, g.questions.map((q) => q.item.code)]),
    [
      ["w1", ["Q01", "Q02"]],
      ["w2", ["Q03"]],
    ],
  );
  const html = s.renderToStaticMarkup(
    h(s.QuestionColumn, {
      groups,
      headings: true,
      onAnswer: async () => {},
      onEnlighten: async () => {},
      onMove() {},
      onWithdraw() {},
    }),
  );
  // No card: a section on the page's ground, its label, then a light heading per wish with its count.
  assert.match(
    html,
    /^<section class="question-column decisions-section" id="questions" aria-label="Questions"><h2 class="question-column-label">Questions<\/h2>/,
  );
  assert.doesNotMatch(html, /action-center/);
  const heads = [
    ...html.matchAll(
      /<span class="name">([^<]*)<\/span><span class="question-group-count">(\d)/g,
    ),
  ];
  assert.deepEqual(
    heads.map((m) => [m[1], m[2]]),
    [
      ["Ship the lamp", "2"],
      ["Trim the wick", "1"],
    ],
  );
  // The first question is open, in full: its move icon beside the title, the whole context (no Read more), its table and its
  // code block with Copy, the recommendation, the lettered options, the note and the three gestures.
  const rows = html.split('<article id="question-').slice(1);
  assert.equal(rows.length, 3);
  assert.match(
    rows[0],
    /^q1" class="question-card question-row open current can-wait" tabindex="0" aria-current="true"/,
  );
  assert.match(
    rows[0],
    /<\/p><\/div><div class="question-corner"><button class="question-corner-button question-move" type="button" aria-label="Move to another wish"><svg[^>]*>(?:(?!<\/svg>).)*<\/svg><\/button><\/div><div class="question-open">/,
  );
  assert.doesNotMatch(rows[0], /read-more|Read more/);
  assert.match(rows[0], /<h2>Versions<\/h2>\s*<h3>Round 0<\/h3>/);
  assert.match(rows[0], /<table>/);
  assert.match(rows[0], /class="ac-copy">Copy<\/button>/);
  assert.equal((rows[0].match(/Olive is dearer\./g) ?? []).length, 40);
  assert.ok(
    rows[0].indexOf("What is at stake") < rows[0].indexOf("Recommendation · B"),
  );
  assert.ok(
    rows[0].indexOf("Recommendation · B") <
      rows[0].indexOf('option-letter">A<'),
  );
  assert.ok(
    rows[0].indexOf('option-letter">B<') < rows[0].indexOf("<textarea"),
  );
  assert.ok(
    rows[0].indexOf("<textarea") < rows[0].indexOf("Withdraw question…"),
  );
  assert.ok(
    rows[0].indexOf("Withdraw question…") <
      rows[0].indexOf(">Enlighten me<kbd"),
  );
  assert.ok(
    rows[0].indexOf(">Enlighten me<kbd") < rows[0].indexOf("To the lead"),
  );
  // The others are closed: their code, their title, how much they hold up, out of the tab order; no body. Each has a
  // perch for the Djinn's eye, empty, hidden from screen readers.
  assert.match(
    rows[1],
    /^q2" class="question-card question-row closed can-wait" tabindex="-1"[^>]*><span class="djinn-eye-perch" data-perch="q:q2" aria-hidden="true"><\/span><span class="question-id">Q02<\/span><div class="question-row-head"><h3 class="question-row-title">Which glass\?<\/h3><p class="question-row-meta"><span class="status-badge tone-later">.*Can wait<\/span><\/span><span>2 options<\/span><span class="revised-badge">Revised ×2<\/span><\/p><\/div><\/article>/,
  );
  assert.doesNotMatch(
    rows[1] + rows[2],
    /<textarea|question-open|Olive is dearer/,
  );

  // On a wish's page: its own questions, no heading; none, a line says so.
  const page = s.renderToStaticMarkup(
    h(s.QuestionColumn, {
      groups: s.questionGroups([lamp], questions.slice(1)),
      onAnswer: async () => {},
    }),
  );
  assert.doesNotMatch(page, /question-group-head/);
  assert.equal((page.match(/<article/g) ?? []).length, 2);
  const none = s.renderToStaticMarkup(
    h(s.QuestionColumn, { groups: [], onAnswer: async () => {} }),
  );
  // None open: the session's drawing above its sentence, the fact first (empty-art.tsx); the first without storage.
  assert.match(
    none,
    /<div class="empty-state question-none"><svg class="empty-art empty-art-column" viewBox="0 0 200 130" fill="none" aria-hidden="true">.*<\/svg><p class="empty-state-text">No open question: nothing to ask you for now\.<\/p><\/div>/,
  );
  // Nothing waits and nothing runs: the Djinn asleep, and the sentence that says so.
  const asleep = s.renderToStaticMarkup(
    h(s.QuestionColumn, { groups: [], calm: true, onAnswer: async () => {} }),
  );
  assert.match(
    asleep,
    /<p class="empty-state-text">Nothing waits for you, and nothing runs: I rest above my lamp\.<\/p>/,
  );
  assert.doesNotMatch(asleep, /No open question/);
});

test("the questions column's keys: up and down across the groups stop at both ends, Escape leaves, the heir of an answered one", () => {
  const order = ["q1", "q2", "q3"];
  // Down and up, one at a time, from one wish to the next; nothing moves past either end.
  assert.equal(s.stepTo(order, "q1", 1), "q2");
  assert.equal(s.stepTo(order, "q2", 1), "q3");
  assert.equal(s.stepTo(order, "q3", 1), "q3");
  assert.equal(s.stepTo(order, "q2", -1), "q1");
  assert.equal(s.stepTo(order, "q1", -1), "q1");
  // From none open (Escape left the column), Down comes back to the first, Up to the last.
  assert.equal(s.stepTo(order, undefined, 1), "q1");
  assert.equal(s.stepTo(order, undefined, -1), "q3");
  assert.equal(s.stepTo([], undefined, 1), undefined);
  // The keys the column takes for itself: the arrows, Home and End, Escape; with a modifier, none.
  const key = (k, mods = {}) =>
    s.columnMove({
      key: k,
      ctrlKey: false,
      metaKey: false,
      shiftKey: false,
      altKey: false,
      ...mods,
    });
  assert.equal(key("ArrowDown"), "down");
  assert.equal(key("ArrowUp"), "up");
  assert.equal(key("Home"), "first");
  assert.equal(key("End"), "last");
  assert.equal(key("Escape"), "leave");
  assert.equal(key("ArrowDown", { shiftKey: true }), undefined);
  assert.equal(key("ArrowDown", { ctrlKey: true }), undefined);
  assert.equal(key("ArrowRight"), undefined);
  assert.equal(key(" "), undefined);
  assert.equal(key("PageDown"), undefined);
  // Answered, withdrawn or moved: the one below opens, else the one above; none left, none.
  assert.equal(s.heir(order, ["q1", "q3"], "q2"), "q3");
  assert.equal(s.heir(order, ["q1", "q2"], "q3"), "q2");
  assert.equal(s.heir(order, ["q3"], "q1"), "q3");
  assert.equal(s.heir(order, [], "q2"), undefined);
});

// W59: with no question focused (the focus on the page, or nowhere), the first arrow gives the focus to the open
// question and moves nothing; the next one, from the focused question, moves. None open, the first for Down, the last
// for Up, opens.
test("the questions column's first arrow takes the focus, the second moves it", () => {
  const order = ["q1", "q2", "q3"];
  assert.deepEqual(s.arrowTo(order, "q2", 1, false), { id: "q2", move: false });
  assert.deepEqual(s.arrowTo(order, "q2", -1, false), {
    id: "q2",
    move: false,
  });
  assert.deepEqual(s.arrowTo(order, "q2", 1, true), { id: "q3", move: true });
  assert.deepEqual(s.arrowTo(order, "q2", -1, true), { id: "q1", move: true });
  assert.deepEqual(s.arrowTo(order, "q3", 1, true), { id: "q3", move: false });
  assert.deepEqual(s.arrowTo(order, undefined, 1, false), {
    id: "q1",
    move: true,
  });
  assert.deepEqual(s.arrowTo(order, undefined, -1, false), {
    id: "q3",
    move: true,
  });
  assert.equal(s.arrowTo([], undefined, 1, false), undefined);
});

test("an open question of the column: letters and digits pick, Enter goes to the note, Escape clears the pick, then leaves it to the column", async () => {
  const answered = [];
  const selected = [];
  const keys = { current: undefined };
  const question = {
    id: "q1",
    code: "Q01",
    text: "Which oil?",
    options: ["Olive", "Paraffin", "Canola"],
    recommendation: "A: it smells good.",
    rounds: [],
  };
  const card = live(s.WishQuestion, {
    question,
    current: true,
    onSelect: (id) => selected.push(id),
    keys,
    onAnswer: async (choice, note) => {
      answered.push({ choice, note });
    },
  });
  const selectedLetter = () =>
    card
      .html()
      .match(
        /<div class="option selected"[^>]*><strong class="option-letter">(\w)</,
      )?.[1];
  const press = (key, more = {}) => {
    let prevented = false;
    card.one("question-card").onKeyDown({
      key,
      ctrlKey: false,
      metaKey: false,
      shiftKey: false,
      altKey: false,
      target: null,
      currentTarget: null,
      preventDefault: () => {
        prevented = true;
      },
      ...more,
    });
    return prevented;
  };
  // The recommended option is selected, as today: the lamp rubs it.
  assert.equal(selectedLetter(), "A");
  // A letter, in either case, or a digit picks its option; nothing leaves.
  assert.ok(press("b"));
  assert.equal(selectedLetter(), "B");
  assert.ok(press("C"));
  assert.equal(selectedLetter(), "C");
  assert.ok(press("2"));
  assert.equal(selectedLetter(), "B");
  // No such option, Shift, or a modifier alone: nothing.
  assert.equal(press("D"), false);
  assert.equal(press("9"), false);
  assert.equal(press("A", { shiftKey: true }), false);
  assert.equal(selectedLetter(), "B");
  // The arrows are the column's: the card leaves them.
  assert.equal(press("ArrowDown"), false);
  assert.equal(press("ArrowRight"), false);
  assert.equal(selectedLetter(), "B");
  await card.settle();
  assert.deepEqual(answered, []);
  // Enter on the question puts the caret in the note: it answers nothing.
  assert.ok(press("Enter"));
  await card.settle();
  assert.deepEqual(answered, []);
  // Escape in the note gives the focus back to the question.
  let back = false;
  card.one("textarea").onKeyDown({
    key: "Escape",
    preventDefault: () => {
      back = true;
    },
  });
  assert.ok(back);
  // Escape on the question clears your pick: the recommendation is selected again; once none is yours, the column
  // takes Escape (the focus leaves it).
  assert.ok(press("Escape"));
  assert.equal(selectedLetter(), "A");
  assert.equal(press("Escape"), false);
  // Ctrl+Enter rubs the lamp on the selected option, as today; Enter in the note too.
  assert.ok(press("Enter", { ctrlKey: true }));
  await card.settle();
  assert.deepEqual(answered, [{ choice: s.Choice.A, note: "" }]);
  press("c");
  card.one("textarea").onChange({ target: { value: "Pressed today" } });
  card.one("textarea").onKeyDown({ key: "Enter", preventDefault() {} });
  await card.settle();
  assert.deepEqual(answered[1], { choice: s.Choice.C, note: "Pressed today" });

  // With no option recommended, the lamp waits for a pick, as today.
  const vague = live(s.WishQuestion, {
    question: {
      ...question,
      id: "q2",
      code: "Q02",
      recommendation: "Both burn.",
    },
    current: true,
    onSelect() {},
    keys,
    onAnswer: async () => assert.fail("nothing picked"),
  });
  assert.match(vague.html(), /title="Pick an option first" disabled=""/);
  vague.one("question-card").onKeyDown({
    key: "Enter",
    ctrlKey: true,
    target: null,
    preventDefault() {},
  });
  await vague.settle();
  assert.match(
    vague.html(),
    /<p class="question-hint unpicked" role="alert">.*Pick an option first<\/p>/,
  );

  // Closed, it is its row: a click or the focus opens it, and the pick you made shows.
  card.update({ current: false });
  const closed = card.html();
  assert.match(closed, /question-row closed/);
  assert.match(closed, /<span class="question-row-picked">Choice C<\/span>/);
  assert.doesNotMatch(closed, /<textarea|question-open/);
  const row = card.one("question-card");
  assert.equal(row.onKeyDown, undefined);
  row.onClick();
  const self = {};
  row.onFocus({ target: self, currentTarget: self });
  assert.deepEqual(selected, ["q1", "q1"]);
});

test("the questions asleep are a discreet count below the column, their list on demand, each with what it waits for and Wake", () => {
  const lamp = wish("w1", "Ship the lamp", s.WishState.ACTIVE, 1);
  const asleep = {
    id: "q3",
    code: "Q03",
    wishId: "w1",
    text: "Release now?",
    options: [],
    rounds: [],
    createTime: { seconds: 1_760_000_000n, nanos: 0 },
    sleep: {
      conditions: [
        { kind: s.SleepKind.TASK, code: "W12", taskId: "t12", url: "" },
        { kind: s.SleepKind.MERGED, code: "W45", taskId: "t45", url: "" },
        {
          kind: s.SleepKind.MERGED,
          code: "",
          taskId: "",
          url: "https://github.com/acme/lamp/pull/7",
        },
      ],
      createTime: { seconds: 1_760_000_000n, nanos: 0 },
    },
  };
  const woken = [];
  const list = live(s.AsleepQuestions, {
    questions: [{ wish: lamp, item: asleep, blocking: [] }],
    headings: true,
    onWake: async (q) => woken.push(q.code),
  });
  assert.match(
    list.html(),
    /^<div class="question-asleep"><button type="button" class="question-asleep-toggle" aria-expanded="false"><svg[^>]*lucide-moon[^>]*>(?:(?!<\/svg>).)*<\/svg>1 question asleep<\/button><\/div>$/,
  );
  list.one("question-asleep-toggle").onClick();
  const html = list.html();
  assert.match(html, /aria-expanded="true"/);
  assert.match(
    html,
    /<li id="asleep-q3"><span class="question-id">Q03<\/span><span class="question-asleep-text">Release now\?<span class="question-asleep-wish">Ship the lamp<\/span><span class="question-asleep-until">until W12 over · W45&#x27;s request merged · https:\/\/github.com\/acme\/lamp\/pull\/7 merged<\/span><\/span><button type="button" class="button secondary question-wake">Wake<\/button><\/li>/,
  );
  list.one("question-wake").onClick();
  assert.deepEqual(woken, ["Q03"]);
  // None asleep: nothing at all.
  assert.equal(
    s.renderToStaticMarkup(h(s.AsleepQuestions, { questions: [] })),
    "",
  );
  // In the column, below its questions; none open, the line still says so.
  const column = s.renderToStaticMarkup(
    h(s.QuestionColumn, {
      groups: [],
      asleep: [{ wish: lamp, item: asleep, blocking: [] }],
      onAnswer: async () => {},
    }),
  );
  assert.match(
    column,
    /<p class="empty-state-text">No open question: nothing to ask you for now\.<\/p><\/div><div class="question-asleep">/,
  );
});
