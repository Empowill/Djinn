// User indications are durable lamp data, apart from the lead's free-form notes and transient reports.
import {
  InstructionStatus,
  type Instruction,
  type Task,
} from "../gen/ts/plan/v1/plan_pb";
import { useEffect, useRef, useState } from "react";
import { date, when } from "./data/format";
import { t } from "./i18n";
import { SettingsDrawer } from "./wish-settings";
import "./instruction.css";

export function InstructionList({
  instructions,
  tasks,
  onTask,
  workspace = false,
  compact = false,
  visible = true,
}: {
  instructions: Instruction[];
  tasks: Task[];
  // Open the task's own view when the task is rendered outside this list.
  onTask?: (taskId: string) => void;
  workspace?: boolean;
  compact?: boolean;
  visible?: boolean;
}) {
  const workers = new Map(tasks.map((task) => [task.id, task]));
  const [detail, setDetail] = useState<Instruction>();
  const [closeRequest, setCloseRequest] = useState(0);
  const pendingTask = useRef<(() => void) | null>(null);
  const openTask = (taskId: string) => {
    if (!onTask) return;
    if (!workspace) {
      onTask(taskId);
      return;
    }
    // The compact row's worker link is usable before its detail drawer exists. Defer only while a drawer is mounted.
    if (!detail) {
      onTask(taskId);
      return;
    }
    pendingTask.current = () => onTask(taskId);
    setCloseRequest((request) => request + 1);
  };
  const statusText = (instruction: Instruction) =>
    instruction.status === InstructionStatus.DONE
      ? t("instruction.done")
      : instruction.status === InstructionStatus.REFLECTING
        ? t("instruction.reflecting")
        : instruction.status === InstructionStatus.PROCESSING
          ? t("instruction.processing")
          : t("instruction.pending");
  useEffect(() => {
    if (workspace && !visible) setDetail(undefined);
  }, [visible, workspace]);
  if (!instructions.length) return null;
  return (
    <section
      className={`wish-section wish-instructions${workspace ? " is-workspace" : ""}${compact ? " is-compact" : ""}`}
      aria-label={t("instruction.title")}
    >
      <div className="section-title">
        <h2>
          {t("instruction.title")}
          <span className="count">{instructions.length}</span>
        </h2>
      </div>
      <ol className="instruction-list">
        {instructions.map((instruction) => {
          const worker = workers.get(instruction.taskId);
          const replies = instruction.replies ?? [];
          return (
            <li
              className="instruction-entry"
              key={instruction.id}
              id={`instruction-${instruction.id}`}
              role={workspace ? "button" : undefined}
              tabIndex={workspace ? 0 : undefined}
              aria-label={workspace ? t("page.details") : undefined}
              onClick={workspace ? () => setDetail(instruction) : undefined}
              onKeyDown={
                workspace
                  ? (event) => {
                      if (event.target !== event.currentTarget) return;
                      if (event.key !== "Enter" && event.key !== " ") return;
                      event.preventDefault();
                      setDetail(instruction);
                    }
                  : undefined
              }
            >
              <header>
                <code>{instruction.code}</code>
                <span className="instruction-state">
                  <span>{statusText(instruction)}</span>
                  {instruction.taskId && (
                    <>
                      {" "}
                      {worker ? (
                        <a
                          className="instruction-worker"
                          href={`#task-${worker.id}`}
                          onClick={(event) => {
                            event.stopPropagation();
                            if (onTask) {
                              event.preventDefault();
                              openTask(worker.id);
                              return;
                            }
                            const target = document.getElementById(
                              `task-${worker.id}`,
                            );
                            if (!target) return;
                            event.preventDefault();
                            target.scrollIntoView({ block: "nearest" });
                            target
                              .querySelector<HTMLButtonElement>(
                                ".wish-task-heading",
                              )
                              ?.focus({ preventScroll: true });
                          }}
                        >
                          {worker.code}
                        </a>
                      ) : (
                        <span>
                          {t("instruction.worker_unavailable", {
                            task: instruction.taskId,
                          })}
                        </span>
                      )}
                    </>
                  )}
                </span>
                {instruction.createTime && (
                  <time>{when(instruction.createTime)}</time>
                )}
              </header>
              <p className={workspace ? "instruction-preview" : undefined}>
                {instruction.text}
              </p>
              {workspace && replies.length > 0 ? (
                <span className="instruction-reply-count">
                  {t("instruction.replies")} · {replies.length}
                </span>
              ) : replies.length > 0 ? (
                <ol
                  className="instruction-replies"
                  aria-label={t("instruction.replies")}
                >
                  {replies.map((reply, index) => (
                    <li
                      className="instruction-reply"
                      key={`${instruction.id}-reply-${index}`}
                    >
                      <header>
                        <span className="instruction-reply-label">
                          {t("instruction.reply")}
                        </span>
                        {reply.createTime && (
                          <time
                            dateTime={date(reply.createTime)?.toISOString()}
                          >
                            {when(reply.createTime)}
                          </time>
                        )}
                      </header>
                      <p>{reply.text}</p>
                    </li>
                  ))}
                </ol>
              ) : null}
            </li>
          );
        })}
      </ol>
      {detail && workspace && visible && (
        <SettingsDrawer
          title={detail.code || t("instruction.title")}
          closeRequest={closeRequest}
          onClose={() => setDetail(undefined)}
          onClosed={() => {
            const action = pendingTask.current;
            pendingTask.current = null;
            if (!action) return;
            action();
            return false;
          }}
        >
          <InstructionDetail
            instruction={detail}
            worker={workers.get(detail.taskId)}
            onTask={openTask}
          />
        </SettingsDrawer>
      )}
    </section>
  );
}

function InstructionDetail({
  instruction,
  worker,
  onTask,
}: {
  instruction: Instruction;
  worker?: Task;
  onTask: (taskId: string) => void;
}) {
  const replies = instruction.replies ?? [];
  return (
    <div className="workspace-activity-detail instruction-detail">
      <header>
        <code>{instruction.code}</code>
        <span className="instruction-state">
          {instruction.status === InstructionStatus.DONE
            ? t("instruction.done")
            : instruction.status === InstructionStatus.REFLECTING
              ? t("instruction.reflecting")
              : instruction.status === InstructionStatus.PROCESSING
                ? t("instruction.processing")
                : t("instruction.pending")}
        </span>
        {worker ? (
          <a
            className="instruction-worker"
            href={`#task-${worker.id}`}
            onClick={(event) => {
              event.preventDefault();
              onTask(worker.id);
            }}
          >
            {worker.code}
          </a>
        ) : instruction.taskId ? (
          <span>
            {t("instruction.worker_unavailable", {
              task: instruction.taskId,
            })}
          </span>
        ) : null}
        {instruction.createTime && <time>{when(instruction.createTime)}</time>}
      </header>
      <p>{instruction.text}</p>
      {replies.length > 0 && (
        <ol
          className="instruction-replies"
          aria-label={t("instruction.replies")}
        >
          {replies.map((reply, index) => (
            <li
              className="instruction-reply"
              key={`${instruction.id}-detail-reply-${index}`}
            >
              <header>
                <span className="instruction-reply-label">
                  {t("instruction.reply")}
                </span>
                {reply.createTime && (
                  <time dateTime={date(reply.createTime)?.toISOString()}>
                    {when(reply.createTime)}
                  </time>
                )}
              </header>
              <p>{reply.text}</p>
            </li>
          ))}
        </ol>
      )}
    </div>
  );
}
