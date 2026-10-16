# Push strategy

A wish's push strategy says which branches its work lands in, and so which pull requests it ends in:

- **Per wish**, the default: one [integration branch](#integration-branch) per wish and project. All the wish's work
  lands there, and the wish ends in one pull request.
- **Per azima**: one branch per [azima](#azima), cut from the project's main branch. Each azima's work lands in its own
  branch, which lands in main through its own pull request.

## Why two strategies

A wish is sometimes one change, sometimes several. "Let customers export their invoices as PDF" reads best as one
pull request. A wish that gathers three tickets, a bug, a feature and a clean-up, reads best as three: each reviewed
on its own, each merged when it is ready, none waiting for the others.

Per wish keeps it simple: one branch, one review. Per azima gives each subject its branch and its review, at the price
of keeping the subjects apart.

## Choosing one

The top of each wish shows a switch, **Per wish** or **Per azima**, beside the chip that says where its work lands. Its
tooltips say what each means: *One integration branch per wish and project*, *One integration branch per azima*.

A wish takes, in order:

1. **Its own choice**, made with the switch, or by the Djinn.
2. **The project's default**, from the project's settings: the team's file, or yours. The window has no switch for it;
   ask the Djinn, or write it in the settings. See [Project and settings](#project-and-settings).
3. **Per wish.**

Choose before any work is merged: switching later is possible, but Djinn protects the work already merged, as below.

| | Per wish | Per azima |
| --- | --- | --- |
| Branches | One per wish and project, the one your checkout was on, or one you name | One per azima, cut from main, plus the wish's branch for work of no azima |
| Pull requests | One for the whole wish | One per azima |
| Azimas may wait for each other | Yes | No |
| The top of the wish says | **Lands in** and the branch | **Each azima lands in main** |
| Suits | One change, or subjects that build on each other | Several independent subjects, tickets, or reviews |

## Per wish

The wish's work lands in its integration branch, which Djinn pushes by the project's [cadence](#push). The chip at the
top of the wish names the branch and changes it. See [Integration branch](#integration-branch).

When the wish has settled, no worker running and no work waiting to merge or failed, its branch pushed as it is, its
push neither held nor refused, and the branch holds commits main lacks, Djinn proposes its pull request: *Open a pull
request to main?*, with a drafted title and description, answered **Open pull request** or **Not yet**. A pull request
already open for the branch takes the new work as it is pushed: Djinn closes its own question, saying the request is
open. See [Pull and merge requests](#pull-requests).

## Per azima

Each azima has a branch of its own, named after its code and title: for T07, "The orchestrator",
djinn/T07-the-orchestrator.

- **Cut from main** when the azima is made, in a project that names checks; otherwise when its first part starts, or
  its first work is merged. Its card says *cut from main at* the commit, or *cut from main when its first part starts*,
  and *lands in main*. A title changed later keeps the branch's name.
- **Its work merges into it**, checked, as into a wish's integration branch. The work of a task part of no azima goes to
  the wish's integration branch.
- **It keeps up with main**, by the same rules as a wish's branch.
- **It is pushed** by the project's cadence: under Standard, at the latest when the azima ends. Its card shows the branch's line when it
  differs from the remote, with its push button, and **Pushed** when it does not.
- **It lands in main** through its own pull request.

### An azima's pull request

Once every part of the azima is done and committed and its branch pushed, Djinn proposes its pull request: *Open a pull
request for T07 to main?*, answered **Open pull request** or **Not yet**. Its card follows it:

| The card shows | What it means |
| --- | --- |
| **PR proposed** | Djinn asked you whether to open it. |
| **PR open** | The request is open, as a draft at first; a click opens it on the forge. Each push of the branch updates it. |
| **PR merged** | Merged into main: the azima is done. |

When the request can be merged and its checks are green, Djinn tells you: *Pull request #12 for T07 is ready to
merge?* Merging is yours: Djinn only notes your answer, and marks the azima done once the forge says it is merged.

### No azima waits for another

Each azima's branch holds main and its own work, nothing else. An azima that waits for another would need that
other's work, which its branch does not have. So, per azima:

- Djinn refuses to make one azima, or its work, come after another's.
- Djinn refuses to switch a wish to per azima while its azimas depend on each other, naming them.

Tasks inside one azima still wait for each other as usual. See [Dependencies](#dependencies).

### A branch name of your own

A project whose branches follow a convention, a ticket's key for instance, can give an azima a branch name of its own:
ask the Djinn. A branch that already exists, here or on the remote, an open pull request's for instance, is adopted as
it is, never cut again nor rewritten, and its open pull request becomes the azima's.

In a project that names no check, Djinn merges an azima's work, untested, only into a branch named this way, never into
one it named itself.

## Switching

The switch changes where the next work lands. It never moves work already merged, and Djinn refuses a switch that would
strand it:

- **To per azima**, when an azima's work is already merged into the wish's integration branch: the azima would get a
  fresh branch from main, without that work.
- **To per wish**, when work is merged into an azima's branch that is not the wish's integration branch.

The refusal names the work and the branch it lives on. The way through is to have the azima **adopt** the branch its
work lives on, then switch: the azima's work keeps landing there, its pull request already open stays its own, and
nothing is cut again. Ask the Djinn to do it.

Work of no azima stays in the wish's integration branch either way.

> **Example.** A wish fixes ticket ET-1234 per wish, on the branch ET-1234-the-fix, its merge request open. A second
> ticket joins the wish. You want two merge requests: the Djinn has azima T1, which carries the first ticket, adopt
> ET-1234-the-fix, switches the wish to per azima, and makes T2 for the second ticket, on its own branch from main.

## Good to know

- **Decide early.** Per azima suits a wish split from the start into independent subjects; switching after work is
  merged takes an adoption first.
- **Per azima needs independent azimas.** If one part needs another's work, they belong to the same azima, or the wish
  goes per wish.
- **One held push holds the wish's new work.** Per azima too, while a push of the wish is held in a project, its new
  tasks there wait, whichever azima they belong to. See [Push](#push).
- **Merging stays yours.** Djinn proposes and opens pull requests as drafts; it never marks them ready nor merges them.

## Read next

- [Azima](#azima): the parts of a wish's plan.
- [Integration branch](#integration-branch): the branch per wish.
- [Pull and merge requests](#pull-requests): how a request is opened, watched and reviewed.
