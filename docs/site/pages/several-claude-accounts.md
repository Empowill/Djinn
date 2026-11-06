# Configure several Claude accounts

With two Claude subscriptions on one machine, Djinn runs its workers on both, and spends first the week that ends
first. Each extra subscription is an account: a folder of Claude Code's own, beside its default one, where Claude Code
keeps that account's login. Djinn makes the folder and runs the sign-in; it keeps no token and shows none.

## Before you start

- **Claude Code is installed and signed in** on its own account: its line in the agents' setup reads Ready. That is
  the agent's own sign-in, the first subscription.
- **You can sign in to the second subscription**: its email and password, or its single sign-on.
- **A browser on this machine**: the sign-in may open a page there.

## Add the account, step by step

1. Open the agents' setup: **Connections & preferences** at the bottom of the side panel, or **Set up the agents** at
   the end of the menu of the Djinn's seat, the chip at the top of a wish.
2. On the panel's last line, the one that starts with a plus, choose **Claude Code** in the first list.
3. Type the account's **name**, such as work: lowercase letters, digits and dashes (the format is below).
4. Type a **label** if you want one, such as Work account: any text. The window shows it instead of the name.
5. Click **Add**. Djinn makes the folder: in your home folder, a hidden folder named .claude- followed by the name
   (for work, .claude-work beside .claude). The account's line appears under Claude Code, and reads Not signed in.
6. Click **Sign in** on the account's line. The panel's terminal runs Claude Code's own sign-in in that folder, and a
   page may open in your browser. Check that the page shows the second subscription before you accept: a browser
   already signed in to the first one signs that one in again.
7. When the terminal ends, Djinn checks the agents again: the account's line reads Ready, and the line under it reads
   Active. Its gauges show after Djinn's first reading of its quota, within a minute.

## The format of each field

| Field | Format | Valid example | Refused |
| --- | --- | --- | --- |
| Agent | Claude Code or Codex. Antigravity keeps no folder per sign-in, so it has no accounts. | Claude Code | Antigravity |
| Name | 1 to 32 characters: lowercase letters from a to z without accents, digits and dashes; the first one a letter or a digit. The window drops spaces at either end. Unique among the accounts. | work, pro, claude-2 | Work, work account, me@example.com, -pro, a letter with an accent |
| Label | Any text, up to 80 characters, or nothing: the name shows then. | Work account | more than 80 characters |
| Folder | Not asked in the window: Djinn takes .claude- followed by the name, in your home folder. From the command line, another absolute path, made if missing. | .claude-work | Djinn's data folder or a folder in it, your home folder, Claude Code's default folder |

The name is this strict because it becomes a folder's name and a word of the command line: djinn lead pause-account
work names the account work. The window checks the same rule as the server, and a name it refuses says the rule under
the line, with an example that passes it. From the command line, djinn lead add-account takes the same name, the
agent and the label; djinn lead accounts lists the accounts.

## How Djinn shares the workers

- **The week that ends first is spent first.** Each new task of Claude Code starts on the sign-in whose week resets
  first, among those neither paused nor limited that have quota left: under 95 % of their 5 hours and of their week.
  What is left of a week is lost at its reset. Two weeks that end within an hour of each other go to the one with more
  left to lose.
- **A task stays where it ran.** A task that ran on a sign-in goes back to it while that one is free and has quota
  left: its session is there.
- **Djinn reads each quota.** Every 5 minutes, Djinn asks Claude Code the usage of each sign-in, without a model turn;
  each worker also tells its own at each of its turns. A reading older than 15 minutes counts as unknown. When Djinn
  knows no quota, it takes the agent's own sign-in, then the accounts in the order you added them.
- **A limit moves the task.** A worker that hits the limit of its account moves at once to another sign-in, its session
  copied into that account's folder. The account stays limited until its reset; with no other sign-in free, the task
  waits for the reset.
- **The window shows it.** Under each sign-in's line: Active, Paused or Limited until its reset, the week's gauge and
  when the week ends, its 5 hours used, and Next task here on the sign-in a new task would take now. A task's card
  names the account it runs on by its label.

More on the rules, the quota probe and the sessions that follow a task: [the Djinn](#the-djinn).

## Pause an account

- **Pause** on an account's line keeps every new worker off it; **Resume** brings it back. **Pause** on Claude Code's
  own line does the same for its own sign-in.
- A worker running there finishes its turn; the next ones start on another sign-in. While every sign-in of the agent
  is paused, its tasks wait, saying so.
- A pause wins over a limit: a sign-in both paused and limited reads Paused.
- From the command line: djinn lead pause-account work, then djinn lead resume-account work.
- **Forget** an account with the cross on its line: Djinn no longer names it, and its folder, with the login in it,
  stays as it is.

## Common errors

| What you see | Why | What to do |
| --- | --- | --- |
| The name is refused, with the rule under the line | A capital, a space, an accent, an at sign, or a dash first | Type the name in lowercase, such as work; put the words you want in the label |
| An account is called work already | Another account has this name | Choose another name, or forget the old account first |
| The account work has that folder already | Another account uses the same folder | Forget it, or choose another name, which takes another folder |
| The line still reads Not signed in | The sign-in did not end: the browser page was left, or the panel closed, which hangs up the command | Click Sign in again and finish on the browser page |
| Both sign-ins show the same week | The account signed in to the first subscription | Forget the account, add one with another name, and check the browser page shows the second subscription before you accept |
| Quota not read yet | Djinn has not read it since the account was added | Wait a minute: the panel reads it again every minute while it is open |
| Quota could not be read | The probe failed: the account is signed out, or Claude Code did not answer; the hover says which | Sign in again if it is signed out |

## Read next

Related: [the Djinn](#the-djinn), [Usage and cost](#usage-and-cost), [Getting started](#getting-started).
