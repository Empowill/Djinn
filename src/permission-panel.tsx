import { useState } from "react";
import {
  Check,
  ChevronDown,
  CircleAlert,
  Clock3,
  ShieldCheck,
  X,
} from "lucide-react";
import type { PermissionDecision, PermissionRequest } from "./types";
import "./permission-panel.css";
import { t } from "./i18n";

type Answers = Record<string, string>;

function providerLabel(provider: PermissionRequest["provider"]) {
  return provider === "codex" ? "Codex" : "Claude Code";
}

function PermissionQuestion({
  requestId,
  question,
  value,
  onChange,
  disabled,
}: {
  requestId: string;
  question: NonNullable<PermissionRequest["questions"]>[number];
  value: string;
  onChange: (value: string) => void;
  disabled: boolean;
}) {
  return (
    <fieldset className="permission-question" disabled={disabled}>
      <legend>
        {question.question}
        {question.optional ? t("permissions.optional_suffix") : ""}
      </legend>
      {question.options?.length ? (
        <div className="permission-options">
          {question.options.map((option) => (
            <label className="permission-option" key={option.label}>
              <input
                type="radio"
                name={`permission-${requestId}-${question.id}`}
                value={option.label}
                checked={value === option.label}
                onChange={() => onChange(option.label)}
              />
              <span>
                <strong>{option.label}</strong>
                {option.description && <small>{option.description}</small>}
              </span>
            </label>
          ))}
        </div>
      ) : (
        <input
          value={value}
          onChange={(event) => onChange(event.target.value)}
          placeholder={t("permissions.answer_placeholder")}
          aria-label={question.question}
        />
      )}
    </fieldset>
  );
}

function PermissionCard({
  request,
  onRespond,
  externallyBusy = false,
}: {
  request: PermissionRequest;
  onRespond: (
    request: PermissionRequest,
    decision: PermissionDecision,
    answers?: Answers,
  ) => Promise<boolean>;
  externallyBusy?: boolean;
}) {
  const [detailsOpen, setDetailsOpen] = useState(false);
  const [answers, setAnswers] = useState<Answers>({});
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const pending = request.status === "pending";
  const isBusy = busy || externallyBusy;
  const disabled = !pending || isBusy;
  const missingAnswer = (request.questions || []).some(
    (question) => !question.optional && !answers[question.id]?.trim(),
  );
  const resolve = async (decision: PermissionDecision) => {
    if (disabled || (decision !== "decline" && missingAnswer)) return;
    setBusy(true);
    setError("");
    try {
      const resolved = await onRespond(
        request,
        decision,
        Object.keys(answers).length ? answers : undefined,
      );
      if (!resolved) setError(t("permissions.unresolved"));
    } catch (cause) {
      setError(
        cause instanceof Error ? cause.message : t("permissions.answer_failed"),
      );
    } finally {
      setBusy(false);
    }
  };
  const statusText =
    request.status === "accepted"
      ? t("permissions.status_accepted")
      : request.status === "declined"
        ? t("permissions.status_declined")
        : request.status === "cancelled"
          ? t("permissions.status_cancelled")
          : t("permissions.status_pending");
  return (
    <article
      id={`permission-${request.id}`}
      className={`permission-card is-${request.status}`}
      aria-busy={isBusy}
    >
      <div className="permission-card-top">
        <span className="permission-icon" aria-hidden="true">
          {request.status === "pending" ? (
            <ShieldCheck size={16} />
          ) : request.status === "accepted" ? (
            <Check size={16} />
          ) : request.status === "cancelled" ? (
            <Clock3 size={16} />
          ) : (
            <X size={16} />
          )}
        </span>
        <div className="permission-card-heading">
          <div className="permission-card-meta">
            <span>{request.agentName || request.agentId}</span>
            <span>·</span>
            <span>{providerLabel(request.provider)}</span>
            <span className={`permission-status is-${request.status}`}>
              {isBusy ? t("permissions.sending") : statusText}
            </span>
          </div>
          <h3>{request.title}</h3>
          {request.reason && <p>{request.reason}</p>}
          {(request.command || request.cwd) && (
            <div className="permission-request-preview">
              {request.command && <code>{request.command}</code>}
              {request.cwd && <span>{request.cwd}</span>}
            </div>
          )}
        </div>
      </div>
      <details
        className="permission-details"
        open={detailsOpen}
        onToggle={(event) =>
          setDetailsOpen((event.currentTarget as HTMLDetailsElement).open)
        }
      >
        <summary>
          {t("permissions.show_details")}
          <ChevronDown size={13} />
        </summary>
        <div className="permission-detail-body">
          {request.method && (
            <div>
              <span>{t("permissions.method")}</span>
              <code>{request.method}</code>
            </div>
          )}
          {request.command && (
            <div>
              <span>{t("permissions.command")}</span>
              <code>{request.command}</code>
            </div>
          )}
          {request.cwd && (
            <div>
              <span>{t("permissions.folder")}</span>
              <code>{request.cwd}</code>
            </div>
          )}
          {!!request.paths?.length && (
            <div>
              <span>{t("permissions.paths")}</span>
              <code>{request.paths.join("\n")}</code>
            </div>
          )}
        </div>
      </details>
      {pending && request.questions?.length ? (
        <div className="permission-questions">
          {request.questions.map((question) => (
            <PermissionQuestion
              key={question.id}
              requestId={request.id}
              question={question}
              value={answers[question.id] || ""}
              onChange={(value) =>
                setAnswers((current) => ({ ...current, [question.id]: value }))
              }
              disabled={isBusy}
            />
          ))}
        </div>
      ) : null}
      {error && (
        <p className="permission-error" role="alert">
          <CircleAlert size={14} />
          {error}
        </p>
      )}
      {pending && (
        <div className="permission-actions">
          <button
            type="button"
            className="button secondary small"
            disabled={disabled}
            onClick={() => void resolve("decline")}
          >
            {t("permissions.decline")}
          </button>
          <button
            type="button"
            className="button secondary small"
            disabled={disabled || missingAnswer}
            onClick={() => void resolve("accept")}
          >
            {t("permissions.accept_once")}
          </button>
          {request.canAcceptForSession && (
            <button
              type="button"
              className="button accent small"
              disabled={disabled || missingAnswer}
              onClick={() => void resolve("acceptForSession")}
            >
              {t("permissions.accept_for_session")}
            </button>
          )}
        </div>
      )}
    </article>
  );
}

