// The screen of one wish, read from the services: what waits for you first (its open questions, and "My wish is
// granted" once Djinn proposes it), then its tasks and their events, its decisions, its blocks and the rights its
// workers have. Djinn proposes; only the user grants.
import {
  CheckCircle2,
  ChevronDown,
  ChevronRight,
  Clock3,
  Download,
  FileText,
  GitBranch,
  Pause,
  Play,
  Sparkles,
  Terminal,
} from "lucide-react";
import { AnimatePresence, motion } from "motion/react";
import { useRef, useState } from "react";

import {
  Allowance,
  type Block,
  Change,
  type Choice,
  type Project,
  TaskStatus,
  type Wish,
  WishState,
} from "../gen/ts/plan/v1/plan_pb";
import { message } from "./data/client";
import { useClients, useData, useStore, useWishDetail } from "./data/djinn";
import {
  allowanceOf,
  isActive,
  isOpen,
  projectsOf,
  when,
  wishStateText,
} from "./data/format";
import { t } from "./i18n";
import { MarkdownBody } from "./markdown-body";
import { useKeepPlace } from "./scroll-anchor";
import { Machine } from "./visuals";
import { WishQuestion } from "./wish-question";
import { WishTask } from "./wish-task";

export function WishView({
  wish,
  onToast,
}: {
  wish: Wish;
  onToast: (text: string) => void;
}) {
  const clients = useClients();
  const store = useStore();
  const allProjects = useData((s) => s.projects);
  const detail = useWishDetail(wish.id);
  const projects = projectsOf(wish, allProjects);
  const open = detail.questions.filter(isOpen);
  const decided = detail.questions.filter((q) => !isOpen(q));
  const running = detail.tasks.some(
    (task) => task.status === TaskStatus.RUNNING,
  );
  const granted = wish.state === WishState.GRANTED;
  const [history, setHistory] = useState(false);
  // The page keeps your place when something above what you read changes (src/scroll-anchor.ts).
  const scrollRef = useRef<HTMLDivElement>(null);
  const keepPlace = useKeepPlace(scrollRef);

  // act runs a write; djinn's answer or its refusal shows as a toast, and what changed is read again.
  const act = async (
    run: () => Promise<unknown>,
    changes: Change[],
    done?: string,
  ) => {
    try {
      await run();
      if (done) onToast(done);
    } catch (error) {
      onToast(message(error));
      throw error;
    } finally {
      void store.changed(wish.id, changes);
    }
  };
  const quiet = (promise: Promise<unknown>) => void promise.catch(() => {});

  const answer = (questionId: string, choice: Choice, note: string) =>
    act(
      () =>
        clients.questions.answer({
          question: { ref: { case: "id", value: questionId } },
          choice,
          note,
          wishId: wish.id,
        }),
      [Change.QUESTION, Change.WISH],
    );

  return (
    <div className="wish-view">
      <header className="topbar">
        <div className="breadcrumbs">
          {projects.map((project) => (
            <span
              className="header-project"
              key={project.id}
              title={project.directory || t("project.no_folder")}
            >
              {project.name}
              <ChevronRight size={12} aria-hidden="true" />
            </span>
          ))}
          <strong>{wish.title}</strong>
        </div>
        <div className="topbar-actions">
          <button
            className="button secondary small"
            title={t("wish.resume_detail")}
            onClick={() =>
              quiet(
                act(
                  () => clients.wishes.resume({ wishId: wish.id }),
                  [Change.WISH],
                ),
              )
            }
          >
            <Terminal size={14} />
            <span>{t("wish.resume")}</span>
          </button>
          <button
            className="button secondary small"
            title={t("wish.page_detail")}
            onClick={() =>
              quiet(
                (async () => {
                  const res = await clients.wishes.render({ wishId: wish.id });
                  onToast(t("wish.page_written", { file: res.file }));
                })().catch((error) => onToast(message(error))),
              )
            }
          >
            <FileText size={14} />
            <span>{t("wish.page")}</span>
          </button>
          <button
            className="button secondary small"
            onClick={() =>
              quiet(
                (async () => {
                  const res = await clients.wishes.export({ wishId: wish.id });
                  onToast(t("wish.exported", { file: res.file }));
                })().catch((error) => onToast(message(error))),
              )
            }
          >
            <Download size={14} />
            <span>{t("app.share")}</span>
          </button>
        </div>
      </header>
      <div className="mission-scroll" ref={keepPlace}>
        <div className="hero mission-header">
          <div className="hero-copy">
            <h1>{wish.title}</h1>
            <div className="hero-meta">
              <span className={`badge ${granted ? "" : "muted"}`}>
                {wishStateText(wish)}
              </span>
              {projects.map((project) => (
                <span key={project.id} title={project.directory}>
                  <GitBranch size={13} />
                  {project.name}
                </span>
              ))}
              <span>
                <Clock3 size={13} />
                {when(wish.createTime)}
              </span>
              {granted ? (
                <button
                  className="text-button"
                  onClick={() =>
                    quiet(
                      act(
                        () => clients.wishes.activate({ wishId: wish.id }),
                        [Change.WISH],
                      ),
                    )
                  }
                >
                  <Play size={13} />
                  {t("wish.reopen")}
                </button>
              ) : isActive(wish) ? (
                <button
                  className="text-button"
                  onClick={() =>
                    quiet(
                      act(
                        () => clients.wishes.pause({ wishId: wish.id }),
                        [Change.WISH],
                      ),
                    )
                  }
                >
                  <Pause size={13} />
                  {t("wish.pause")}
                </button>
              ) : (
                <button
                  className="text-button"
                  onClick={() =>
                    quiet(
                      act(
                        () => clients.wishes.activate({ wishId: wish.id }),
                        [Change.WISH],
                      ),
                    )
                  }
                >
                  <Play size={13} />
                  {t("wish.activate")}
                </button>
              )}
            </div>
          </div>
          <div className="hero-visual">
            <Machine active={running} />
          </div>
        </div>

        <div className="overview-content">
          {(open.length > 0 || wish.ready) && (
            <section
              className="action-center"
              id="action-center"
              aria-label={t("panels.your_move")}
            >
              <div className="action-center-heading">
                <div>
                  <span className="eyebrow">{t("panels.next_action")}</span>
                  <h2>{t("panels.your_move")}</h2>
                  <p>
                    {open.length
                      ? t("wish.questions_wait", { count: open.length })
                      : t("wish.ready_detail")}
                  </p>
                </div>
              </div>
              {open.length > 0 && (
                <section className="decisions-section">
                  <div className="section-heading">
                    <h3>
                      {t("panels.decisions")}
                      <span className="count">{open.length}</span>
                    </h3>
                  </div>
                  <AnimatePresence mode="popLayout">
                    {open.map((q) => (
                      <WishQuestion
                        key={q.id}
                        question={q}
                        expanded={open.length === 1}
                        onAnswer={(choice, note) => answer(q.id, choice, note)}
                      />
                    ))}
                  </AnimatePresence>
                </section>
              )}
              {wish.ready && (
                <article className="step-result-action wish-grant">
                  <div>
                    <span className="step-result-kicker">
                      {t("wish.ready")}
                    </span>
                    <h3>{t("wish.ready_title")}</h3>
                  </div>
                  <button
                    type="button"
                    className="button accent"
                    onClick={() =>
                      quiet(
                        act(
                          () => clients.wishes.grant({ wishId: wish.id }),
                          [Change.WISH],
                          t("wish.granted_toast"),
                        ),
                      )
                    }
                  >
                    <Sparkles size={14} />
                    {t("wish.grant")}
                  </button>
                </article>
              )}
            </section>
          )}

          <section className="wish-section" aria-label={t("wish.tasks")}>
            <div className="section-heading">
              <h3>
                {t("wish.tasks")}
                <span className="count">{detail.tasks.length}</span>
              </h3>
            </div>
            {detail.tasks.length === 0 ? (
              <p className="muted-text">
                {detail.loaded ? t("wish.no_tasks") : t("common.loading")}
              </p>
            ) : (
              detail.tasks.map((task) => (
                <WishTask
                  key={task.id}
                  task={task}
                  project={allProjects.find((p) => p.id === task.projectId)}
                  onStop={() =>
                    quiet(
                      act(
                        () => clients.tasks.stop({ taskId: task.id }),
                        [Change.TASK],
                      ),
                    )
                  }
                />
              ))
            )}
          </section>

          {decided.length > 0 && (
            <div className="decision-history">
              <button
                className="text-button"
                onClick={() => setHistory(!history)}
              >
                <CheckCircle2 size={14} />
                {t("panels.decisions_recorded", { count: decided.length })}
                <ChevronDown size={13} className={history ? "rotated" : ""} />
              </button>
              <AnimatePresence>
                {history && (
                  <motion.div
                    initial={{ opacity: 0, height: 0 }}
                    animate={{ opacity: 1, height: "auto" }}
                    exit={{ opacity: 0, height: 0 }}
                  >
                    {decided.map((q) => (
                      <WishQuestion
                        key={q.id}
                        question={q}
                        onAnswer={async () => {}}
                      />
                    ))}
                  </motion.div>
                )}
              </AnimatePresence>
            </div>
          )}

          {detail.blocks.length > 0 && (
            <section className="wish-section" aria-label={t("wish.blocks")}>
              {detail.blocks.map((block) => (
                <WishBlock key={block.id} block={block} />
              ))}
            </section>
          )}

          {projects.length > 0 && (
            <Rights
              wish={wish}
              projects={projects}
              onAllow={(projectId, mode) =>
                quiet(
                  act(
                    () =>
                      clients.wishes.allow({
                        wishId: wish.id,
                        projectId,
                        mode,
                      }),
                    [Change.WISH],
                  ),
                )
              }
            />
          )}
        </div>
      </div>
    </div>
  );
}

