# Tilasm

A tilasm, or talisman, is the material that explains a wish: a concept drawn, a data model walked through, a
comparison laid out side by side, a mockup. It is a small web page, a folder with its index page and everything that
page needs, coded L01, L02 and on in its wish, versioned, and kept in Djinn's data folder on your machine, never in a
project.

Where a [block](#block) is a note the agents write for one another, a tilasm is written for you: the page you open
to understand a design, or to choose between options with full light.

## Why it exists

Some things do not fit in a sentence. A question must read at a glance, so that you can decide; a plan's notes are
plain text. A diagram of how two services talk, three options laid out with what each changes, a screen that does not
exist yet: these need a page, with drawings and room.

A tilasm is that page. The Djinn makes it once and links it wherever it is needed, a question, a block, the brief,
rather than explaining the same thing again in each. Keeping it out of the projects means it never lands in a
commit, and its versions keep how the explanation grew.

## What a tilasm holds

| Part | What it is |
| --- | --- |
| Code | L01, L02 and on, unique in its wish. |
| Title | The page's own title, unless one is given. A new version keeps it. |
| Files | An index page at the folder's root, and its sources: styles, scripts, images, fonts. 50 MB at most per version. |
| Versions | Every version kept, numbered, with who put it, when, how many files and how large. |
| Explains | The [azimas](#azima) and [tasks](#task-and-worker) of the wish it explains. |
| Link | Its own address, which opens Djinn on it from anywhere. |

## When the Djinn makes one

The Djinn's rules tell it to make a tilasm to explain a concept, and to cite it by its link rather than explain it
again. Most often you meet one through a question:

- **A question too long to read at a glance.** Outside its commands, a question's context fits on one screen of the
  window, 1200 characters at most. When the choice needs more, an analysis, a comparison, a mockup, a design, the
  details go in a tilasm that lays out the choices, one section per option, and the question links it. Djinn warns
  whoever asks a longer question, and suggests a tilasm.
- **A design to see.** A worker asked to design a screen makes the mockup as a tilasm, and the question links it.
- **A concept for the workers.** The Djinn can hand a tilasm to a worker as part of its task, its text included, so
  that the worker starts from the same picture.

The Djinn links a tilasm the wish already has before it makes another one. A worker can put tilasms in its own wish
only. A worker sent by **Enlighten me** writes no file: it links a tilasm the wish has, or has another worker make
one. See [Enlighten me](#enlighten).

## Opening one

A wish's **Tilasms** tab lists its tilasms, the newest first, with their count on the tab. Click a tilasm's title to
open it there (**Open it here**): it shows in a frame under its code, its title and its version, with **Full size** to
give it the whole page, and **Close the tilasm**. Escape, or **Leave full size**, brings the page back.

- **From a question.** A tilasm's link in a question opens it full size over the page you read, without leaving it.
  Close it, or press Escape, and you are back on the link, ready to answer.
- **From a block or the brief.** A link elsewhere in the window opens the wish's Tilasms tab on that tilasm.
- **From outside.** The link opens Djinn on the tilasm from a browser, a chat, a terminal or a note, and starts Djinn
  if it does not run.

A link to a tilasm that is not on this machine says so: Djinn does not know it.

### What a tilasm may do

A tilasm runs in a frame of its own, closed to the rest:

- **Its scripts run**, its own and those in its page: a diagram can move, a demonstration can react to a click.
- **The network is out of its reach.** It loads nothing from the internet and sends nothing: no font from a service,
  no script from elsewhere, not even a request for a file of its own folder. All it needs is in its folder, or in its
  page.
- **Djinn is out of its reach.** It cannot read Djinn's data, nor call Djinn, nor send a form.

That is what lets you open any tilasm safely, whoever made it.

## Comments

Read a tilasm and say what it calls for where you read it. **Comments** (the speech bubble on its head, with the count
of your drafts) opens a column beside the frame, full size too; **On the whole tilasm** writes a comment on the
whole of it. To comment a passage, select it in the page, as to copy it: a **Comment** button shows under it, and
Ctrl+Alt+M does the same. The card keeps the passage and the heading above it, and the passage stays highlighted in
gold in the page, again after a reload. To comment what you cannot select, a figure, a drawing, a table row, choose
**Aim at an element** on the tilasm's head: the block under the mouse is framed, a click takes it, and Escape leaves.
Numbered dots in a margin beside the frame stand at the height of each comment in the page and follow it as you
scroll; a click on a dot opens its card, and a click on a card's anchor shows it in the page. A comment is a draft: it stays in Djinn's store on this machine, survives a
restart, and no agent reads it. Edit one in place (Ctrl+Enter saves, Escape cancels); delete it at once, and **Undo**
puts it back for five seconds. Your drafts are counted in gold on the tilasm's head, on its row and on the
**Tilasms** tab.

The page stays out of Djinn's reach: Djinn adds a small script to each page it serves, never to the tilasm's files,
which tells the window what you select or aim at and highlights the passages, through the one channel a sandboxed
frame keeps.
A tilasm that sets a content security policy of its own, in a meta tag, may refuse that script: its page reads as
before, and you comment the whole of it.

### Send them to the Djinn

Nothing leaves until you send, and nothing costs before. At the foot of the column, write a last word on the whole
tilasm if you like, then choose:

- **Launch**: the Djinn launches the work the tilasm explains, as your comments change it. With no comment at all,
  Launch is your yes to the question that links the tilasm: it gets its recommended option, as approving it would.
- **Iterate**: the Djinn makes the tilasm's next version, as your comments say.

A line confirms the send (Enter sends, Escape gives up). Every draft and your last word leave together, in one send:
the Djinn reads them all at once, each with what it is on, and your choice.

A tilasm greys its comments and both buttons once its work was launched, or while a task works on it or makes its
next version; a line says why, and which task or question. A send the Djinn has not read yet holds the buttons only.
Launch never goes twice: Djinn refuses it, whatever the window shows.

### The Djinn's replies

A sent comment keeps its card, in its place, marked sent; it no longer changes. The Djinn replies to each one: what
it did with it, or why not, and the tasks it made of it, named under the reply. A comment it did not understand gets
a reply that asks. The card then reads **Replied**. Until each comment has its reply, the wish's brief reminds the
Djinn which ones wait; your drafts never show there.

### A new version

A comment belongs to the tilasm, not to a version. When its next version comes, the frame finds each comment again:
the same passage or element, perhaps moved, shows as before; one that changed but keeps most of its words says
**Changed** on its card, the new passage above the one you wrote on; one that is gone waits under **Detached**, its
quote struck, never lost. **Reattach** puts a detached draft on a passage you select or an element you aim at;
otherwise it leaves as it is, and the Djinn reads it gone. Where each comment stands is kept with it, and the Djinn
reads it with the comment.

## Making one yourself

Drop it on the Tilasms tab: an HTML file, a folder with an index page at its root, or a zip file of one. A click on
the tab chooses a file instead. The new tilasm takes the next code and opens at once. Hidden files of a dropped
folder are left out, and a drop over 100 MB is refused.

A zip file that a tilasm's export wrote is brought back as that tilasm: a new version of it when the wish has it, a
new tilasm otherwise.

## Versions, search and export

- **Versions.** Each tilasm lists its versions. **Restore this version**, on any but the latest, brings an older one
  back as a new version: nothing is lost, the others stay. The Djinn's new version of a tilasm keeps its code and its
  links.
- **Search.** The field at the top of the tab keeps the tilasms whose code, title or text holds every word you type,
  case ignored. Only the text of each one's index page counts, in its latest version.
- **Export as a .zip** writes the latest version's files to your Downloads folder, ready to send or to drop in another
  wish.
- **Sharing a wish** carries its tilasms, every version. See [Sharing a wish](#sharing).

## What to expect

- **A tilasm is the Djinn's smoke.** It explains; it decides nothing, and nothing in Djinn computes on it. What holds
  is in the question it supports, and in the decision you take.
- **It looks the same in light and dark.** The Djinn's rules have it draw its diagrams by hand, with boxes, labelled
  arrows and a legend, in colours that read in both themes.
- **It stays on this machine** unless you export it or share its wish.

## Common mistakes

- **Expecting a tilasm in the project.** It never is: it is in Djinn's data folder. To keep one with the code, export
  it and commit it yourself.
- **A page that loads from the internet.** A font, a script or a picture from a website does not load: everything must
  be in the folder. A page that fetches its own data files fails too; the data belongs in the page or a script.
- **Searching for text of a second page.** The search reads the index page only.
- **Asking for a longer question rather than a tilasm.** When a decision needs details, ask the Djinn to lay them out
  in a tilasm: the question stays short and the details stay one click away.

## Read next

- [Question and move](#question-and-move): where most tilasms are linked.
- [Block](#block): the notes written for the agents, not for you.
- [Wish](#wish): what a tilasm explains.
