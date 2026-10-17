# The window

Everything you do in Djinn, you do in its window: make a wish, answer a question, follow the workers, watch the
machine. It has four parts: the **side panel** on the left, with your wishes and projects; the **page** in the middle,
a wish, the flying carpet or the Hourglass; the **terminal** pinned at the bottom, with the Djinn's tab; and a thin
**status bar** below.

## Why it is laid out this way

Djinn runs several wishes at once, and you have one pair of eyes. The window puts what needs you first and keeps the
rest one click away: the side panel says where each wish stands without opening it, each page opens on what waits for
you, and the lists you read never move under you. A new item comes below the older ones, a revised one keeps its place.

## The side panel and the wish bar

From top to bottom:

- **New wish**, Ctrl+N (Cmd+N on macOS).
- **Flying carpet**, Ctrl+1, while a wish is active or the inbox has news: every active wish together. Its counts say
  how many questions wait for you across them, and how many cards wait in the inbox.
- **Wishes**, with how many are active out of three: the wish bar, below.
- **Projects**, with + to create one. Each project opens its panel; its terminal button opens a terminal at its root,
  and a line shows each of its branches out of step with its remote.
- **Import a wish**, **Hourglass** (Ctrl+2), **Grimoire**, this documentation, and your profile, **Connections &
  preferences**.

### The wish bar

The wish bar lists the active wishes by rank, the first first, then the **Paused** ones, then the granted ones,
folded under their count. Each active wish shows its state's icon and title, and a second line of counts, each left
out when it is zero:

| Count | What it says |
| --- | --- |
| Flame | Ifrits lit |
| Orange | Questions waiting for your answer |
| Blue | Workers running |
| Merge | Work waiting to merge, no worker running |
| Violet | Questions being investigated |
| Wrench | Merges being fixed: agents correct them, nothing to do |
| Teal | Watchers watching |
| Ready to grant | The wish is done for you to grant |
| #3 | Its rank, never a count |

