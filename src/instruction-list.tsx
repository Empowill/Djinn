// User indications are durable lamp data, apart from the lead's free-form notes and transient reports.
import {
  InstructionStatus,
  type Instruction,
  type Task,
} from "../gen/ts/plan/v1/plan_pb";
import { when } from "./data/format";
import { t } from "./i18n";
import "./instruction.css";

export function InstructionList({
  instructions,
  tasks,
}: {
  instructions: Instruction[];
  tasks: Task[];
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
            </li>
          );
        })}
      </ol>
    </section>
  );
}
