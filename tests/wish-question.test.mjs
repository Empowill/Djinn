// A question's card (src/wish-question.tsx): its options, its gestures and keys, its links, its answer; the decision
// log of a launched plan. Run with `go tool task test-ui`.
import test from "node:test";
import assert from "node:assert/strict";

import { screens } from "./screens.mjs";

const { s, h, live } = await screens();

test("a revised question's earlier versions take no room: Versions (3) in its corner opens them below it, to read", () => {
  // Its context is its current version only (W114); each revision's round keeps the version it replaced.
  const question = {
    id: "q1",
    code: "Q01",
    text: "Which oil?",
    options: ["Olive", "Paraffin"],
    recommendation: "A: it smells good.",
    context: "The lamp smokes.",
    revision: 2,
    rounds: [
      { kind: s.RoundKind.ENLIGHTEN, note: "Burn time?" },
      {
        kind: s.RoundKind.REVISE,
        context: "The lamp smoked a little.",
        options: ["Olive", "Canola"],
        recommendation: "B: it is cheap.",
      },
      {
        kind: s.RoundKind.REVISE,
        context: "The lamp smoked more.",
        options: ["Olive", "Paraffin"],
        recommendation: "B: brighter.",
      },
    ],
  };
  const row = live(s.WishQuestion, {
    question,
    current: true,
    onSelect() {},
    onAnswer: async () => {},
  });
  const card = live(s.WishQuestion, {
    question,
    expanded: true,
    onAnswer: async () => {},
  });
  for (const view of [row, card]) {
    const closed = view.html();
    assert.match(closed, /The lamp smokes\./);
    assert.match(
      closed,
      /<div class="question-corner"><button type="button" class="question-corner-button question-versions" title="The earlier versions of this question, to read" aria-expanded="false"><svg[^>]*lucide-history[^>]*>.*?<\/svg>Versions \(3\)<\/button>/,
    );
    // Closed, the earlier versions take no room, and no history is left in the context.
    assert.doesNotMatch(
      closed,
      /smoked|Earlier versions|question-versions-list|Historique|Previous versions|question-rounds/,
    );
    view.one("question-versions").onClick();
    const open = view.html();
    assert.match(
      open,
      /question-versions is-pressed" [^>]*aria-expanded="true"/,
    );
    // The current version first, then the earlier ones, the last one first, each with what was asked about it.
    assert.match(
      open,
      /The lamp smokes\..*<h4>Earlier versions<\/h4><ol reversed=""><li>.*Version 2.*revised .*The lamp smoked more\..*<li>Olive<\/li><li>Paraffin<\/li>.*Recommended: B: brighter\..*<\/li><li>.*Version 1.*asked .*The lamp smoked a little\..*<li>Canola<\/li>.*Recommended: B: it is cheap\..*Asked to investigate: Burn time\?/s,
    );
    // To read only: nothing restores a version.
    assert.doesNotMatch(open, /Restore/);
  }
  // Never revised: no Versions button.
  const plain = s.renderToStaticMarkup(
    h(s.WishQuestion, {
      question: { ...question, revision: 0, rounds: [] },
      expanded: true,
      onAnswer: async () => {},
    }),
  );
  assert.doesNotMatch(plain, /question-versions|Versions \(/);
});

test("W100: an option with a planned task says on one grey line what it launches, and its azima", () => {
  const azima = {
    id: "a58",
    wishId: "w1",
    code: "T58",
    title: "Lighting",
  };
  const question = {
    id: "q1",
    code: "Q01",
    wishId: "w1",
    text: "Which oil?",
    options: ["Olive", "Paraffin", "Neither"],
    recommendation: "",
    context: "",
    plans: [
      {
        option: s.Choice.A,
        title: "Fill the lamp with olive oil",
        partOf: "a58",
      },
      {
        option: s.Choice.B,
        title: "",
        prompt: "Buy paraffin\nThen fill the lamp.",
      },
    ],
  };
  // The wish's tasks read: the azima by its code and title.
  const djinn = {
    store: {
      getState: () => ({ details: { w1: { tasks: [azima] } } }),
      subscribe: () => () => {},
    },
  };
  const render = (props) =>
    s.renderToStaticMarkup(
      h(
        s.DjinnProvider,
        { djinn },
        h(s.WishQuestion, { question, onAnswer: async () => {}, ...props }),
      ),
    );
  for (const html of [render({}), render({ current: true, onSelect() {} })]) {
    const options = html.split('<strong class="option-letter">').slice(1);
    assert.equal(options.length, 3);
    assert.match(
      options[0],
      /<p>Olive<\/p><span class="option-plan" title="Launches: Fill the lamp with olive oil · T58 Lighting"><svg[^>]*lucide-corner-down-right[^]*?<\/svg><span class="option-plan-text">Launches: Fill the lamp with olive oil<\/span><span class="option-plan-azima">· T58 Lighting<\/span><\/span><\/div>/,
    );
    // Untitled, its prompt's first line; no azima, nothing after it.
    assert.match(
      options[1],
      /<span class="option-plan-text">Launches: Buy paraffin<\/span><\/span><\/div>/,
    );
    // An option without a plan says nothing.
    assert.doesNotMatch(options[2], /option-plan/);
  }
  // The wish's tasks not read yet: the line, without its azima, on the same single line.
  const alone = s.renderToStaticMarkup(
    h(s.WishQuestion, { question, onAnswer: async () => {} }),
  );
  assert.match(
    alone,
    /<span class="option-plan" title="Launches: Fill the lamp with olive oil">.*?<span class="option-plan-text">Launches: Fill the lamp with olive oil<\/span>/,
  );
  assert.doesNotMatch(alone, /T58/);
  // A question without options: the plan of its yes, above the note.
  const yes = s.renderToStaticMarkup(
    h(s.WishQuestion, {
      question: {
        ...question,
        options: [],
        plans: [{ option: s.Choice.YES, title: "Light the lamp" }],
      },
      onAnswer: async () => {},
    }),
  );
  assert.match(yes, /Launches: Light the lamp<\/span><\/span><textarea/);
});

test("W100: an answer that launched its option's plan links the launched task on its own line in the decision log", () => {
  const question = {
    id: "q1",
    code: "Q01",
    wishId: "w1",
    text: "Which oil?",
    options: ["Olive", "Paraffin"],
    answer: { choice: s.Choice.A, note: "" },
    plans: [
      {
        option: s.Choice.A,
        title: "Fill the lamp with olive oil",
        taskId: "t40",
      },
      { option: s.Choice.B, title: "Buy paraffin" },
    ],
  };
  const item = (q, tasks) => ({
    item: {
      id: q.id,
      ref: q.code,
      question: q,
      human: true,
      approved: false,
      by: "",
      icon: "*",
      tasks,
    },
  });
  const launched = { id: "t40", code: "W40", title: "Fill the lamp" };
  const other = { id: "t41", code: "W41", title: "Polish the lamp" };
  const opened = [];
  const html = s.renderToStaticMarkup(
    h(s.DecisionLog, {
      items: [item(question, [launched, other])],
      onTask: (id) => opened.push(id),
    }),
  );
  assert.match(
    html,
    /<p class="decision-tasks decision-launched"><svg[^]*?<\/svg>Launched as planned<a class="agent-code" href="#task-t40" title="Open W40 in the Tasks tab">W40<\/a><span class="decision-launched-title">Fill the lamp<\/span><\/p><p class="decision-tasks"><svg[^]*?<\/svg>Led to<a class="agent-code" href="#task-t41"[^>]*>W41<\/a><\/p>/,
  );
  // Not launched yet (a note left it to the lead): nothing launched, the plan says nothing in the log.
  const pending = s.renderToStaticMarkup(
    h(s.DecisionLog, {
      items: [
        item(
          {
            ...question,
            plans: [
              { option: s.Choice.A, title: "Fill the lamp with olive oil" },
            ],
          },
          [],
        ),
      ],
      onTask() {},
    }),
  );
  assert.doesNotMatch(pending, /decision-launched|Launched|Led to/);
  // Another option's plan launched nothing for this answer.
  const otherOption = s.renderToStaticMarkup(
    h(s.DecisionLog, {
      items: [
        item({ ...question, answer: { choice: s.Choice.B, note: "" } }, [
          other,
        ]),
      ],
      onTask() {},
    }),
  );
  assert.doesNotMatch(otherOption, /decision-launched/);
  assert.match(otherOption, /Led to<a class="agent-code" href="#task-t41"/);
});

test("a question shows its options by letter and its recommendation; answered, what was chosen", () => {
  const open = s.renderToStaticMarkup(
    h(s.WishQuestion, {
      question: {
        id: "q1",
        code: "Q01",
        text: "Which oil?",
        options: ["Olive", "Paraffin"],
        recommendation: "**A**, for the smell.",
        context: "",
      },
      expanded: true,
      onAnswer: async () => {},
    }),
  );
  assert.match(open, /Which oil\?/);
  assert.match(open, /<strong class="option-letter">A<\/strong><p>Olive<\/p>/);
  assert.match(
    open,
    /<strong class="option-letter">B<\/strong><p>Paraffin<\/p>/,
  );
  assert.match(open, /<strong>A<\/strong>, for the smell\./);
  // Two gestures, no third: the lamp is rubbed on the recommended option, preselected.
  assert.doesNotMatch(open, /Confirm/);
  assert.match(
    open,
    /<div class="option selected" role="button" tabindex="0" aria-pressed="true"><strong class="option-letter">A</,
  );
  assert.match(
    open,
    /title="Answer with the selected option and your note \(↵\)"><svg[^]*?Rub the lamp<kbd class="key-hint" aria-hidden="true">↵<\/kbd><\/button>/,
  );
  // The recommendation is boxed first, its option marked.
  assert.ok(open.indexOf("Recommendation · A") < open.indexOf("Olive"));
  assert.match(open, /Olive<\/p><span class="option-recommended">Recommended/);
  // Nothing waits for it, nothing said before what: it can wait, grey.
  assert.match(open, /question-card open can-wait/);
  assert.match(open, /status-badge tone-later[^>]*>.*<span>Can wait<\/span>/);
  // Needed before the merge: orange, under those words.
  const before = s.renderToStaticMarkup(
    h(s.WishQuestion, {
      question: {
        id: "q1",
        code: "Q01",
        text: "Which oil?",
        options: ["Olive", "Paraffin"],
        recommendation: "",
        context: "",
        before: "before the merge",
      },
      onAnswer: async () => {},
    }),
  );
  assert.match(
    before,
    /status-badge tone-waiting[^>]*>.*<span>before the merge<\/span>/,
  );
  assert.doesNotMatch(before, /can-wait|is-blocking/);

  // With the lamp's writes: Move at the top, then Withdraw on the left, Enlighten in the middle, and the lamp on the right.
  const lamp = s.renderToStaticMarkup(
    h(s.WishQuestion, {
      question: {
        id: "q1",
        code: "Q01",
        text: "Which oil?",
        options: ["Olive", "Paraffin"],
        recommendation: "B: brighter.",
        context: "## Cost\n\nOlive is dearer.",
        rounds: [],
        revision: 0,
      },
      blocking: ["W2"],
      onAnswer: async () => {},
      onEnlighten: async () => {},
      onMove: () => {},
      onWithdraw: () => {},
    }),
  );
  // Move, an icon named by its label, beside the heading; Withdraw on the left, Enlighten in the middle, then the
  // lamp; B, recommended, is the one rubbed.
  assert.match(
    lamp,
    /<\/div><div class="question-corner"><button class="question-corner-button question-move" type="button" aria-label="Move to another wish"><svg[^>]*>(?:(?!<\/svg>).)*<\/svg><\/button><\/div><\/div>/,
  );
  assert.doesNotMatch(lamp, /question-move"[^>]*title=/);
  assert.ok(
    lamp.indexOf("Withdraw question…") < lamp.indexOf(">Enlighten me<kbd"),
  );
  assert.ok(lamp.indexOf(">Enlighten me<kbd") < lamp.indexOf("Rub the lamp"));
  assert.match(
    lamp,
    /<div class="option selected" role="button" tabindex="0" aria-pressed="true"><strong class="option-letter">B</,
  );
  assert.doesNotMatch(
    lamp,
    /mark-toggle|question-menu|Your answer goes to the Djinn/,
  );
  assert.match(lamp, /Blocks W2/);
  assert.match(lamp, /What is at stake<\/h4>.*<h2>Cost<\/h2>/);
  assert.match(lamp, /question-card open is-blocking/);
  // No option named: nothing selected, the lamp waits for your pick.
  const vague = s.renderToStaticMarkup(
    h(s.WishQuestion, {
      question: {
        id: "q1",
        code: "Q01",
        text: "Which oil?",
        options: ["Olive", "Paraffin"],
        recommendation: "Both burn.",
        rounds: [],
      },
      onAnswer: async () => {},
    }),
  );
  assert.doesNotMatch(vague, /option selected/);
  assert.match(
    vague,
    /title="Pick an option first" disabled=""><svg[^]*?Rub the lamp<kbd class="key-hint" aria-hidden="true">↵<\/kbd><\/button>/,
  );

  // Being investigated, then revised: one line out of the way, which opens on its state, the note, the badge, the
  // history folded.
  const card = live(s.WishQuestion, {
    question: {
      id: "q1",
      code: "Q01",
      text: "Which oil?",
      options: ["Olive", "Paraffin"],
      recommendation: "A: it smells good.",
      revision: 1,
      rounds: [
        { kind: s.RoundKind.ENLIGHTEN, note: "Burn time?" },
        { kind: s.RoundKind.REVISE, recommendation: "B: brighter." },
        { kind: s.RoundKind.ENLIGHTEN, note: "And the price?" },
      ],
    },
    onAnswer: async () => {},
    onEnlighten: async () => {},
  });
  const folded = card.html();
  assert.match(folded, /question-card investigating {2}folded/);
  assert.match(
    folded,
    /<button class="question-heading question-fold" aria-expanded="false"><svg[^>]*lucide-lightbulb[^]*?<span class="question-id">Q01<\/span><span class="question-fold-text">Which oil\?<\/span>.*Being investigated.*<span class="question-fold-note">You asked: And the price\?<\/span>/,
  );
  assert.doesNotMatch(folded, /question-inner|Olive|<textarea/);
  // A request no one took on shows its age, to the minute (W557).
  const old = live(s.WishQuestion, {
    question: {
      id: "q2",
      code: "Q02",
      text: "Which wick?",
      options: ["Cotton", "Hemp"],
      recommendation: "A: it burns even.",
      rounds: [
        {
          kind: s.RoundKind.ENLIGHTEN,
          note: "Burn time?",
          createTime: {
            seconds: BigInt(
              Math.floor(Date.now() / 1000) - (4 * 3600 + 12 * 60 + 20),
            ),
            nanos: 0,
          },
        },
      ],
    },
    onAnswer: async () => {},
    onEnlighten: async () => {},
  });
  assert.match(old.html(), /Being investigated for 4h12m/);
  card.one("question-fold").onClick();
  const digging = card.html();
  assert.doesNotMatch(digging, /folded/);
  assert.match(
    digging,
    /<button class="question-heading" aria-expanded="true">/,
  );
  assert.match(digging, /You asked to find out more: And the price\?/);
  assert.match(digging, /Revised/);
  // Its earlier version is behind Versions (2), closed.
  assert.match(digging, /question-versions"[^>]*>.*?Versions \(2\)<\/button>/);
  assert.doesNotMatch(digging, /B: brighter\./);
  // Already asked: no second request.
  assert.doesNotMatch(digging, /lamp-enlighten/);
  // A click on its heading folds it again.
  card.one("question-heading").onClick();
  assert.match(card.html(), /question-fold/);
  // The investigator revises it: it waits for an answer again, open by itself.
  card.update({
    question: {
      id: "q1",
      code: "Q01",
      text: "Which oil?",
      options: ["Olive", "Paraffin"],
      recommendation: "B: it lasts longer.",
      revision: 2,
      rounds: [
        { kind: s.RoundKind.ENLIGHTEN, note: "Burn time?" },
        { kind: s.RoundKind.REVISE, recommendation: "B: brighter." },
        { kind: s.RoundKind.ENLIGHTEN, note: "And the price?" },
        { kind: s.RoundKind.REVISE, recommendation: "A: it smells good." },
      ],
    },
  });
  const revised = card.html();
  assert.match(revised, /question-card open can-wait"/);
  assert.doesNotMatch(revised, /question-fold|Being investigated/);
  assert.match(revised, /<div class="question-heading">/);
  assert.match(revised, /Paraffin.*<textarea/);
  assert.match(revised, /lamp-enlighten/);

  const answered = s.renderToStaticMarkup(
    h(s.WishQuestion, {
      question: {
        id: "q1",
        code: "Q01",
        text: "Which oil?",
        options: ["Olive", "Paraffin"],
        answer: { choice: s.Choice.B, note: "" },
      },
      onAnswer: async () => {},
    }),
  );
  assert.match(answered, /B · Paraffin/);
  assert.match(answered, /Decision recorded/);
});

test("Enlighten me sends the answer's note in one click: no second field", async () => {
  const sent = [];
  const card = live(s.WishQuestion, {
    question: {
      id: "q1",
      code: "Q01",
      text: "Which oil?",
      options: ["Olive", "Paraffin"],
      recommendation: "A: it smells good.",
      rounds: [],
    },
    onAnswer: async () => assert.fail("Enlighten me answers nothing"),
    onEnlighten: async (note) => {
      sent.push(note);
    },
  });
  // One field, for both gestures; Enlighten me opens nothing.
  const open = card.html();
  assert.equal(open.match(/<textarea/g).length, 1);
  assert.match(
    open,
    /placeholder="A note with your choice, or what to look into for Enlighten me \(optional · ⇧↵ new line\)"/,
  );
  assert.match(
    open,
    /<button class="button secondary lamp-enlighten" title="Find out more before deciding: the Djinn looks into what your note asks, then revises the question \(⌃⇧↵\)">/,
  );
  assert.doesNotMatch(open, /aria-expanded|Ask the lead to investigate/);

  // Typed in the note, sent at the first click, as typed; the note is cleared.
  card.one("textarea").onChange({
    target: { value: "  How long does each burn?\nAnd the price?  " },
  });
  assert.match(card.html(), /<textarea[^>]*> {2}How long does each burn\?/);
  card.one("lamp-enlighten").onClick();
  await card.settle();
  assert.deepEqual(sent, ["How long does each burn?\nAnd the price?"]);
  assert.match(card.html(), /<textarea[^>]*><\/textarea>/);
  assert.equal(card.all("textarea").length, 1);

  // Nothing typed: it asks to investigate in general.
  card.one("lamp-enlighten").onClick();
  await card.settle();
  assert.deepEqual(sent.at(-1), "");

  // Refused: the note stays, to try again.
  const kept = live(s.WishQuestion, {
    question: { id: "q2", code: "Q02", text: "Which wick?", options: [] },
    onAnswer: async () => {},
    onEnlighten: async () => {
      throw new Error("refused");
    },
  });
  assert.match(
    kept.html(),
    /placeholder="Your answer, or what to look into for Enlighten me \(⇧↵ new line\)"/,
  );
  kept.one("textarea").onChange({ target: { value: "Cotton?" } });
  kept.one("lamp-enlighten").onClick();
  await kept.settle();
  assert.match(kept.html(), /<textarea[^>]*>Cotton\?<\/textarea>/);
});

// A gesture shows on its card, by key as by click: its button is down while the answer waits, then the answer leaves
// as it comes back up; the card keeps the gesture's colour, and a wisp rises from the chosen button only.
test("a gesture presses its button and the card answers back in its colour", async () => {
  const answered = [];
  const enlightened = [];
  const card = live(s.WishQuestion, {
    question: {
      id: "q1",
      code: "Q01",
      text: "Which oil?",
      options: ["Olive", "Paraffin"],
      recommendation: "A: it smells good.",
    },
    onAnswer: async (choice) => {
      answered.push(choice);
    },
    onEnlighten: async (note) => {
      enlightened.push(note);
    },
  });
  assert.doesNotMatch(card.html(), /gesture-|is-pressed|question-wisp/);

  // Ctrl+Enter on the card: the lamp is down, the card plays the rub, nothing has left yet.
  card.one("question-card").onKeyDown({
    key: "Enter",
    ctrlKey: true,
    preventDefault() {},
  });
  let html = card.html();
  assert.match(html, /class="question-card open can-wait gesture-rub"/);
  assert.match(html, /class="button accent lamp-rub is-pressed"/);
  assert.match(
    html,
    /<span class="question-wisp" aria-hidden="true"><\/span><\/button>/,
  );
  assert.equal(html.match(/question-wisp/g).length, 1);
  assert.match(html, /class="button secondary lamp-enlighten"/);
  assert.equal(answered.length, 0);

  // Back up: the answer left, the card still glows in the lamp's colour.
  await card.settle();
  assert.deepEqual(answered, [s.Choice.A]);
  html = card.html();
  assert.match(html, /class="button accent lamp-rub"/);
  assert.match(html, /gesture-rub/);

  // A click plays the same; Enlighten me plays its own, the wisp on its button.
  const other = live(s.WishQuestion, {
    question: { id: "q2", code: "Q02", text: "Which wick?", options: [] },
    onAnswer: async () => {},
    onEnlighten: async (note) => {
      enlightened.push(note);
    },
  });
  other.one("lamp-enlighten").onClick();
  html = other.html();
  assert.match(html, /class="question-card open can-wait gesture-enlighten"/);
  assert.match(html, /class="button secondary lamp-enlighten is-pressed"/);
  assert.match(
    html,
    /⇧↵<\/kbd><span class="question-wisp" aria-hidden="true">/,
  );
  await other.settle();
  assert.deepEqual(enlightened, [""]);

  // Refused: the card stops playing, as it was.
  const refused = live(s.WishQuestion, {
    question: { id: "q3", code: "Q03", text: "Which lamp?", options: [] },
    onAnswer: async () => {
      throw new Error("refused");
    },
  });
  refused.one("lamp-rub").onClick();
  assert.match(refused.html(), /gesture-rub/);
  await refused.settle();
  assert.doesNotMatch(refused.html(), /gesture-|is-pressed|question-wisp/);
});

test("Later sits before Move in a question's top right corner, pressed while the question is put off", () => {
  const question = {
    id: "q1",
    code: "Q01",
    text: "Which oil?",
    options: ["Olive", "Paraffin"],
    recommendation: "",
    rounds: [],
    revision: 0,
    createTime: { seconds: 1_760_000_000n, nanos: 0 },
  };
  const later = [];
  const card = live(s.WishQuestion, {
    question,
    onAnswer: async () => {},
    onMove: () => {},
    onLater: (q) => later.push(q.code),
  });
  // Two icons named by their labels, Later first; not pressed, its icon says it goes down.
  assert.match(
    card.html(),
    /<div class="question-corner"><button class="question-corner-button question-later" aria-pressed="false" type="button" aria-label="Later"><svg[^>]*lucide-clock-arrow-down[^>]*>(?:(?!<\/svg>).)*<\/svg><\/button><button class="question-corner-button question-move" type="button" aria-label="Move to another wish">/,
  );
  assert.doesNotMatch(card.html(), /put off/);
  card.one("question-later").onClick();
  assert.deepEqual(later, ["Q01"]);

  // Put off: pressed, its icon says it comes back up, and the grey line says when.
  card.update({
    question: {
      ...question,
      laterTime: { seconds: 1_760_000_600n, nanos: 0 },
    },
  });
  assert.match(
    card.html(),
    /<button class="question-corner-button question-later is-pressed" aria-pressed="true" type="button" aria-label="Later"><svg[^>]*lucide-clock-arrow-up/,
  );
  assert.match(card.html(), /asked [^<]* · put off (?:at|[^<]* at) /);
  card.one("question-later").onClick();
  assert.deepEqual(later, ["Q01", "Q01"]);

  // Without the write, no Later: Move alone in the corner.
  const alone = s.renderToStaticMarkup(
    h(s.WishQuestion, { question, onAnswer: async () => {}, onMove: () => {} }),
  );
  assert.doesNotMatch(alone, /question-later/);
  assert.match(
    alone,
    /<div class="question-corner"><button class="question-corner-button question-move"/,
  );
});

test("keyboard shortcuts on a question card: Enter, Shift+Enter, Ctrl+Enter, Enlighten, Withdraw, Move, composing, arrows, 1-4, and macOS hints", async () => {
  const answered = [];
  const enlightened = [];
  const moved = [];
  const withdrawn = [];
  const card = live(s.WishQuestion, {
    question: {
      id: "q1",
      code: "Q01",
      text: "Which oil?",
      options: ["Olive", "Paraffin", "Canola", "Sesame"],
      recommendation: "A: it smells good.",
    },
    onAnswer: async (choice, note) => {
      answered.push({ choice, note });
    },
    onEnlighten: async (note) => {
      enlightened.push(note);
    },
    onMove: (q) => {
      moved.push(q.code);
    },
    onWithdraw: (q) => {
      withdrawn.push(q.code);
    },
  });

  // 1. Initial state: option A is recommended and preselected.
  assert.match(
    card.html(),
    /<div class="option selected" role="button" tabindex="0" aria-pressed="true"><strong class="option-letter">A</,
  );
  assert.doesNotMatch(card.html(), /question-hint unpicked/);

  // Arrow navigation: Down/Right moves next (wraps), Up/Left moves previous.
  let prevented = false;
  card.one("question-card").onKeyDown({
    key: "ArrowDown",
    preventDefault: () => {
      prevented = true;
    },
  });
  assert.ok(prevented);
  assert.match(
    card.html(),
    /<div class="option selected" role="button" tabindex="0" aria-pressed="true"><strong class="option-letter">B</,
  );

  card.one("question-card").onKeyDown({
    key: "ArrowRight",
    preventDefault: () => {},
  });
  assert.match(
    card.html(),
    /<div class="option selected" role="button" tabindex="0" aria-pressed="true"><strong class="option-letter">C</,
  );

  card.one("question-card").onKeyDown({
    key: "ArrowLeft",
    preventDefault: () => {},
  });
  assert.match(
    card.html(),
    /<div class="option selected" role="button" tabindex="0" aria-pressed="true"><strong class="option-letter">B</,
  );

  card.one("question-card").onKeyDown({
    key: "ArrowUp",
    preventDefault: () => {},
  });
  assert.match(
    card.html(),
    /<div class="option selected" role="button" tabindex="0" aria-pressed="true"><strong class="option-letter">A</,
  );

  // Number keys 1-4 pick options
  card.one("question-card").onKeyDown({
    key: "4",
    preventDefault: () => {},
  });
  assert.match(
    card.html(),
    /<div class="option selected" role="button" tabindex="0" aria-pressed="true"><strong class="option-letter">D</,
  );

  card.one("question-card").onKeyDown({
    key: "2",
    preventDefault: () => {},
  });
  assert.match(
    card.html(),
    /<div class="option selected" role="button" tabindex="0" aria-pressed="true"><strong class="option-letter">B</,
  );

  // Typing in the textarea
  card.one("textarea").onChange({ target: { value: "Pure and filtered" } });

  // IME composition: isComposing ignores Enter
  card.one("textarea").onKeyDown({
    key: "Enter",
    isComposing: true,
    preventDefault: () => {},
  });
  await card.settle();
  assert.equal(answered.length, 0);

  // Shift+Enter in textarea does not answer (allows native newline)
  let shiftPrevented = false;
  card.one("textarea").onKeyDown({
    key: "Enter",
    shiftKey: true,
    preventDefault: () => {
      shiftPrevented = true;
    },
  });
  await card.settle();
  assert.equal(shiftPrevented, false);
  assert.equal(answered.length, 0);

  // Ctrl+Shift+Enter in textarea triggers Enlighten me, sending note text and clearing note
  let enlightenPrevented = false;
  card.one("textarea").onKeyDown({
    key: "Enter",
    ctrlKey: true,
    shiftKey: true,
    preventDefault: () => {
      enlightenPrevented = true;
    },
  });
  await card.settle();
  assert.ok(enlightenPrevented);
  assert.deepEqual(enlightened, ["Pure and filtered"]);
  assert.match(card.html(), /<textarea[^>]*><\/textarea>/);

  // Type new note, then Enter in textarea rubs the lamp
  card.one("textarea").onChange({ target: { value: "Pressed today" } });
  let enterPrevented = false;
  card.one("textarea").onKeyDown({
    key: "Enter",
    preventDefault: () => {
      enterPrevented = true;
    },
  });
  await card.settle();
  assert.ok(enterPrevented);
  assert.deepEqual(answered, [{ choice: s.Choice.B, note: "Pressed today" }]);

  // Ctrl+Enter on card answers as well
  card.one("question-card").onKeyDown({
    key: "Enter",
    ctrlKey: true,
    preventDefault: () => {},
  });
  await card.settle();
  assert.equal(answered.length, 2);

  // Cmd+Shift+Enter on card triggers enlighten
  card.one("textarea").onChange({ target: { value: "From card" } });
  card.one("question-card").onKeyDown({
    key: "Enter",
    metaKey: true,
    shiftKey: true,
    preventDefault: () => {},
  });
  await card.settle();
  assert.deepEqual(enlightened, ["Pure and filtered", "From card"]);

  // Move button at the top, Withdraw button at the bottom, and Mod+Shift+Backspace on the card and in the textarea
  card.one("question-move").onClick();
  assert.deepEqual(moved, ["Q01"]);
  card.one("lamp-withdraw").onClick();
  assert.deepEqual(withdrawn, ["Q01"]);
  let withdrawPrevented = false;
  card.one("question-card").onKeyDown({
    key: "Backspace",
    ctrlKey: true,
    shiftKey: true,
    preventDefault: () => {
      withdrawPrevented = true;
    },
  });
  assert.ok(withdrawPrevented);
  assert.deepEqual(withdrawn, ["Q01", "Q01"]);
  let textareaWithdrawPrevented = false;
  card.one("textarea").onKeyDown({
    key: "Backspace",
    metaKey: true,
    shiftKey: true,
    preventDefault: () => {
      textareaWithdrawPrevented = true;
    },
  });
  assert.ok(textareaWithdrawPrevented);
  assert.deepEqual(withdrawn, ["Q01", "Q01", "Q01"]);
  // Plain Backspace or Ctrl+Alt+Backspace does not withdraw
  card.one("question-card").onKeyDown({
    key: "Backspace",
    preventDefault: () => {},
  });
  card.one("question-card").onKeyDown({
    key: "Backspace",
    ctrlKey: true,
    shiftKey: true,
    altKey: true,
    preventDefault: () => {},
  });
  assert.equal(withdrawn.length, 3);

  // Question with no recommendation: Enter without option selected does not answer and shows alert
  const unselectedAnswered = [];
  const cardNoRec = live(s.WishQuestion, {
    question: {
      id: "q2",
      code: "Q02",
      text: "Which wick?",
      options: ["Cotton", "Linen"],
      recommendation: "",
    },
    onAnswer: async (choice, note) => {
      unselectedAnswered.push({ choice, note });
    },
  });
  assert.doesNotMatch(cardNoRec.html(), /option selected/);
  assert.doesNotMatch(cardNoRec.html(), /question-hint unpicked/);

  // Ctrl+Enter on card warns
  cardNoRec.one("question-card").onKeyDown({
    key: "Enter",
    ctrlKey: true,
    preventDefault: () => {},
  });
  await cardNoRec.settle();
  assert.equal(unselectedAnswered.length, 0);
  assert.match(
    cardNoRec.html(),
    /<p class="question-hint unpicked" role="alert">.*Pick an option first<\/p>/,
  );

  // Pressing 1 picks first option and clears unpicked warning
  cardNoRec.one("question-card").onKeyDown({
    key: "1",
    preventDefault: () => {},
  });
  assert.match(
    cardNoRec.html(),
    /<div class="option selected" role="button" tabindex="0" aria-pressed="true"><strong class="option-letter">A</,
  );
  assert.doesNotMatch(cardNoRec.html(), /question-hint unpicked/);

  // Enter alone on the card answers nothing (W574): a click on its text leaves the focus there.
  cardNoRec.one("question-card").onKeyDown({
    key: "Enter",
    preventDefault: () => {},
  });
  await cardNoRec.settle();
  assert.equal(unselectedAnswered.length, 0);
  assert.doesNotMatch(cardNoRec.html(), /gesture-/);

  // Now Ctrl+Enter answers
  cardNoRec.one("question-card").onKeyDown({
    key: "Enter",
    ctrlKey: true,
    preventDefault: () => {},
  });
  await cardNoRec.settle();
  assert.deepEqual(unselectedAnswered, [{ choice: s.Choice.A, note: "" }]);

  // macOS symbols on macOS
  globalThis.__DJINN_MAC__ = true;
  try {
    const macMarkup = s.renderToStaticMarkup(
      h(s.WishQuestion, {
        question: {
          id: "q3",
          code: "Q03",
          text: "Mac test?",
          options: ["Yes", "No"],
          recommendation: "",
        },
        onAnswer: async () => {},
        onEnlighten: async () => {},
        onWithdraw: () => {},
      }),
    );
    assert.match(
      macMarkup,
      /<kbd class="key-hint" aria-hidden="true">⌘⇧⌫<\/kbd>/,
    );
    assert.match(
      macMarkup,
      /title="Close this question without an answer \(⌘⇧⌫\)"/,
    );
    assert.match(
      macMarkup,
      /<kbd class="key-hint" aria-hidden="true">⌘⇧↵<\/kbd>/,
    );
    assert.match(
      macMarkup,
      /title="Find out more before deciding: the Djinn looks into what your note asks, then revises the question \(⌘⇧↵\)"/,
    );
  } finally {
    delete globalThis.__DJINN_MAC__;
  }
});

test("a question reads top-down: its title, what is at stake folded, its report, the recommendation, the options, the note, then the gestures", () => {
  const wishId = "01a130b1-398a-7072-be60-ce928cb7241e";
  const report = {
    id: "01a130d9-0000-7000-8000-000000000001",
    wishId,
    kind: "report",
    title: s.dew.REPORT_TITLE,
    content: s.dew.REPORT,
  };
  const html = s.renderToStaticMarkup(
    h(s.WishQuestion, {
      question: {
        id: "q4",
        code: "Q04",
        wishId,
        text: s.dew.QUESTION,
        options: s.dew.options(wishId),
        context: s.dew.CONTEXT,
        recommendation: s.dew.RECOMMENDATION,
        before: "before the implementation starts",
        move: true,
        rounds: [],
      },
      report,
      onAnswer: async () => {},
      onEnlighten: async () => {},
      onMove: () => {},
      onWithdraw: () => {},
    }),
  );
  // The explanation comes before the call to act, the note above the gestures at the bottom.
  const at = (text) => {
    const i = html.indexOf(text);
    assert.ok(i >= 0, `the card shows ${text}`);
    return i;
  };
  const order = [
    "Will you run the command?",
    'aria-label="Move to another wish"',
    "What is at stake</h4>",
    s.dew.CONTEXT_FIRST,
    "Read more",
    `Report: ${s.dew.REPORT_TITLE}`,
    "Recommendation · A",
    '<strong class="option-letter">A</strong>',
    '<strong class="option-letter">B</strong>',
    "<textarea",
    "Withdraw question…",
    ">Enlighten me<kbd",
    "Rub the lamp",
  ].map(at);
  assert.deepEqual(
    order,
    [...order].sort((a, b) => a - b),
  );
  assert.doesNotMatch(html, /Your answer goes to the Djinn/);
  // Its before words stay on its badge.
  assert.match(
    html,
    /status-badge tone-waiting[^>]*>.*before the implementation starts/,
  );
  // The long context shows its first section; Read more unfolds the rest in place.
  assert.match(
    html,
    /<button type="button" class="read-more" aria-expanded="false"><svg[^>]*>.*?<\/svg>Read more<\/button>/,
  );
  assert.doesNotMatch(html, new RegExp(s.dew.CONTEXT_LATER));
  // The report it names is one click away, folded.
  assert.match(
    html,
    /<button type="button" class="question-report-link" aria-expanded="false">.*Report: Payroll for acme: feasibility/,
  );
  assert.doesNotMatch(html, /What the fix costs/);
  // The option's command is selectable text with its copy button, inside an option that is no button.
  assert.match(
    html,
    new RegExp(
      `<div class="option selected" role="button" tabindex="0" aria-pressed="true"><strong class="option-letter">A</strong><p>Done: I ran <span class="command"><code>djinn wish allow ${wishId} --mode auto</code><button type="button" class="copy-command" title="Copy the command" aria-label="Copy the command">`,
    ),
  );
  assert.doesNotMatch(html, /<button class="option/);
});

test("a move's links are clickable in its title, context, recommendation and options; its main link is a button", () => {
  const mr = "https://gitlab.com/empowill/app/-/merge_requests/123";
  const ci = "https://gitlab.com/empowill/app/-/pipelines/9";
  const tilasm = "djinn://tilasm/01a130b1-398a-7072-be60-ce928cb7241e";
  const question = {
    id: "q7",
    code: "Q07",
    text: `Review MR !123 and take it out of draft: ${mr}`,
    context: `W12 opened it as a draft; its CI: ${ci}. The diagram: ${tilasm}\n\n\`\`\`sh\nglab mr update 123 --ready\n\`\`\``,
    recommendation: "A: read the diff, then run `glab mr update 123 --ready`",
    options: [`Done: it is ready, ${mr}`, "Not yet"],
    move: true,
    rounds: [],
  };
  const html = s.renderToStaticMarkup(
    h(s.WishQuestion, { question, onAnswer: async () => {} }),
  );
  const a = (href, cls) =>
    cls === "djinn-link"
      ? `<a href="${href}" class="djinn-link">`
      : `<a href="${href}"${cls ? ` class="${cls}"` : ""} target="_blank" rel="noopener noreferrer">`;
  // The title: its link, in the text.
  assert.match(
    html,
    new RegExp(
      `<h3>Review MR !123 and take it out of draft: ${a(mr, "text-link")}${mr}</a></h3>`.replace(
        /[?]/g,
        "\\?",
      ),
    ),
  );
  // The move's main link, the first of the card, a button before what is at stake.
  assert.ok(
    html.includes(
      `<p class="question-move-link">${a(mr, "button secondary small move-link")}<svg`,
    ),
  );
  assert.match(html, /<span>Open MR !123<\/span><\/a><\/p>/);
  assert.ok(
    html.indexOf("question-move-link") < html.indexOf("question-context"),
  );
  // The context: a web address and a bare djinn:// link are links; its command block has a copy button.
  assert.ok(html.includes(`${a(ci)}${ci}</a>`));
  assert.ok(html.includes(`${a(tilasm, "djinn-link")}${tilasm}</a>`));
  assert.match(
    html,
    /<button type="button" aria-label="Copy the code" class="ac-copy">Copy<\/button><\/div><pre><code class="language-sh">glab mr update 123 --ready\n<\/code><\/pre>/,
  );
  // The recommendation's command is copyable.
  assert.match(
    html,
    /Recommendation · A<\/span><div class="ac-markdown"><p>A: read the diff, then run <span class="command"><code>glab mr update 123 --ready<\/code><button type="button" class="copy-command"/,
  );
  // An option's link.
  assert.ok(
    html.includes(`<p>Done: it is ready, ${a(mr, "text-link")}${mr}</a></p>`),
  );

  // A question that is no move has no button; an answered move neither, and its title, in the heading's button, is
  // text.
  const plain = s.renderToStaticMarkup(
    h(s.WishQuestion, {
      question: { ...question, move: false },
      onAnswer: async () => {},
    }),
  );
  assert.doesNotMatch(plain, /move-link/);
  const answered = s.renderToStaticMarkup(
    h(s.WishQuestion, {
      question: {
        ...question,
        answer: { choice: s.Choice.A, note: "" },
      },
      expanded: true,
      onAnswer: async () => {},
    }),
  );
  assert.doesNotMatch(answered, /move-link/);
  assert.ok(
    answered.includes(
      `<h3>Review MR !123 and take it out of draft: ${mr}</h3>`,
    ),
  );
});

test("a link's click opens it and stops there: the option around it is not picked", () => {
  for (const href of [
    "https://gitlab.com/empowill/app/-/merge_requests/123",
    "djinn://tilasm/01a130b1-398a-7072-be60-ce928cb7241e",
  ]) {
    const link = live(s.Link, { href, className: "text-link", children: href });
    let stopped = 0;
    link.one("a").onClick({
      stopPropagation: () => stopped++,
      preventDefault: () => {},
    });
    assert.equal(stopped, 1, href);
  }
  // A link of another scheme is text.
  assert.equal(
    s.renderToStaticMarkup(h(s.Link, { href: "file:///etc/passwd" }, "x")),
    "<span>x</span>",
  );
});

test("a copy button copies the exact command and says so, and never answers the question around it", async () => {
  const copied = [];
  const clipboard = Object.getOwnPropertyDescriptor(navigator, "clipboard");
  Object.defineProperty(navigator, "clipboard", {
    configurable: true,
    value: { writeText: async (text) => copied.push(text) },
  });
  try {
    const command =
      "djinn wish allow 01a130b1-398a-7072-be60-ce928cb7241e --mode auto";
    const button = live(s.CopyButton, { text: command });
    let stopped = 0;
    button.one("copy-command").onClick({
      stopPropagation: () => stopped++,
      preventDefault: () => {},
    });
    await button.settle();
    assert.deepEqual(copied, [command]);
    // Its click stops there: the option around it is not picked.
    assert.equal(stopped, 1);
    assert.match(
      button.html(),
      /class="copy-command copied" title="Copied" aria-label="Copied">.*<span class="copy-feedback" role="status">Copied<\/span>/,
    );
  } finally {
    if (clipboard) Object.defineProperty(navigator, "clipboard", clipboard);
    else delete navigator.clipboard;
  }

  // Enter on a copy button presses it: the card does not rub the lamp.
  const answered = [];
  const card = live(s.WishQuestion, {
    question: {
      id: "q4",
      code: "Q04",
      text: s.dew.QUESTION,
      options: s.dew.options("w"),
      recommendation: s.dew.RECOMMENDATION,
    },
    onAnswer: async (choice) => {
      answered.push(choice);
    },
  });
  const onCopy = {
    tagName: "BUTTON",
    closest: (selector) => (selector.startsWith("button") ? {} : null),
  };
  let prevented = false;
  card.one("question-card").onKeyDown({
    key: "Enter",
    target: onCopy,
    preventDefault: () => {
      prevented = true;
    },
  });
  await card.settle();
  assert.deepEqual(answered, []);
  assert.equal(prevented, false);
  // Selecting the words of an option picks nothing; a plain click picks it, and answers nothing either.
  const options = () => card.all("option");
  globalThis.getSelection = () => ({ toString: () => "djinn wish allow" });
  try {
    options()[1].props.onClick();
  } finally {
    delete globalThis.getSelection;
  }
  assert.equal(options()[1].props["aria-pressed"], false);
  options()[1].props.onClick();
  assert.equal(options()[1].props["aria-pressed"], true);
  // Space on a focused option picks it, as on a button.
  const option0 = options()[0].props;
  option0.onKeyDown({
    key: " ",
    target: "self",
    currentTarget: "self",
    preventDefault: () => {},
  });
  assert.equal(options()[0].props["aria-pressed"], true);
  await card.settle();
  assert.deepEqual(answered, []);
  // Enter on a focused option picks it too, and answers nothing (W574).
  options()[1].props.onKeyDown({
    key: "Enter",
    target: "self",
    currentTarget: "self",
    preventDefault: () => {},
  });
  assert.equal(options()[1].props["aria-pressed"], true);
  // Ctrl+Enter elsewhere on the card rubs the lamp on that pick, and only now answers.
  const article = { tagName: "ARTICLE", closest: () => null };
  card.one("question-card").onKeyDown({
    key: "Enter",
    ctrlKey: true,
    target: article,
    preventDefault: () => {},
  });
  await card.settle();
  assert.deepEqual(answered, [s.Choice.B]);
});

test("a question of a wish without a lead session says no lead is told: open, then answered", () => {
  const props = (noLead, extra) =>
    s.renderToStaticMarkup(
      h(s.WishQuestion, {
        question: {
          id: "q1",
          code: "Q01",
          text: "Which oil?",
          options: ["Olive", "Paraffin"],
          recommendation: "A: it smells good.",
          ...extra,
        },
        expanded: true,
        noLead,
        onAnswer: async () => {},
      }),
    );
  const told = props(false, {});
  assert.doesNotMatch(
    told,
    /goes to the Djinn|The Djinn is not there to tell|question-hint/,
  );
  const open = props(true, {});
  assert.match(
    open,
    /The Djinn is not there to tell: your answer goes to the workers and waits in the wish&#x27;s brief\./,
  );
  const answer = { answer: { choice: s.Choice.A, note: "" } };
  assert.doesNotMatch(props(false, answer), /The Djinn is not there to tell/);
  assert.match(
    props(true, answer),
    /<p class="no-lead">The Djinn is not there to tell: the answer waits in the wish&#x27;s brief\.<\/p>/,
  );
  const waitingLead = {
    answer: { choice: s.Choice.A, note: "" },
    waitingLead: true,
  };
  const waitingHtml = props(false, waitingLead);
  assert.match(
    waitingHtml,
    /<p class="no-lead">The Djinn is not running to receive your answer: it gets it once it starts\.<\/p>/,
  );
  assert.match(
    waitingHtml,
    /<span class="status-badge tone-waiting">.*Waiting for the Djinn<\/span>/,
  );
});

test("Sleep sits between Later and Move in a question's top right corner, and opens what it sleeps until", () => {
  const question = {
    id: "q1",
    code: "Q01",
    text: "Which oil?",
    options: ["Olive", "Paraffin"],
    recommendation: "",
    rounds: [],
    revision: 0,
    createTime: { seconds: 1_760_000_000n, nanos: 0 },
  };
  const slept = [];
  const card = live(s.WishQuestion, {
    question,
    onAnswer: async () => {},
    onMove: () => {},
    onLater: () => {},
    onSleep: (q) => slept.push(q.code),
  });
  assert.match(
    card.html(),
    /aria-label="Later">(?:(?!<\/button>).)*<\/button><button class="question-corner-button question-sleep" type="button" aria-label="Sleep"><svg[^>]*lucide-moon[^>]*>(?:(?!<\/svg>).)*<\/svg><\/button><button class="question-corner-button question-move"/,
  );
  card.one("question-sleep").onClick();
  assert.deepEqual(slept, ["Q01"]);
  // Woken, the grey line says when.
  card.update({
    question: {
      ...question,
      sleep: {
        conditions: [],
        createTime: { seconds: 1_760_000_100n, nanos: 0 },
        wakeTime: { seconds: 1_760_000_600n, nanos: 0 },
      },
    },
  });
  assert.match(card.html(), /asked [^<]* · woke (?:at|[^<]* at) /);
});

test("a question iterating on its mockup waits for its mockup task, not for you: folded as one being investigated (W268)", () => {
  const card = live(s.WishQuestion, {
    question: {
      id: "q1",
      code: "Q61",
      text: "Do the typed options suit you?",
      options: ["Iterate", "Build", "Other"],
      recommendation: "A: one more version.",
      rounds: [
        { kind: s.RoundKind.ITERATE, note: "Bigger icons", option: s.Choice.A },
      ],
    },
    onAnswer: async () => {},
    onEnlighten: async () => {},
  });
  const folded = card.html();
  assert.match(folded, /question-card investigating {2}folded/);
  assert.match(folded, /You asked: Bigger icons/);
  assert.doesNotMatch(folded, /<textarea/);
});
