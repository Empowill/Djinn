// One sentence where a region's content will be while Djinn reads it: three motes of smoke rise in turn beside the
// fact, and the Djinn adds a short aside in the theme's voice (solar in light, mocking demon in dark).
import { useEffect, useState } from "react";

import type { ReadState } from "./data/store";
import { EyePerch } from "./djinn-eye";
import { type TextKey, type TextParams, t } from "./i18n";
import { useShownTheme } from "./theme";

export type ReadingSentenceKind =
  | "wishes"
  | "projects"
  | "questions"
  | "questions_slow"
  | "runs"
  | "runs_slow"
  | "runs_failed"
  | "tasks"
  | "tasks_slow"
  | "carpet_tasks"
  | "graph"
  | "tilasms"
  | "decisions"
  | "review"
  | "agents"
  | "machine"
  | "commands"
  | "project"
  | "terminal"
  | "inbox"
  | "failed";

export type ReadingLineSize = "q" | "g" | "sb-r" | "card-r";

interface SentenceDef {
  what: TextKey;
  light?: TextKey;
  dark?: TextKey;
}

const SENTENCES: Record<ReadingSentenceKind, SentenceDef> = {
  wishes: { what: "reading.wishes" },
  projects: { what: "reading.projects" },
  questions: {
    what: "reading.questions",
    light: "reading.questions_light",
    dark: "reading.questions_dark",
  },
  questions_slow: {
    what: "reading.questions_slow",
    light: "reading.questions_slow_light",
    dark: "reading.questions_slow_dark",
  },
  runs: {
    what: "reading.runs",
    light: "reading.runs_light",
    dark: "reading.runs_dark",
  },
  runs_slow: {
    what: "reading.runs_slow",
    light: "reading.runs_slow_light",
    dark: "reading.runs_slow_dark",
  },
  runs_failed: { what: "reading.runs_failed" },
  tasks: {
    what: "reading.tasks",
    light: "reading.tasks_light",
    dark: "reading.tasks_dark",
  },
  tasks_slow: {
    what: "reading.tasks_slow",
    light: "reading.tasks_slow_light",
    dark: "reading.tasks_slow_dark",
  },
  carpet_tasks: {
    what: "reading.carpet_tasks",
    light: "reading.carpet_tasks_light",
    dark: "reading.carpet_tasks_dark",
  },
  graph: {
    what: "reading.graph",
    light: "reading.graph_light",
    dark: "reading.graph_dark",
  },
  tilasms: {
    what: "reading.tilasms",
    light: "reading.tilasms_light",
    dark: "reading.tilasms_dark",
  },
  decisions: {
    what: "reading.decisions",
    light: "reading.decisions_light",
    dark: "reading.decisions_dark",
  },
  review: {
    what: "reading.review",
    light: "reading.review_light",
    dark: "reading.review_dark",
  },
  agents: { what: "reading.agents" },
  machine: {
    what: "reading.machine",
    light: "reading.machine_light",
    dark: "reading.machine_dark",
  },
  commands: { what: "reading.commands" },
  project: { what: "reading.project" },
  terminal: { what: "reading.terminal" },
  inbox: { what: "reading.inbox" },
  failed: { what: "reading.failed" },
};

export function ReadingLine({
  kind,
  state = "reading",
  size,
  params,
  onRetry,
  eyePerch = false,
  hasEye = false,
  themeOverride,
}: {
  kind: ReadingSentenceKind;
  state?: ReadState;
  size?: ReadingLineSize;
  params?: TextParams;
  onRetry?: () => void;
  eyePerch?: boolean;
  hasEye?: boolean;
  themeOverride?: "light" | "dark";
}) {
  const shownTheme = useShownTheme();
  const theme = themeOverride ?? shownTheme;
  const isFailed =
    state === "failed" || kind === "failed" || kind === "runs_failed";
  const wantPerch = eyePerch && state !== "ready" && !isFailed;
  const [perchReady, setPerchReady] = useState(false);
  useEffect(() => {
    if (!wantPerch) {
      setPerchReady(false);
      return;
    }
    const timer = setTimeout(() => setPerchReady(true), 150);
    return () => clearTimeout(timer);
  }, [wantPerch]);

  if (state === "ready") return null;

  const isSlow =
    state === "slow" ||
    kind === "questions_slow" ||
    kind === "runs_slow" ||
    kind === "tasks_slow";

  let effectiveKind: ReadingSentenceKind = kind;
  if (isFailed) {
    effectiveKind =
      kind === "runs" || kind === "runs_slow" || kind === "runs_failed"
        ? "runs_failed"
        : "failed";
  } else if (state === "slow") {
    if (kind === "questions" && params?.wish) effectiveKind = "questions_slow";
    else if (kind === "runs") effectiveKind = "runs_slow";
    else if (kind === "tasks") effectiveKind = "tasks_slow";
  }

  const def = SENTENCES[effectiveKind] ?? SENTENCES.failed;
  const what = t(def.what, params);
  const voiceKey = theme === "light" ? def.light : def.dark;
  const voice = voiceKey ? t(voiceKey, params) : "";

  const cls = [
    "reading",
    size,
    isSlow && !isFailed && "slow",
    isFailed && "failed",
    hasEye && "has-eye",
  ]
    .filter(Boolean)
    .join(" ");

  return (
    <div
      className={cls}
      role={isFailed ? "alert" : "status"}
      aria-live={isFailed ? undefined : "polite"}
      data-eye-perch={eyePerch ? "reading" : undefined}
    >
      {wantPerch && perchReady ? <EyePerch name="reading" /> : null}
      <span className="motes" aria-hidden="true">
        <i />
        <i />
        <i />
      </span>
      <span className="what">
        {what}
        {voice ? <span className="voice">{` ${voice}`}</span> : null}
      </span>
      {isFailed && onRetry ? (
        <button type="button" className="retry" onClick={onRetry}>
          {t("reading.retry")}
        </button>
      ) : null}
    </div>
  );
}
