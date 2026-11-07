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

**Filters.** Each filter names a ref, compares it and gives a value: equal, one of several values, none of them,
greater or smaller than a number, a day or a duration, a text that contains or begins with another, ignoring case, or
absent. All the filters of a query hold together. An equality on an identifier narrows what the store reads before
anything is decoded.

**Aggregates.** A query may group its rows instead of listing them: by the value of a ref, by the day, the week or the
month of a time, in the machine's time zone, or by bins of a number or a duration whose bounds it gives, such as below
ten seconds, ten seconds to a minute, a minute to ten, and above. For each group it computes measures over a ref:
the count, the sum, the mean, the least, the greatest, the median and the ninetieth percentile. Absent values are left
out of every measure. A period without rows does not show; a bin without rows does, with a count of zero, so that a
histogram keeps its shape. The window asks for the aggregate and draws it as it comes: it computes nothing itself.

**Arrow.** For the window, the table comes in Apache Arrow, typed: text, the name of a status, whole numbers, decimals,
booleans, instants and durations in milliseconds, and lists. Each column carries its ref, its label and its unit. The
same table as text or CSV writes a status by its short name, a time in UTC, and a bin by its range.
