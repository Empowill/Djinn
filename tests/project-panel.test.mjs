// A project's panel (src/project-panel.tsx): its sources, checks, push cadence, reference branch, watchers,
// configuration. Run with `go tool task test-ui`.
import test from "node:test";
import assert from "node:assert/strict";

import { projectSync, screens } from "./screens.mjs";

const { s, h, live } = await screens();

test("a project's panel lists the sources its skills declare: what each reads, how it stands, and Plug in or Unplug with what it does", async () => {
  const p1 = { id: "p1", name: "gong", git: true, directory: "/tmp/gong" };
  const sources = [
    {
      name: "gong/babysit-mr",
      skill: "babysit-mr",
      project: "gong",
      watch: "sh .agents/skills/babysit-mr/assigned.sh",
      every: "5m0s",
      summary:
        "The merge requests of this project assigned to you, read with glab.",
    },
    {
      name: "gong/mentions",
      skill: "mentions",
      project: "gong",
      watch: "mentions --new",
      every: "1h0m0s",
      plugged: true,
      lastRun: { endTime: { seconds: 1_760_000_000n, nanos: 0 }, items: 2 },
    },
    {
      name: "gong/noisy",
      skill: "noisy",
      project: "gong",
      error: "metadata.djinn.source.every: 10s is too often",
    },
    { name: "bell/other", skill: "other", project: "bell", every: "5m0s" },
  ];
  const render = async (list) => {
    const transport = s.createRouterTransport(({ service }) => {
      service(s.ProjectService, { show: () => ({ project: p1 }) });
      service(s.SkillService, { list: () => ({ skills: [] }) });
      service(s.InboxService, {
        list: () => ({ items: [] }),
        sources: () => ({ sources: list }),
      });
    });
    const djinn = s.createDjinn(transport, 10);
    await djinn.store.changed("", [s.Change.INBOX]);
    return s.renderToStaticMarkup(
      h(
        s.DjinnProvider,
        { djinn },
        h(s.ProjectPanel, { project: p1, onClose() {} }),
      ),
    );
  };
  const html = await render(sources);
  assert.match(
    html,
    /<h3>Inbox sources<span class="config-from">on this machine<\/span><\/h3>/,
  );
  const rows = html
    .split('<li class="inbox-source-row">')
    .slice(1)
    .map((row) => row.slice(0, row.indexOf("</li>")));
  // Only this project's sources.
  assert.equal(rows.length, 3);
  assert.match(rows[0], /<strong>babysit-mr<\/strong>/);
  assert.match(
    rows[0],
    /<p>The merge requests of this project assigned to you, read with glab\.<\/p>/,
  );
  assert.match(rows[0], /Unplugged: runs nothing/);
  assert.match(
    rows[0],
    /<code>sh .agents\/skills\/babysit-mr\/assigned.sh<\/code>/,
  );
  // No setting's button in the accent colour: none is recommended.
  assert.match(
    rows[0],
    /class="button small secondary"[^>]*>.*Plug in<\/button>/,
  );
  assert.match(
    rows[0],
    /<p>Runs this command every 5m on this machine, logged in by its own tool; each item it prints becomes a card in the inbox\.<\/p>/,
  );
  assert.match(rows[1], /Plugged in, every 1h · last run [^<]+: 2 items/);
  assert.match(rows[1], />Unplug<\/button>/);
  // A source that cannot run says why, and offers nothing to plug in.
  assert.match(rows[2], /10s is too often/);
  assert.doesNotMatch(rows[2], /<button/);

  assert.match(
    await render([]),
    /No skill of this project declares a source\./,
  );
});

