// The screen of one wish, read from the services: what waits for you first (its open questions, the blocking ones
// first, its workers that wait, and "My wish is granted" once Djinn proposes it), then its tasks (those that need an
// eye first) with what they spent, its decisions, its blocks, its journal and the rights its workers have. Djinn
// proposes; only the user grants.
import {
  CheckCircle2,
  ChevronDown,
  ChevronRight,
  Clock3,
  Download,
  FileText,
  GitBranch,
  Hourglass,
  Pause,
  Play,
  ScrollText,
  Sparkles,
  Terminal,
} from "lucide-react";
import { AnimatePresence, motion } from "motion/react";
import { useEffect, useRef, useState } from "react";

import {
  Allowance,
  type Block,
  Change,
  type Choice,
  type Project,
  type Task,
  TaskStatus,
  type Wish,
  type WishExport,
  WishState,
} from "../gen/ts/plan/v1/plan_pb";
import { message } from "./data/client";
import { useClients, useData, useStore, useWishDetail } from "./data/djinn";
import { openQuestions, spent, waitingTasks } from "./data/flight";
import {
  allowanceOf,
  isActive,
  isOpen,
  projectsOf,
  when,
  wishStateText,
} from "./data/format";
import { type Entry, isLog, journal } from "./data/journal";
import { t } from "./i18n";
import { MarkdownBody } from "./markdown-body";
import { useKeepPlace } from "./scroll-anchor";
import { SpentLine } from "./usage";
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
  const open = openQuestions(wish, detail);
  const waiting = waitingTasks(wish, detail);
  const codes = new Map(detail.tasks.map((task) => [task.id, task.code]));
  const notes = detail.blocks.filter((b) => !isLog(b));
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
          {(open.length > 0 || waiting.length > 0 || wish.ready) && (
            <section
              className="action-center"
              id="action-center"
              aria-label={t("panels.your_move")}
            >
              <div className="action-center-heading">
                <div>
                  <span className="eyebrow">{t("panels.next_action")}</span>
                  <h2>{t("panels.your_move")}</h2>
                  {(open.length > 0 || wish.ready) && (
                    <p>
                      {open.length
                        ? t("wish.questions_wait", { count: open.length })
                        : t("wish.ready_detail")}
                    </p>
                  )}
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
                    {open.map(({ item: q, blocking }) => (
                      <WishQuestion
                        key={q.id}
                        question={q}
                        blocking={blocking}
                        expanded={open.length === 1}
                        onAnswer={(choice, note) => answer(q.id, choice, note)}
                      />
                    ))}
                  </AnimatePresence>
                </section>
              )}
              {waiting.length > 0 && <WaitingTasks waiting={waiting} />}
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
              <SpentLine spent={spent(detail.tasks)} />
            </div>
            {detail.tasks.length === 0 ? (
              <p className="muted-text">
                {detail.loaded ? t("wish.no_tasks") : t("common.loading")}
              </p>
            ) : (
              byAttention(detail.tasks).map((task) => (
                <WishTask
                  key={task.id}
                  task={task}
                  codes={codes}
                  project={allProjects.find((p) => p.id === task.projectId)}
                  onStop={() =>
                    quiet(
                      act(
                        () => clients.tasks.stop({ taskId: task.id }),
                        [Change.TASK],
                      ),
                    )
                  }
                  onSend={(text) =>
                    act(() => clients.tasks.send({ taskId: task.id, text }), [])
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

          {notes.length > 0 && (
            <section className="wish-section" aria-label={t("wish.blocks")}>
              <div className="section-heading">
                <h3>
                  {t("wish.blocks")}
                  <span className="count">{notes.length}</span>
                </h3>
              </div>
              {notes.map((block) => (
                <WishBlock
                  key={block.id}
                  block={block}
                  task={codes.get(block.taskId) ?? ""}
                />
              ))}
            </section>
          )}

          <Journal wish={wish} blocks={detail.blocks} />

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

// WishOrigin marks a line with its wish, in the flight plan of several wishes: its rank and its title.
export function WishOrigin({ wish }: { wish: Wish }) {
  return (
    <span className="wish-origin" title={wish.title}>
      {wish.rank > 0 && <b>{wish.rank}</b>}
      {wish.title}
    </span>
  );
}

// WaitingTasks are the workers that wait for the user: for an answer before they edit, or cut short by a stop.
export function WaitingTasks({
  waiting,
  origin = false,
}: {
  waiting: { item: Task; question: string; wish: Wish }[];
  // Each line shows its wish.
  origin?: boolean;
}) {
  return (
    <section className="plan-waiting">
      <div className="section-heading">
        <h3>
          {t("page.actions")}
          <span className="count">{waiting.length}</span>
        </h3>
      </div>
      {waiting.map(({ item, question, wish }) => (
        <p className="plan-line" key={item.id}>
          <Hourglass size={14} aria-hidden="true" />
          {origin && <WishOrigin wish={wish} />}
          <span>
            {item.status === TaskStatus.INTERRUPTED
              ? t("page.action_interrupted", { task: item.code })
              : question
                ? t("page.action_waiting", { task: item.code, question })
                : t("page.action_waiting_unknown", { task: item.code })}
          </span>
        </p>
      ))}
    </section>
  );
}

// The tasks that need an eye come first (running, waiting, failed, cut short), then the planned ones, then the
// finished ones; each group keeps the lamp's order.
function byAttention(tasks: readonly Task[]): Task[] {
  const group = (task: Task) =>
    task.status === TaskStatus.PENDING || task.status === TaskStatus.UNSPECIFIED
      ? 1
      : task.status === TaskStatus.DONE || task.status === TaskStatus.STOPPED
        ? 2
        : 0;
  return [...tasks].sort((a, b) => group(a) - group(b));
}

// A block this long, in characters or lines, is folded under its title, as on the wish's page.
const LONG_BLOCK = 800;
const LONG_BLOCK_LINES = 16;

// WishBlock shows a block as the lead wrote it: Markdown, or the text as it is for another media type. A long one
// opens on a click.
function WishBlock({ block, task }: { block: Block; task: string }) {
  const markdown = !block.mediaType || block.mediaType === "text/markdown";
  const long =
    block.content.length > LONG_BLOCK ||
    block.content.split("\n").length > LONG_BLOCK_LINES;
  const [unfolded, setUnfolded] = useState(false);
  const folded = long && !unfolded;
  return (
    <article
      className={`wish-block ${folded ? "folded" : ""}`}
      id={`block-${block.id}`}
    >
      <span className="eyebrow">
        {block.kind}
        {task && ` · ${t("page.about_task", { task })}`}
        {block.updateTime && ` · ${when(block.updateTime)}`}
      </span>
      {block.title && <h3>{block.title}</h3>}
      <div className="wish-block-body">
        {markdown ? (
          <MarkdownBody text={block.content} />
        ) : (
          <pre className="wish-block-raw">{block.content}</pre>
        )}
      </div>
      {long && (
        <button
          className="text-button"
          onClick={() => setUnfolded(!unfolded)}
          aria-expanded={!folded}
        >
          <ChevronDown size={13} className={folded ? "" : "rotated"} />
          {folded ? t("wish.block_more") : t("wish.block_less")}
        </button>
      )}
    </article>
  );
}

// Journal is the story of the wish: its log blocks at once, and the commands that changed it once asked for.
// WishService.Snapshot reads the whole wish, events included: it is read on a click, then again as the wish changes.
function Journal({ wish, blocks }: { wish: Wish; blocks: Block[] }) {
  const clients = useClients();
  const [commands, setCommands] = useState(false);
  const [exp, setExp] = useState<WishExport>();
  const [error, setError] = useState("");
  // What the store read of the wish: a new one means the wish changed.
  const detail = useData((s) => s.details[wish.id]);
  useEffect(() => {
    if (!commands) return;
    let current = true;
    clients.wishes
      .snapshot({ wishId: wish.id })
      .then((res) => {
        if (current) (setExp(res.export), setError(""));
      })
      .catch((err) => {
        if (current) setError(message(err));
      });
    return () => {
      current = false;
    };
  }, [clients, wish, commands, detail]);
  const entries = journal(commands ? exp : undefined, blocks);
  return (
    <section
      className="wish-section wish-journal"
      aria-label={t("page.journal")}
    >
      <div className="section-heading">
        <h3>
          {t("page.journal")}
          {entries.length > 0 && (
            <span className="count">{entries.length}</span>
          )}
        </h3>
        <button
          className="text-button"
          onClick={() => setCommands(!commands)}
          aria-pressed={commands}
        >
          <ScrollText size={13} />
          {commands ? t("wish.journal_hide") : t("wish.journal_show")}
        </button>
      </div>
      {error && <p className="muted-text">{error}</p>}
      {entries.length > 0 && (
        <ol className="journal-list">
          {entries.map((entry) => (
            <JournalEntry key={entry.id} entry={entry} />
          ))}
        </ol>
      )}
    </section>
  );
}

function JournalEntry({ entry }: { entry: Entry }) {
  return (
    <li className={entry.command ? "command" : "log"}>
      <time>{when(entry.at)}</time>
      <div>
        {entry.command && <code>{entry.command}</code>}
        {entry.summary && <span>{entry.summary}</span>}
        {entry.note && <MarkdownBody text={entry.note} />}
      </div>
    </li>
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
