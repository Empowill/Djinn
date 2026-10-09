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
  prompt,
  onChoose,
  origin,
}: {
  wishId: string;
  origin?: ReactNode;
  prompt: LeadPrompt;
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
  return (
    <motion.article
      id={`lead-prompt-${wishId}`}
      className="question-card open is-blocking lead-prompt"
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
            <h3>{prompt.title || t("lead_prompt.title")}</h3>
          </div>
        </div>
      </div>
      <div className="question-inner">
        {prompt.lines.length > 0 && (
          <pre className="lead-prompt-lines">{prompt.lines.join("\n")}</pre>
        )}
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
        <p className="form-tip">{t("lead_prompt.detail")}</p>
      </div>
    </motion.article>
  );
}
