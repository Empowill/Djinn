// The For agents tab of a wish: the blocks its lead and workers wrote for one another (a hand-off, a reference, a
// report), every kind but the decisions, which have their own tab, and the logs, which the journal shows. Nothing in
// it waits for the developer: whatever does is a question. Each block is folded under its title; opened, Markdown is
// rendered, another media type shown as it is. No mark: a block is not read nor approved.
import { ChevronRight } from "lucide-react";
import { useState } from "react";

import { type Block } from "../gen/ts/plan/v1/plan_pb";
import { isDecisionBlock } from "./data/decisions";
import { when } from "./data/format";
import { isLog } from "./data/journal";
import { t } from "./i18n";
import { MarkdownBody } from "./markdown-body";

// agentBlocks are the blocks of the For agents tab: neither a decision nor a log.
export function agentBlocks(blocks: readonly Block[]): Block[] {
  return blocks.filter((b) => !isLog(b) && !isDecisionBlock(b));
}

export function AgentBlocks({
  blocks,
  codes,
}: {
  blocks: readonly Block[];
  // The code of each task, by its id: the task a block is about.
  codes: ReadonlyMap<string, string>;
}) {
  return (
    <section
      className="wish-section agent-blocks"
      role="tabpanel"
      aria-labelledby="view-tab-agents"
    >
      <p className="muted-text">{t("agents.detail")}</p>
      {blocks.length === 0 && <p className="muted-text">{t("agents.none")}</p>}
      {blocks.map((block) => (
        <AgentBlock
          key={block.id}
          block={block}
          task={codes.get(block.taskId) ?? ""}
        />
      ))}
    </section>
  );
}

function AgentBlock({ block, task }: { block: Block; task: string }) {
  const [open, setOpen] = useState(false);
  const markdown = !block.mediaType || block.mediaType === "text/markdown";
  return (
    <article className="wish-block agent-block" id={`block-${block.id}`}>
      <button
        className="fold-heading"
        onClick={() => setOpen(!open)}
        aria-expanded={open}
      >
        <ChevronRight size={14} className={open ? "rotated-90" : ""} />
        <h3>{block.title || t("agents.untitled")}</h3>
        <span className="eyebrow">
          {block.kind}
          {task && ` · ${t("page.about_task", { task })}`}
          {block.updateTime && ` · ${when(block.updateTime)}`}
        </span>
      </button>
      {open && (
        <div className="wish-block-body prose">
          {markdown ? (
            <MarkdownBody text={block.content} />
          ) : (
            <pre className="wish-block-raw">{block.content}</pre>
          )}
        </div>
      )}
    </article>
  );
}