Drag a wish by its grip, on its left, or press Alt+Up or Alt+Down on it, to change its rank; a thin line shows where it
will land. Drop a paused or granted wish among the active ones to make it active at that rank: the last active wish
is then paused. **Make it the first active wish**, beside a paused wish, does it in one click. See [Wish](#wish).

Ctrl+\ (Cmd+\ on macOS), or the button at the top, folds the side panel into a rail of icons; Djinn remembers it. In a
narrow window, under 820 pixels, the panel keeps only its icons by itself: a wish with open questions then shows a
raised hand in place of its icon.

### The personal space

The **personal space**, at the bottom of the side panel, keeps your own settings: the language, the theme, the text
size and the Djinn's eye. Compact and Very compact shrink the whole window, its spacing with its text, to see more at
once; Large grows it. The documentation opens in the window's theme and text size.

## The flying carpet

The flying carpet shows every active wish at a glance. Its head holds the load gauge and **Talk to the Djinn**. Below:

- **What waits for you**, the bar at the top, while something does: what keeps a wish from moving forward, the most
  blocking first. A click takes you to it. See [Question and move](#question-and-move).
- Its tabs: the flying carpet itself, then **Tasks** and **Decisions** of every active wish together.
- The [inbox](#inbox), when a source found something, or one of yours fails.
- The questions column on the left, the workers glance on the right.
- A last line, how many workers run and the memory used, with **See it live in the Hourglass**.

When nothing waits and nothing runs, it says so: **Nothing waits for you, and nothing runs.**

A wish's own page has the same layout for that wish alone, under its tabs: Wish, Tasks, Review, Decisions, Tilasms and
For agents. See [Wish](#wish).

## The questions column

The **Questions** column holds every open question that waits for you, those that can wait included, the oldest first:
a new question comes below, a revised one keeps its place, one put off goes to the bottom. On the flying carpet they
are grouped by wish, the first wish first, each under its title and count; a click on the title opens the wish.

One question is open in full, the others fold to two lines. The arrows move from one to the next, across wishes, and
the keys answer it. When the open one is answered, the one below opens. Below the column come **Your move**, the
workers that wait for you and the wishes ready to grant, then the questions **Being investigated**. The keys and the
levels are in [Question and move](#question-and-move).

## The Djinn's eye

The seal at the top of the side panel is the Djinn's eye, drawn from it: its eight-pointed star for the iris, its
saffron flame for the pupil. Solar and wide open in the light theme, a mocking demon with a half-lowered lid in the
dark. The eye opens and goes where to look now:

| Where it rests | When |
| --- | --- |
| In the margin of a question, watching it | You just arrived: the question that matters most, one a worker waits for, then one needed before something, then the oldest |
| In the margin of a question, reading it | The question you went to, by a click or the arrows |
| On **Write to the Djinn**, which it lights a little | No question is open |
| Home, in the seal | A page with none of those; closed while the lead is stopped; pale while Djinn's stream is cut |

From one place to the next it flies on a curve, slowly enough to be followed, and lands in the margin: at rest, it
covers nothing and scrolls with what it watches. It never moves while you type in a field, press a mouse button, scroll
or read a dialog, nor while the window is in the background: it waits, then goes to the last place asked. It never takes
the focus or a click. Its flame shines while something waits for you: a question a worker waits for, a worker waiting,
a lit ifrit. With less motion asked by the system, it fades out and in, without flying.

In the personal space, **The Djinn's eye** chooses **Alive**, **Discreet**, where it stays in the seal and only
changes its state, or **None**.

## The workers glance

**What runs**, beside the questions, says what each wish's workers are doing, in a line each: a mark, the task's code
and title, and what it waits on. A click on a line opens its task.

| Mark | What it means |
| --- | --- |
| Filled blue dot, pulsing | Running |
| Filled violet dot | Investigating a question, named after "on" |
| Hollow blue circle | Waiting on a task, under that task when it is of the same wish |
| Dotted grey circle | Planned |
| Flame | A lit [ifrit](#ifrit) |
| Eye | The wish's watchers, in one line, with the requests they watch |

Below its workers, each wish shows the work on its way into its branch, and the workers that repair it:

| Mark | What it means |
| --- | --- |
| Blue merge arrow | A batch being merged: which tasks, into which branch, the check it runs (lint, test) through the gate of its name, and for how long; or that it waits for its gate |
| Grey merge arrow | A task's work waiting in line, in the order Djinn merges it, with why when something holds it |
| Blue cloud | The push checks running, or the push itself, and for how long |
| Salmon cloud, crossed | A push its checks hold: which check went red, and which tests failed |
| Wrench | A repair worker: blue at work, grey to come; it settles a conflict, makes red checks or the push checks pass, settles main's merge, or commits work a worker left not committed |

A click on a line opens its task in the Tasks tab. A held push opens what failed: the check, its command, the end of
its output, and the correction at work, which opens in turn. See [Merges and corrections](#merges-and-corrections).

On the flying carpet each wish has its title and counts, "1 running, 2 to come, 1 merging, push held, 1 repair"; a
legend names only the marks shown. It stays in view while a long question scrolls. In a narrow page it shrinks to a
line per wish, a dot per worker, a wrench per repair worker, one arrow for the merges and one cloud for a held push,
and comes before the questions. Nothing in it loops: a time shown counts on only while the window is in front.

## The Hourglass

**Hourglass**, Ctrl+2, is where the live metrics are, as time goes by: what the machine and its workers use, minute by
minute, and where the time of the commands goes. Pick the time shown, from 10 minutes to 7 days.

- **The machine.** Its CPU and memory: the whole machine, Djinn with everything it started, and Djinn alone, with the
  load limit and the gates held.
- **Workers running.** A row per worker: its project, model, time, turns, cost, gate, and its own CPU and memory
  graphs. Its eye opens the task in its wish.
- **Where the time goes.** The commands by project and by gate: how often they ran, how long, how long they waited for
  their gate.

A running task's card links to its row, **Live in the Hourglass**. The page reads its figures only while it shows:
leave it, or fold its panel, and the reads stop. See [Machine and load](#machine-and-load).

## The load gauge

**Operating load**, in the middle of each page's head, sets how much of the machine Djinn may take: **Minimal**,
**Medium**, **High**, **Max** or **Overclock**, each allowing more workers and more memory, and **Auto**, which steps
down to Minimal while you work at the keyboard and climbs back when you leave. Hover a notch, or reach it by the keys,
to see what it would allow before you choose it: how many workers, how much memory, and which workers would pause.

Lowering the load pauses the newest workers above its limit, and new tasks wait; the paused ones resume first once a
place frees. In Auto, or on Windows, they finish their run instead. Overclock warns that other applications may slow
down. See [Machine and load](#machine-and-load).

## The terminal

The **Terminal**, pinned at the bottom, holds tabs: **The Djinn** first, then the shells you open, Ctrl+Shift+T from
anywhere. A click on its title folds it, its top edge resizes it, and Djinn remembers both. A shell opens only when you
ask for one. When the Djinn's program ends, its tab stays, with its exit code and a button to start it again.

The window's own keys never take a key typed in the terminal.

## The status bar and the update banner

The status bar says whether the window is live with Djinn, or reconnecting, how many workers run of how many may, and
the gates held. When the machine is under pressure, it says why.

When a newer Djinn is ready, a banner takes its own row at the very top of the window, over nothing. See
[Restarts and updates](#restarts-and-updates).

## Keys

| Keys | What they do |
| --- | --- |
| Ctrl+N (Cmd+N) | New wish |
| Ctrl+1 (Cmd+1) | The flying carpet |
| Ctrl+2 (Cmd+2) | The Hourglass |
| Ctrl+\ (Cmd+\) | Fold or unfold the side panel |
| Ctrl+I (Cmd+I) | Write to the Djinn |
| Ctrl+Shift+T | A new terminal |
| Alt+Up, Alt+Down | Move the wish in focus in the wish bar |
| Escape | Close the Grimoire, a dialog or a menu |

Ctrl+1 and Ctrl+2 are keys' places, not characters: on a French keyboard, the keys of the ampersand and of the e with an
acute accent; the numpad's 1 and 2 work too. They work in a text field, never in the terminal. Each entry's tooltip
names its key as your keyboard writes it.

The **global shortcut**, set in Connections & preferences, brings Djinn forward from any application.

## Common mistakes

- **Looking for a question that can wait in the bar.** The bar holds only what holds a wish up; the questions column
  holds them all.
- **Reading the rank as a count.** The last figure of a wish's line, #3, is its rank.
- **Leaving the Hourglass open to keep figures.** It reads only while it shows; Djinn records the machine's history
  anyway, for a week, across restarts.
- **Waiting for the eye to move while you type.** It waits on purpose: it goes once you leave the field.
- **Closing the window to stop the work.** Closing the window pauses nothing: active wishes go on while Djinn runs.
  Pause a wish to stop its workers.

## Read next

- [Wish](#wish): a wish's page and its tabs.
- [Question and move](#question-and-move): the bar, the column and the keys.
- [Machine and load](#machine-and-load): the Hourglass and the load in depth.