export function PermissionPanel({
  requests,
  onRespond,
  busyIds = [],
}: {
  requests: PermissionRequest[];
  onRespond: (
    request: PermissionRequest,
    decision: PermissionDecision,
    answers?: Answers,
  ) => Promise<boolean>;
  busyIds?: string[];
}) {
  const pending = requests.filter((request) => request.status === "pending");
  const resolved = requests
    .filter((request) => request.status !== "pending")
    .slice()
    .sort(
      (a, b) =>
        Date.parse(b.updatedAt || b.createdAt) -
        Date.parse(a.updatedAt || a.createdAt),
    );
  const recent = pending.length ? [] : resolved.slice(0, 1);
  const history = resolved.slice(recent.length);
  const [historyOpen, setHistoryOpen] = useState(false);
  // Resolved requests remain available to the session history, but they do
  // not keep an action section alive once the agent has no pending request.
  if (!pending.length) return null;
  return (
    <section
      className="permission-panel"
      aria-label={t("permissions.panel_label")}
    >
      <div className="permission-panel-heading">
        <div>
          <h3>
            {t("permissions.title")}
            <span className="count">{pending.length}</span>
          </h3>
          <p>{t("permissions.check_before_deciding")}</p>
        </div>
        <ShieldCheck size={17} aria-hidden="true" />
      </div>
      <div className="permission-list">
        {[...pending, ...recent].map((request) => (
          <PermissionCard
            key={request.id}
            request={request}
            onRespond={onRespond}
            externallyBusy={busyIds.includes(request.id)}
          />
        ))}
      </div>
      {history.length > 0 && (
        <details onToggle={(event) => setHistoryOpen(event.currentTarget.open)}>
          <summary>
            {t("permissions.history", { count: history.length })}
          </summary>
          {historyOpen &&
            history.map((request) => (
              <PermissionCard
                key={request.id}
                request={request}
                onRespond={onRespond}
                externallyBusy={false}
              />
            ))}
        </details>
      )}
    </section>
  );
}

export default PermissionPanel;