test("the project view lists the setup and the checks, when each runs, and how each last ran", () => {
  const none = s.renderToStaticMarkup(
    h(s.ProjectChecks, { setup: "", checks: [], runs: [] }),
  );
  assert.match(
    none,
    /No check: Djinn integrates this project&#x27;s work only into a wish&#x27;s own branch, untested/,
  );
  const html = s.renderToStaticMarkup(
    h(s.ProjectChecks, {
      setup: "npm ci",
      checks: [
        {
          name: "lint",
          command: "go tool task lint",
          when: [s.CheckWhen.COMMIT, s.CheckWhen.PUSH],
        },
        {
          name: "test",
          command: "go tool task test",
          when: [s.CheckWhen.PUSH],
        },
      ],
      runs: [
        {
          name: "setup",
          setup: true,
          passed: true,
          sha: "0123456789abcdef",
          durationMs: 61_000n,
          reason: "",
          output: "",
          endTime: { seconds: 1791640800n, nanos: 0 },
        },
        {
          name: "LINT",
          setup: false,
          passed: false,
          sha: "fedcba9876543210",
          durationMs: 2_000n,
          reason: "go tool task lint exited 1:\nmain.go:1: unused",
          output: "main.go:1: unused",
          endTime: { seconds: 1791640800n, nanos: 0 },
        },
      ],
    }),
  );
  assert.ok(
    html.indexOf("npm ci") < html.indexOf("go tool task lint") &&
      html.indexOf("go tool task lint") < html.indexOf("go tool task test"),
  );
  assert.match(
    html,
    /<strong>setup<\/strong><p><code>npm ci<\/code><\/p><p class="muted-text">Passed on 01234567, [^<]+, in 1m1s\.<\/p><\/div><span><span class="badge muted">makes a worktree ready<\/span>/,
  );
  assert.match(
    html,
    /<p class="login-message" title="main.go:1: unused">Failed on fedcba98, [^<]+: go tool task lint exited 1<\/p><\/div><span><span class="badge muted">before a commit<\/span><span class="badge muted">before a push<\/span>/,
  );
  assert.match(
    html,
    /<strong>test<\/strong><p><code>go tool task test<\/code><\/p><p class="muted-text">Not run yet\.<\/p>/,
  );
});

