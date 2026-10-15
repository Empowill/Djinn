// The choice the lead's agent shows in its terminal, waiting for the developer (an approval of a command, an edit,
// the network, a folder to trust), as a card of the wish: an option clicked goes to the terminal as its keys would
// (WishService.Choose). Nothing to do in the terminal.
import { TerminalSquare } from "lucide-react";
import { motion } from "motion/react";
import { type ReactNode, useState } from "react";

import type { LeadPrompt } from "../gen/ts/plan/v1/plan_pb";
import { t } from "./i18n";
import { StatusBadge } from "./status";

export function LeadPromptCard({
  wishId,
  domId,
  prompt,
  onChoose,
  origin,
  compact = false,
  dockMode = false,
}: {
  wishId: string;
  // A content-aware id avoids duplicate DOM targets while a revised permission transitions in the dock.
  domId?: string;
  origin?: ReactNode;
  prompt: LeadPrompt;
  // Compact cards keep terminal context under an inline details disclosure in the attention lane.
  compact?: boolean;
  // Dock cards keep permission choices above the bounded terminal request details.
  dockMode?: boolean;
  // Picks option index, from 0; resolves once the terminal has it. A rejection (the choice changed) keeps the card.
  onChoose: (index: number) => Promise<void>;
}) {
  const [sending, setSending] = useState(false);
  const choose = async (index: number) => {
    setSending(true);
    try {
      await onChoose(index);
    } catch {
      // The toast says why; the card follows the terminal.
    } finally {
      setSending(false);
    }
  };
  const options = (
    <div
      className="question-options"
      role="group"
      aria-label={t("page.options")}
    >
      {prompt.options.map((option, index) => (
        <button
          key={index}
          className="option"
          disabled={sending}
          onClick={() => void choose(index)}
        >
          <strong className="option-letter">{index + 1}</strong>
          <p>{option}</p>
        </button>
      ))}
    </div>
  );
  return (
    <motion.article
      id={domId ?? `lead-prompt-${wishId}`}
      className={`question-card open is-blocking lead-prompt${compact ? " compact" : ""}`}
      initial={{ opacity: 0, y: 10 }}
      animate={{ opacity: 1, y: 0 }}
      exit={{ opacity: 0, x: 30 }}
      transition={{ duration: 0.25 }}
    >
      <div className="question-top">
        <div className="question-heading">
          <span className="question-id">
            <TerminalSquare size={15} aria-label={t("lead_prompt.terminal")} />
          </span>
          <div className="question-title">
            <span className="question-meta">
              {origin}
              <StatusBadge tone="failed" label={t("lead_prompt.badge")} />
            </span>
            {!dockMode && <h3>{prompt.title || t("lead_prompt.title")}</h3>}
          </div>
        </div>
      </div>
      {dockMode && <div className="dock-prompt-actions">{options}</div>}
      <div className={`question-inner${dockMode ? " dock-prompt-body" : ""}`}>
        {prompt.lines.length > 0 &&
          (dockMode ? (
            <pre className="lead-prompt-lines dock-prompt-lines">
              {prompt.lines.join("\n")}
            </pre>
          ) : compact ? (
            <details className="question-compact-details">
              <summary>{t("page.details")}</summary>
              <pre className="lead-prompt-lines">{prompt.lines.join("\n")}</pre>
            </details>
          ) : (
            <pre className="lead-prompt-lines">{prompt.lines.join("\n")}</pre>
          ))}
        {!dockMode && options}
        {!dockMode && <p className="form-tip">{t("lead_prompt.detail")}</p>}
      </div>
    </motion.article>
  );
}
