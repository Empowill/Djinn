# Decision

A decision is a choice made for a wish and kept with it: a question you answered, or a decision the Djinn wrote down,
often one you told it in words. Each one stays with when it was taken, why, by whom, and the tasks it led to. Together
they make the wish's decision log.

## Why decisions are kept

A plan changes for reasons, and the reasons are the first thing lost. A week later, nobody remembers why the export
renders in the background, or why the old endpoint was kept. The decision log keeps them:

- **For you.** What was decided, in the order it was decided, with the note you gave.
- **For the Djinn.** Every new session of the Djinn starts from the wish's brief, which holds its latest decisions:
  it does not ask again what was settled, nor undo it by mistake.
- **For the work.** A task that comes from a decision points to it, and the decision lists the tasks it led to: from
  a line of code back to the choice that asked for it.

## Where decisions come from

| Source | How it becomes a decision |
| --- | --- |
| A question you answered | Rubbing the lamp turns it into a decision, with the option you chose and your note. |
| A question approved | Approving an open question, from a terminal, answers it with the option it recommends. |
| A question the Djinn answered | The Djinn may settle a question itself; it is a decision all the same. |
| Something you told the Djinn | When you decide something by writing to the Djinn, it writes the decision down for the wish. |
| A decision a worker wrote | A worker may write one down about its own task. |

A withdrawn question is no decision: it was closed without an answer. A worker never answers a question; it asks.

> **Example.** In the Djinn's terminal you write: "Keep the old export endpoint until the mobile app moves." The Djinn
> writes that decision down for the wish, and spawns the task that keeps the endpoint, from that decision. The
> Decisions tab shows it first, by the Djinn, and the task it led to.

## The decision log

The wish's **Decisions** tab lists every decision, the latest first, with their count. Each row shows:

1. **Its icon**, the subject in one emoji, then **who**. An answered question shows **Decided by you**, or
   **Approved by you** once approved; a decision written down shows **By the Djinn**, or **By** the code of the task
   whose worker wrote it.
2. **The question's code** and when it was decided.
3. **The question**, or the decision's title.
4. **The answer**: yes or no, or the option's letter and text.
5. **The note**: your note, or the decision written down, folded under its first line when it is long.
6. **Led to**, followed by the codes of the tasks it led to: a click opens the task in the Tasks tab.

The log is read only: no button changes it. The thirty latest show first, and a line at the end shows the older ones.
The flying carpet holds the log of every active wish at once, each row naming its wish.

On a task's card, **From** names the decision it comes from; a click shows it in the Decisions tab.

## Deciding, and changing your mind

- **Answer** a question in the wish: see [Question and move](#question-and-move). The Djinn is told at once, and turns
  your answer into work. If it is not running, the answer waits for it, and the row says so.
- **Tell the Djinn** a decision in words, with **Write to the Djinn** or in its terminal. It answers in the window, and
  writes the decision down for the wish.
- **Change your mind** by telling the Djinn. An answered question is closed in the window: its card shows the answer,
  and no longer takes one. Answered again from a terminal with another option, the answer is replaced and the Djinn
  is told what changed, from what to what; a new note alone reaches it once; the same answer changes nothing.
- **Approve** a decision the Djinn took, from a terminal, and the Djinn is told it is yours now. The window has no
  approve button.

When a question offers options that each plan a task, your answer launches that task: at once when you add no note
and the task comes after nothing, otherwise once the Djinn has read your answer. Its card then comes from your
decision.

## Decisions from the Djinn

The Djinn reads the latest decisions in the wish's brief, each with who took it, its note and the tasks it led to,
and it is told each time you answer. Ask it in words:

- "Why did we keep the old endpoint?" It finds the decision and its note.
- "Write down that we drop Safari 15." It writes a decision for the wish.
- "Spawn the migration from Q12." The new task comes from that decision, and both point to each other. A task comes
  only from a question already answered, or from a decision of the same wish.

## Good to know

- **Put the why in the note.** The option says what; your note says why, and it is what the log keeps for later.
- **Decide in the window, or say it to the Djinn.** A choice made in a chat outside Djinn is not in the log; tell it
  to the Djinn and it is.
- **Withdraw is not no.** A withdrawn question leaves no decision; answer no when the answer is no.
- **The log is the history.** To change a decision, answer the question again or decide anew: the log keeps both.

## Read next

- [Question and move](#question-and-move): how a question is asked and answered.
- [Block](#block): what the Djinn writes down for the wish, decisions included.
- [Task and worker](#task-and-worker): the work a decision leads to.