test("the project panel displays the push cadence setting, out-of-sync status, and push button", () => {
  const p1 = {
    id: "p1",
    name: "app",
    git: true,
    directory: "/tmp/app",
    remote: "git@github.com:org/app.git",
    push: s.ProjectPush.STANDARD,
    syncs: [projectSync("feat/x", 3, 0), projectSync("feat/y", 0, 0)],
  };
  const transport = s.createRouterTransport(({ service }) => {
    service(s.ProjectService, {
      show: () => ({ project: p1 }),
      push: () => ({}),
      setPush: () => ({ project: p1 }),
    });
    service(s.SkillService, {
      list: () => ({ skills: [] }),
    });
  });
  const djinn = s.createDjinn(transport, 10);
  const html = s.renderToStaticMarkup(
    h(
      s.DjinnProvider,
      { djinn },
      h(s.ProjectPanel, { project: p1, onClose() {} }),
    ),
  );
  // The same short line as the side panel's: the branch, its counts, a small grey button.
  assert.match(
    html,
    /<div class="sync-line" title="Integration branch of the wish Lamp\n3 commits ahead of origin\/feat\/x"><span class="sync-branch">feat\/x<\/span><span class="sync-counts"><span class="sync-ahead">↑3<\/span><\/span><button type="button" class="icon-button sync-push" title="Push feat\/x"/,
  );
  assert.doesNotMatch(html, /feat\/y|button accent/);
  assert.match(
    html,
    /Pushes at an azima(?:'|&#x27;)s end or after 3 tasks and an hour\./,
  );
  assert.match(
    html,
    /<button type="button" class="active">Standard<\/button><button type="button" class="">On demand<\/button>/,
  );

  const p2 = {
    ...p1,
    push: s.ProjectPush.ON_DEMAND,
    syncs: [],
  };
  const htmlOnDemand = s.renderToStaticMarkup(
    h(
      s.DjinnProvider,
      { djinn },
      h(s.ProjectPanel, { project: p2, onClose() {} }),
    ),
  );
  assert.match(htmlOnDemand, /Pushes only when you ask\./);
  assert.doesNotMatch(htmlOnDemand, /sync-line/);
  assert.match(
    htmlOnDemand,
    /<button type="button" class="">Standard<\/button><button type="button" class="active">On demand<\/button>/,
  );
});

// The reference branch chosen in the panel (W149): Change reads the remote's branches, the current one first when this
// copy lacks it; the choice goes to your own file, and when it is yours it can be taken back. Never an accent button.
test("a project's reference branch is chosen from the remote's branches, and your choice taken back", async () => {
  const chosen = [];
  let refuse = false;
  const choice = live(s.BranchChoice, {
    reference: {
      branch: "main",
      ref: "origin/main",
      commit: "0458f74c",
      source: s.SettingSource.DEFAULT,
    },
    load: async () => ["dev", "main", "release"],
    onChoose: async (branch) => {
      chosen.push(branch);
      return !refuse;
    },
  });
  assert.equal(
    choice.html(),
    '<button type="button" class="button small secondary">Change</button>',
  );
  choice.one("button").onClick();
  assert.match(choice.html(), /Reading the remote(?:'|&#x27;)s branches…/);
  await choice.settle();
  let html = choice.html();
  assert.match(
    html,
    /<select aria-label="Reference branch"><option value="dev">dev<\/option><option value="main" selected="">main<\/option><option value="release">release<\/option><\/select>/,
  );
  assert.doesNotMatch(html, /Take your choice back|button accent/);
  assert.match(
    html,
    /<button type="submit" class="button small secondary" disabled="">Choose<\/button>/,
  );
  assert.match(html, /never in the repository/);
  choice.one("select").onChange({ target: { value: "dev" } });
  refuse = true;
  await choice.one("form").onSubmit({ preventDefault() {} });
  await choice.settle();
  assert.deepEqual(chosen, ["dev"]);
  assert.match(
    choice.html(),
    /<select/,
    "a refused choice keeps the list open",
  );
  refuse = false;
  await choice.one("form").onSubmit({ preventDefault() {} });
  await choice.settle();
  assert.deepEqual(chosen, ["dev", "dev"]);
  assert.match(
    choice.html(),
    /^<button type="button" class="button small secondary">Change<\/button>$/,
  );

  // Yours: the list offers to take it back, and keeps a branch this copy has not fetched.
  const yours = live(s.BranchChoice, {
    reference: {
      branch: "dev",
      ref: "origin/dev",
      commit: "",
      source: s.SettingSource.DEVELOPER,
    },
    load: async () => ["main"],
    onChoose: async (branch) => {
      chosen.push(branch);
      return true;
    },
  });
  yours.one("button").onClick();
  await yours.settle();
  html = yours.html();
  assert.match(
    html,
    /<option value="">Take your choice back: the team(?:'|&#x27;)s file, else Djinn(?:'|&#x27;)s default<\/option><option value="dev" selected="">dev<\/option><option value="main">main<\/option>/,
  );
  yours.one("select").onChange({ target: { value: "" } });
  await yours.one("form").onSubmit({ preventDefault() {} });
  assert.deepEqual(chosen, ["dev", "dev", ""]);

  // Branches it cannot read: the error, and Cancel to close the list.
  const unread = live(s.BranchChoice, {
    load: async () => {
      throw new Error("git for-each-ref: not a git repository");
    },
    onChoose: async () => true,
  });
  unread.one("button").onClick();
  await unread.settle();
  assert.match(unread.html(), /not a git repository/);
  assert.doesNotMatch(unread.html(), /<select/);
  unread.all("button").at(-1).props.onClick();
  assert.match(unread.html(), /^<button[^>]*>Change<\/button>$/);
});

// The watcher commands a developer allows in a project, in its panel: each with a button that takes it back, and a
// field that allows one more, kept in their own settings.
test("a project's panel lists the watcher commands you allow", () => {
  const html = s.renderToStaticMarkup(
    h(s.WatchCommands, {
      commands: ["go run ./.agents/skills/babysit-mr-empowill/mrwatch"],
      onAllow: async () => true,
      onRevoke() {},
    }),
  );
  assert.match(
    html,
    /<div class="setting-row"><div><code>go run \.\/\.agents\/skills\/babysit-mr-empowill\/mrwatch<\/code><\/div><button type="button" class="button secondary">Take back<\/button><\/div>/,
  );
  assert.match(html, /never in the repository/);
  assert.match(
    html,
    /<form class="folder-field"><input aria-label="Watcher command to allow"[^>]*\/><button type="submit" class="button secondary" disabled="">Allow<\/button><\/form>/,
  );
  const none = s.renderToStaticMarkup(
    h(s.WatchCommands, {
      commands: [],
      onAllow: async () => true,
      onRevoke() {},
    }),
  );
  assert.doesNotMatch(none, /Take back/);
});

// The project's configuration panel (W148): a thing to fix says what it is, the error as read, and what settles it,
// a button never alone and never in the accent colour; the reference branch says its ref, commit and where its name
// comes from; each skill says its role for Djinn; each setting where it comes from, those at the default folded.
test("a project's configuration says what to fix, its reference branch, each skill's role and where each setting comes from", () => {
  const render = (el) => s.renderToStaticMarkup(el);
  const unreadable = render(
    h(s.NoticeLine, {
      notice: {
        kind: s.ProjectNoticeKind.TO_FIX,
        topic: s.ProjectNoticeTopic.SKILL,
        subject: "notion-ticket",
        text: "Djinn cannot read the skill notion-ticket.",
        detail: "yaml: line 3: did not find expected key",
        action: s.ProjectNoticeAction.FIX_FILE,
        file: "/tmp/dolmen/.agents/skills/notion-ticket/SKILL.md",
        command: "",
      },
      busy: false,
      onAct() {},
    }),
  );
  assert.match(unreadable, /^<div class="notice to-fix">/);
  assert.match(
    unreadable,
    /<span>Djinn cannot read the skill notion-ticket\.<\/span><\/p><pre class="notice-detail">yaml: line 3: did not find expected key<\/pre>/,
  );
  assert.match(
    unreadable,
    /To fix in <span class="command"><code>\/tmp\/dolmen\/\.agents\/skills\/notion-ticket\/SKILL\.md<\/code>/,
  );
  assert.doesNotMatch(unreadable, /From a terminal/);

  const acted = [];
  const offered = render(
    h(s.NoticeLine, {
      notice: {
        kind: s.ProjectNoticeKind.TO_FIX,
        topic: s.ProjectNoticeTopic.SOURCE,
        subject: "djinn/babysit-pr",
        text: "The skill babysit-pr offers an inbox source this machine has not decided on.",
        detail: "",
        action: s.ProjectNoticeAction.PLUG_SOURCE,
        file: "",
        command: "djinn inbox plug djinn/babysit-pr",
      },
      busy: false,
      onAct: (plug) => acted.push(plug),
    }),
  );
  assert.doesNotMatch(offered, /notice-detail|button accent/);
  assert.match(
    offered,
    /<div class="notice-choice"><button type="button" class="button small secondary">.*Plug in<\/button><p>Its command runs on this machine; each item it prints becomes a card in the inbox\.<\/p><\/div><div class="notice-choice"><button type="button" class="button small secondary">.*Say no<\/button><p>It runs nothing on this machine, and the project stops asking\.<\/p><\/div>/,
  );
  assert.match(
    offered,
    /From a terminal: <span class="command"><code>djinn inbox plug djinn\/babysit-pr<\/code>/,
  );

  const shown = {
    repositoryFile: "/tmp/djinn/.agents/settings.txtpb",
    developerFile: "/home/you/.config/djinn/projects/p1/settings.txtpb",
  };
  const reference = render(
    h(s.ReferenceBranch, {
      reference: {
        branch: "main",
        ref: "origin/main",
        commit: "0458f74c0ffee",
        source: s.SettingSource.DEFAULT,
        checkedOut: "feature",
      },
      missing: false,
      notices: [
        {
          kind: s.ProjectNoticeKind.INFORMATION,
          topic: s.ProjectNoticeTopic.BRANCH,
          text: "Your copy has feature checked out, not the reference branch origin/main.",
        },
      ],
      shown,
    }),
  );
  assert.match(
    reference,
    /<strong>Reference branch<\/strong><p><code>main<\/code> origin\/main at 0458f74 <span class="config-from">Djinn(?:'|&#x27;)s default<\/span><\/p>/,
  );
  assert.match(reference, /pull requests aim at it/);
  assert.match(
    reference,
    /<p>Your copy has feature checked out, not the reference branch origin\/main\.<\/p>/,
  );
  const named = render(
    h(s.ReferenceBranch, {
      reference: {
        branch: "dev",
        ref: "origin/dev",
        commit: "",
        source: s.SettingSource.DEVELOPER,
        checkedOut: "dev",
      },
      missing: true,
      notices: [],
      shown,
    }),
  );
  assert.match(
    named,
    /<code>dev<\/code> origin\/dev, not in this copy yet <span class="config-from" title="\/home\/you\/\.config\/djinn\/projects\/p1\/settings\.txtpb">your file<\/span>/,
  );
  assert.match(
    render(h(s.ReferenceBranch, { missing: true, notices: [], shown })),
    /None found: see “To fix”\./,
  );
  assert.equal(
    render(h(s.ReferenceBranch, { missing: false, notices: [], shown })),
    "",
  );
  assert.doesNotMatch(reference, /Change/);
  assert.match(
    render(
      h(s.ReferenceBranch, {
        missing: true,
        notices: [],
        shown,
        load: async () => [],
        onChoose: async () => true,
      }),
    ),
    /None found: see “To fix”\.<\/p><p class="muted-text">.*<\/p><button type="button" class="button small secondary">Change<\/button>/,
  );

  const project = { id: "p1", name: "djinn", directory: "/tmp/djinn" };
  const skill = (extra) =>
    render(
      h(s.SkillLine, {
        project,
        toFix: false,
        skill: {
          project: "djinn",
          name: "babysit-pr",
          source: "",
          directory: "/tmp/djinn/.agents/skills/babysit-pr",
          description: "Babysits a pull request.",
          missing: "",
          template: "",
          inboxSource: "",
          babysit: "",
          ...extra,
        },
      }),
    );
  assert.match(
    skill({
      template: "Babysit PR #{number}",
      inboxSource: "sh inbox.sh",
      babysit: "sh watch.sh {number}",
    }),
    /<strong>babysit-pr<\/strong><span class="config-from">\.agents\/skills<\/span><p>Babysits a pull request\.<\/p><p class="skill-role">Wish template “Babysit PR #\{number\}” · Inbox source · Babysits the project(?:'|&#x27;)s pull requests<\/p>/,
  );
  assert.match(
    skill({}),
    /<p class="skill-role">Know-how for the workers: no wish template, inbox source nor babysit for Djinn\.<\/p>/,
  );
  assert.match(
    render(
      h(s.SkillLine, {
        project,
        toFix: true,
        skill: {
          name: "notion-ticket",
          source: "",
          directory: "/tmp/djinn/.claude/skills/notion-ticket",
          description: "",
          missing: "",
        },
      }),
    ),
    /<span class="config-from">\.claude\/skills<\/span>.*<p class="skill-role to-fix">Djinn cannot use all of it: see “To fix”\.<\/p>/,
  );
  const summoned = skill({
    source: "dolmen/recette",
    name: "recette",
    directory: "/tmp/dolmen/.agents/skills/recette",
  });
  assert.match(
    summoned,
    /<span class="badge muted">from dolmen\/recette<\/span>/,
  );
  assert.doesNotMatch(summoned, /config-from/);

  const settings = render(
    h(s.ProjectSettings, {
      shown,
      settings: [
        {
          name: "provider",
          value: "claude",
          source: s.SettingSource.DEVELOPER,
        },
        { name: "model", value: "", source: s.SettingSource.DEFAULT },
        {
          name: "checks",
          value: "lint: go tool task lint (commit)",
          source: s.SettingSource.REPOSITORY,
        },
        { name: "babysit", value: "true", source: s.SettingSource.DEFAULT },
        {
          name: "merge_main",
          value: "release",
          source: s.SettingSource.REPOSITORY,
        },
      ],
    }),
  );
  // The checks have their own part of the panel; the settings a file sets come first, the defaults folded.
  assert.doesNotMatch(settings, /lint: go tool task lint/);
  assert.match(
    settings,
    /<div class="config-setting"><code>provider<\/code><span class="">claude<\/span><span class="config-from" title="\/home\/you\/\.config\/djinn\/projects\/p1\/settings\.txtpb">your file<\/span><\/div><div class="config-setting"><code>merge_main<\/code><span class="">release<\/span><span class="config-from" title="\/tmp\/djinn\/\.agents\/settings\.txtpb">the team(?:'|&#x27;)s file<\/span><\/div><details class="config-more"><summary>2 more at Djinn(?:'|&#x27;)s default<\/summary><div class="config-setting"><code>model<\/code><span class="muted-text">not set<\/span>/,
  );
});

// The journal of changes (W151): each commit of the reference branch that changed the configuration, when, which and
// who, what it changed in sentences; one that waits for your look has the flame, its asking lines marked, and Seen
// with what it does; the others say they were applied. A failed fetch is said with git's own words.
test("the journal of configuration changes says each change, and marks seen those that wait for your look", () => {
  const at = { seconds: 1760000000n, nanos: 0 };
  const seen = [];
  const waiting = {
    id: "c1",
    ref: "origin/main",
    commit: "7290747aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
    author: "Skayfa",
    title: "Allow curl",
    commitTime: at,
    commits: 1,
    firstLook: false,
    lines: [
      {
        text: "Permissions: 1 more command the workers may run: curl.",
        asks: true,
      },
      { text: "New check e2e before a push: go tool task e2e.", asks: false },
    ],
  };
  const applied = {
    ...waiting,
    id: "c2",
    commit: "bf53c16bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
    title: "Change recette",
    lines: [
      { text: "The skill recette changes its instructions.", asks: false },
    ],
  };
  const history = { ...applied, id: "c3", firstLook: true, commits: 3 };
  const out = s.renderToStaticMarkup(
    h(s.ConfigChanges, {
      watch: {
        ref: "origin/main",
        fetchError: "fetch main from origin: could not read Username",
      },
      changes: [waiting, applied, history],
      busy: "",
      onSeen: (c) => seen.push(c.id),
    }),
  );
  assert.match(
    out,
    /<h3>What changed on origin\/main<span class="config-from">watched every 5 min<\/span><\/h3>/,
  );
  assert.match(
    out,
    /<pre class="notice-detail">fetch main from origin: could not read Username<\/pre>/,
  );
  assert.match(
    out,
    /<div class="config-line config-change waits">.*<code>7290747<\/code><span>Skayfa<\/span><\/p><strong>Allow curl<\/strong>/,
  );
  assert.match(
    out,
    /<li class="asks">Permissions: 1 more command the workers may run: curl\.<\/li><li class="">New check e2e/,
  );
  assert.match(
    out,
    /Seen<\/button><p>Djinn applies it already: the gear stops lighting for it\./,
  );
  assert.match(out, /<code>bf53c16<\/code>.*Applied, nothing to decide\./);
  assert.match(
    out,
    /3 commits.*Before Djinn watched it: applied, nothing to decide\./,
  );
  assert.doesNotMatch(out, /button accent/);
  assert.equal((out.match(/Seen<\/button>/g) ?? []).length, 1);

  const none = s.renderToStaticMarkup(
    h(s.ConfigChanges, {
      watch: { ref: "origin/dev", fetchError: "" },
      changes: [],
      busy: "",
      onSeen() {},
    }),
  );
  assert.match(
    none,
    /Nothing changed in the configuration since Djinn watches it\./,
  );
  assert.doesNotMatch(none, /notice-detail/);
});

test("the Summoning Circle lists each project with its agent, worker model and question investigator pickers, where each value comes from, and Take back when set in your file", async () => {
  const p1 = { id: "p1", name: "djinn", git: true, directory: "/tmp/djinn" };
  const p2 = { id: "p2", name: "lamp", git: true, directory: "/tmp/lamp" };
  const shownByProject = {
    p1: {
      project: p1,
      repositoryFile: "/tmp/djinn/.agents/settings.txtpb",
      developerFile: "/home/you/.config/djinn/projects/p1/settings.txtpb",
      settings: [
        {
          name: "provider",
          value: "antigravity",
          source: s.SettingSource.DEVELOPER,
        },
        {
          name: "model",
          value: "Gemini 4 Argon (High)",
          source: s.SettingSource.DEVELOPER,
        },
        {
          name: "question_model",
          value: "gemini-3.8-flash-high",
          source: s.SettingSource.DEVELOPER,
        },
      ],
    },
    p2: {
      project: p2,
      repositoryFile: "/tmp/lamp/.agents/settings.txtpb",
      developerFile: "/home/you/.config/djinn/projects/p2/settings.txtpb",
      settings: [
        {
          name: "provider",
          value: "claude",
          source: s.SettingSource.REPOSITORY,
        },
        {
          name: "model",
          value: "",
          source: s.SettingSource.DEFAULT,
        },
        {
          name: "question_model",
          value: "",
          source: s.SettingSource.DEFAULT,
        },
      ],
    },
  };
  const transport = s.createRouterTransport(({ service }) => {
    service(s.ProjectService, {
      show: (req) => shownByProject[req.project],
    });
  });
  const djinn = s.createDjinn(transport, 10);
  const html = s.renderToStaticMarkup(
    h(
      s.DjinnProvider,
      { djinn },
      h(
        "div",
        null,
        h(s.SummoningProject, { project: p1, initialShown: shownByProject.p1 }),
        h(s.SummoningProject, { project: p2, initialShown: shownByProject.p2 }),
      ),
    ),
  );
  // Project 1 has developer settings: shows "your file" with hover path, Argon option selected, and Take back button.
  assert.match(html, /<strong>djinn<\/strong>/);
  assert.match(
    html,
    /title="\/home\/you\/\.config\/djinn\/projects\/p1\/settings\.txtpb">your file<\/span>/,
  );
  assert.match(
    html,
    /<option value="antigravity" selected="">Antigravity<\/option>/,
  );
  assert.match(
    html,
    /<option value="Gemini 4 Argon \(High\)" selected="">Gemini 4 Argon \(High\)<\/option>/,
  );
  assert.match(
    html,
    /<button type="button" class="button small secondary"[^>]*>Take back<\/button>/,
  );
  // Project 2 has team/default settings: shows "the team's file" and "Djinn's default", and no Take back button in its card.
  const p2Card = html.slice(html.indexOf('aria-label="lamp"'));
  assert.match(
    p2Card,
    /title="\/tmp\/lamp\/\.agents\/settings\.txtpb">the team(?:'|&#x27;)s file<\/span>/,
  );
  assert.match(p2Card, />Djinn(?:'|&#x27;)s default<\/span>/);
  assert.match(
    p2Card,
    /<option value="" selected="">Agent(?:'|&#x27;)s default \(claude-sonnet-5-5\)<\/option>/,
  );
  assert.match(p2Card, /<option value="" selected="">Same as worker<\/option>/);
  assert.doesNotMatch(p2Card, />Take back<\/button>/);
  assert.doesNotMatch(html, /button accent/);
  assert.equal(s.shortModel("Gemini 4 Argon (High)"), "gemini 4 argon");

  // Empty state when there are no projects.
  const empty = s.renderToStaticMarkup(
    h(
      s.DjinnProvider,
      { djinn },
      h(s.SummoningCircle, { projects: [], onClose() {} }),
    ),
  );
  assert.match(empty, /Summoning Circle/);
  assert.match(empty, /No project yet\. Add one first\./);
});