// WishBlock shows a block as the lead wrote it: Markdown, or the text as it is for another media type.
function WishBlock({ block }: { block: Block }) {
  const markdown = !block.mediaType || block.mediaType === "text/markdown";
  return (
    <article className="wish-block" id={`block-${block.id}`}>
      <span className="eyebrow">{block.kind}</span>
      {block.title && <h3>{block.title}</h3>}
      {markdown ? (
        <MarkdownBody text={block.content} />
      ) : (
        <pre className="wish-block-raw">{block.content}</pre>
      )}
    </article>
  );
}

const modes = [Allowance.NONE, Allowance.EDIT, Allowance.AUTO] as const;
const modeKeys = {
  [Allowance.NONE]: "rights.none",
  [Allowance.EDIT]: "rights.edit",
  [Allowance.AUTO]: "rights.auto",
} as const;

// Rights shows, per project, what the wish allows its workers, and changes it (WishService.Allow).
function Rights({
  wish,
  projects,
  onAllow,
}: {
  wish: Wish;
  projects: Project[];
  onAllow: (projectId: string, mode: Allowance) => void;
}) {
  return (
    <section className="wish-section" aria-label={t("rights.title")}>
      <div className="section-heading">
        <h3>{t("rights.title")}</h3>
      </div>
      <p className="muted-text">{t("rights.detail")}</p>
      {projects.map((project) => (
        <div className="setting-row" key={project.id}>
          <div>
            <strong>{project.name}</strong>
            <p>{project.directory || t("project.no_folder")}</p>
          </div>
          <select
            aria-label={t("rights.for", { project: project.name })}
            value={allowanceOf(wish, project.id)}
            onChange={(e) => onAllow(project.id, Number(e.target.value))}
          >
            {modes.map((mode) => (
              <option key={mode} value={mode}>
                {t(modeKeys[mode])}
              </option>
            ))}
          </select>
        </div>
      ))}
    </section>
  );
}
