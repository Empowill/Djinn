# Queries

One route reads every list Djinn keeps as a table: the **query**. It names a collection, the tasks, the questions, the
wishes or the projects, then the columns it wants, the filters its rows must pass, their order, and how many to keep.
The answer is a table whose shape follows the question. The window reads it to draw its charts; you and the agents read
it from the command line, aligned or as CSV, and an agent's client reaches it through MCP too. Every query only reads:
none changes anything, so an agent runs it without asking.

**Refs.** A column is named by a ref: a field by its path in the entity, such as a task's code or the cost of its
usage; across a field that holds another entity's identifier, such as the title of the wish a task belongs to; or a
value Djinn computes, such as the size of a prompt in tokens, the time a question waited for its answer, or the time
from a task's end to its commit into the wish's integration branch. Long texts, a prompt, a context, a worker's last
line, are never columns, nor filters: a list of thousands of rows would weigh megabytes. The schema of a collection
lists its refs, each with its type, a short label, the unit of its numbers (dollars, tokens), what it says, and the
collection it leads to.

**Types, never guessed.** A column's type comes from the definition of the entity, not from the values its rows happen
to hold: a duration stays a duration even when the first row has none. An absent value stays absent, never a zero: a
task without usage has no cost, which is not a cost of nothing.

**Times and types of tasks.** The store does not keep how long a task worked: a query computes it from the task's
runs and gate waits, as the task list does, as of the moment it runs. A task reads its time running, pauses and gates
excluded, its time waiting for a gate, its time paused or interrupted between its first start and its end, and its
wait before its first start. A task that never started has none of them but that wait. An azima's times sum its
parts', so leave the azimas out when you add up work. Every task has one kind, its type: work, correction, review,
investigation, mockup, ifrit, watcher or azima. Djinn alone makes a correction, a review or a watcher; a task stored
before these were kinds reads its own from what Djinn started it for. Measure, bin and group by these as by any ref: the
median time running of the work done this week, the tasks created each day by type.

**Filters.** Each filter names a ref, compares it and gives a value, and its kind follows the type of the ref: a text
or a status equal to a value, one of several, containing or beginning with another, ignoring case; a number, a time or
a duration equal to, greater or smaller than a value; true or false; or absent. Filters join in groups, all of them,
one of them, or none. All the filters of a query hold together. An equality on an identifier narrows what the store
reads before anything is decoded. The window sends each filter typed; the command line writes it as a short text, which it
reads itself into the typed filter, with the type the schema gives its ref.

**Aggregates.** A query may group its rows instead of listing them: by the value of a ref, by the day, the week or the
month of a time, in the machine's time zone, or by bins of a number or a duration whose bounds it gives, such as below
ten seconds, ten seconds to a minute, a minute to ten, and above. For each group it computes measures over a ref:
the count, the sum, the mean, the least, the greatest, the median and the ninetieth percentile. Absent values are left
out of every measure. A period without rows does not show; a bin without rows does, with a count of zero, so that a
histogram keeps its shape. The window asks for the aggregate and draws it as it comes: it computes nothing itself.

**Arrow.** The table comes in Apache Arrow unless a query asks otherwise: the window reads it so, typed: text, the name of a status, whole numbers, decimals,
booleans, instants and durations in milliseconds, and lists. Each column carries its ref, its label and its unit. The
command line and an agent's client ask the same table as text, or as CSV, which write a status by its short name, a
time in UTC, and a bin by its range.
