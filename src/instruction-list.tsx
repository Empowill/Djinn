// User indications are durable lamp data, apart from the lead's free-form notes and transient reports.
import {
  InstructionStatus,
  type Instruction,
  type Task,
} from "../gen/ts/plan/v1/plan_pb";
import { date, when } from "./data/format";
import { t } from "./i18n";
import "./instruction.css";

export function InstructionList({
  instructions,
  tasks,
  onTask,
}: {
  instructions: Instruction[];
  tasks: Task[];
  // Open the task's own view when the task is rendered outside this list.
  onTask?: (taskId: string) => void;
}) {
  if (!instructions.length) return null;
  const workers = new Map(tasks.map((task) => [task.id, task]));
  return (
    <section
      className="wish-section wish-instructions"
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
            >
              <header>
                <code>{instruction.code}</code>
                <span className="instruction-state">
                  <span>
                    {instruction.status === InstructionStatus.DONE
                      ? t("instruction.done")
                      : instruction.status === InstructionStatus.REFLECTING
                        ? t("instruction.reflecting")
                        : instruction.status === InstructionStatus.PROCESSING
                          ? t("instruction.processing")
                          : t("instruction.pending")}
                  </span>
                  {instruction.taskId && (
                    <>
                      {" "}
                      {worker ? (
                        <a
                          className="instruction-worker"
                          href={`#task-${worker.id}`}
                          onClick={(event) => {
                            if (onTask) {
                              event.preventDefault();
                              onTask(worker.id);
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
              <p>{instruction.text}</p>
              {replies.length > 0 && (
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
              )}
            </li>
          );
        })}
      </ol>
    </section>
  );
}
