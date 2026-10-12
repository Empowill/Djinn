// The reports of a wish: its blocks of kind report (djinn block put --kind report), what a worker found out, a note
// for the agents like any block (the For agents tab lists it with the others). Nothing counts them for the developer:
// what concerns them comes as a question, whose card links the report it names to open on demand. Djinn computes
// nothing else from them: a report stays smoke.
import { timestampMs } from "@bufbuild/protobuf/wkt";

import type { Block, Question } from "../../gen/ts/plan/v1/plan_pb";

// The kind of the blocks that are reports.
export const REPORT_KIND = "report";

// A title shorter than this is too common a phrase for a question that quotes it to name its report.
const TITLE_MIN = 12;

// isReport tells a block of kind report, case ignored.
export function isReport(block: Pick<Block, "kind">): boolean {
  return (block.kind ?? "").toLowerCase() === REPORT_KIND;
}

const reportLists = new WeakMap<readonly Block[], Block[]>();

// reportsOf are the reports among a wish's blocks, the latest first; the same array for the same blocks.
export function reportsOf(blocks: readonly Block[]): Block[] {
  let found = reportLists.get(blocks);
  if (!found) {
    const at = (b: Block) =>
      b.updateTime
        ? timestampMs(b.updateTime)
        : b.createTime
          ? timestampMs(b.createTime)
          : 0;
    found = blocks
      .filter(isReport)
      .map((b, i) => ({ b, i, at: at(b) }))
      .sort((x, y) => y.at - x.at || y.i - x.i)
      .map((x) => x.b);
    reportLists.set(blocks, found);
  }
  return found;
}

// reportOf is the report a question links: the one whose block id its text, context or recommendation names, or
// else the one whose title it quotes, case ignored. The asker links a report by writing either, with no field for
// it: Djinn computes nothing from the link.
export function reportOf(
  question: Pick<Question, "text" | "context" | "recommendation">,
  reports: readonly Block[],
): Block | undefined {
  if (!reports.length) return undefined;
  const said = [question.text, question.context, question.recommendation]
    .filter(Boolean)
    .join("\n");
  if (!said) return undefined;
  const byId = reports.find((r) => r.id && said.includes(r.id));
  if (byId) return byId;
  const lower = said.toLowerCase();
  return reports.find(
    (r) =>
      (r.title ?? "").trim().length >= TITLE_MIN &&
      lower.includes(r.title.trim().toLowerCase()),
  );
}
