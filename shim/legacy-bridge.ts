// A temporary bridge: the interface still keeps each mission in its own saved state, in the model the Electron
// version used. Until it reads the Go services directly, in their proto form (T03), importSession and
// exportSession go through WishService, and an imported wish is converted here into a mission of that model.
// This whole file disappears at that switch. It converts one way only, from a wish to a mission: nothing turns an
// old "djinn-session" file into a wish.
import { fromBinary } from "@bufbuild/protobuf";
import { type Timestamp, timestampDate } from "@bufbuild/protobuf/wkt";
import {
  Code,
  ConnectError,
  type Transport,
  createClient,
} from "@connectrpc/connect";

import {
  type Block,
  Choice,
  type Project as GoProject,
  type Task as GoTask,
  TaskEventKind,
  TaskStatus,
  WishExportSchema,
  WishService,
  type WishServiceSnapshotResponse,
  WishState,
} from "../gen/ts/plan/v1/plan_pb";

// The largest file the import reads, as the server does.
const MAX_FILE = 100 * 1024 * 1024;
// The interface bounds a work item's detail.
const MAX_DETAIL = 4000;

type Fail = (code: string, message: string) => Error;

// legacyExchange gives the bridge its importSession, exportSession and grantWish. pick chooses the file to import;
// by default a file picker of the page.
export function legacyExchange(
  transport: Transport,
  fail: Fail,
  pick: () => Promise<File | null> = pickFile,
) {
  const wishes = createClient(WishService, transport);
  // The wish of a mission, found by its title: the interface keeps no wish identifier yet. Only a mission that is a
  // wish of Djinn has one: the interface's own missions wait for the switch to the Go services. A session file holds
  // its mission in task; grantWish gets the mission itself.
  const wishOf = async (session: unknown) => {
    const s = session as { title?: unknown; task?: { title?: unknown } };
    const title = String(s?.task?.title ?? s?.title ?? "");
    const { wishes: all } = await wishes.list({});
    const latest = [...all].reverse();
    const wish =
      latest.find((w) => w.title === title) ??
      latest.find((w) => w.title.toLowerCase() === title.toLowerCase());
    if (!wish)
      throw fail(
        "not_available",
        "Only a wish of Djinn can be exported or granted yet: import it or make it with djinn wish make",
      );
    return wish;
  };
  return {
    // Import a .djinn file (or its JSON form) as a wish, then hand it to the interface as a mission.
    importSession: async () => {
      const file = await pick();
      if (!file) return null;
      if (file.size > MAX_FILE)
        throw fail("input_too_large", "The file is larger than 100 MB");
      const data = new Uint8Array(await file.arrayBuffer());
      let wishId: string;
      try {
        wishId = (await wishes.importData({ data })).wish?.id ?? "";
      } catch (error) {
        // Already here: show the wish as Djinn holds it, without importing it twice.
        if (
          !(error instanceof ConnectError) ||
          error.code !== Code.AlreadyExists
        )
          throw error;
        wishId = wishIdOf(data);
      }
      return wishToSession(await wishes.snapshot({ wishId }));
    },
    // Export the mission's wish to a .djinn file in the Downloads folder. Only a mission that is a wish of Djinn
    // can be exported: the interface's own missions wait for the switch to the Go services.
    exportSession: async (session: unknown) => {
      const wish = await wishOf(session);
      const { file } = await wishes.export({ wishId: wish.id });
      return { path: file, filename: file.split(/[\\/]/).pop() };
    },
    // Grant the mission's wish: the user says it is done. Djinn proposes it once the wish is ready, never grants it.
    grantWish: async (mission: unknown) => {
      const wish = await wishOf(mission);
      await wishes.grant({ wishId: wish.id });
      return { granted: true };
    },
  };
}

function wishIdOf(data: Uint8Array): string {
  const text = new TextDecoder().decode(data.subarray(0, 1)).trim();
  if (text === "{")
    return JSON.parse(new TextDecoder().decode(data))?.wish?.id ?? "";
  return fromBinary(WishExportSchema, data).wish?.id ?? "";
}

function pickFile(): Promise<File | null> {
  return new Promise((resolve) => {
    const input = document.createElement("input");
    input.type = "file";
    input.accept = ".djinn,.json";
    input.style.display = "none";
    const done = (file: File | null) => {
      input.remove();
      resolve(file);
    };
    input.addEventListener("change", () => done(input.files?.[0] ?? null));
    input.addEventListener("cancel", () => done(null));
    document.body.append(input);
    input.click();
  });
}

const letters = ["A", "B", "C", "D"];

// An option is "Label — description" when it has both.
function option(text: string, index: number) {
  const [label, ...rest] = text.split(" — ");
  return {
    id: letters[index] ?? String(index + 1),
    label: rest.length && label.length <= 200 ? label : text,
    description: rest.length && label.length <= 200 ? rest.join(" — ") : "",
  };
}

function answerText(choice: Choice, options: string[], note: string): string {
  if (choice === Choice.YES || choice === Choice.UNSPECIFIED)
    return note || "Yes";
  const index = choice - Choice.A;
  const picked = `${letters[index]} · ${option(options[index] ?? "", index).label}`;
  return note ? `${picked}\n\n${note}` : picked;
}

function content(block: Block): string {
  const type = block.mediaType || "text/markdown";
  if (type === "text/markdown" || type.startsWith("text/plain"))
    return block.content;
  if (type === "text/html") return block.content;
  const lang =
    type
      .split("/")
      .pop()
      ?.replace(/^vnd\./, "")
      .replace(/\+.*$/, "") ?? "";
  return "```" + lang + "\n" + block.content + "\n```";
}

function artifactType(block: Block) {
  const type = block.mediaType || "text/markdown";
  if (type === "text/html") return "visualization" as const;
  if (type.includes("mermaid") || block.kind.toLowerCase() === "diagram")
    return "diagram" as const;
  return "document" as const;
}

const workerStatus: Record<number, string> = {
  [TaskStatus.PENDING]: "queued",
  [TaskStatus.RUNNING]: "running",
  [TaskStatus.DONE]: "done",
  [TaskStatus.FAILED]: "error",
  [TaskStatus.STOPPED]: "blocked",
  [TaskStatus.INTERRUPTED]: "blocked",
};

const itemStatus: Record<number, string> = {
  [TaskStatus.RUNNING]: "running",
  [TaskStatus.DONE]: "done",
  [TaskStatus.FAILED]: "blocked",
  [TaskStatus.STOPPED]: "blocked",
  [TaskStatus.INTERRUPTED]: "blocked",
};

// wishToSession converts a wish, as Snapshot returns it, into the session file the interface imports: a mission
// with its questions, decisions, workers, plan items and documents.
export function wishToSession(snapshot: WishServiceSnapshotResponse) {
  const exp = snapshot.export;
  const wish = exp?.wish;
  if (!exp || !wish) throw new Error("The wish is empty");
  const created = iso(wish.createTime) ?? new Date().toISOString();
  const at = (ts?: Timestamp) => iso(ts) ?? created;
  const stepId = `${wish.id}:step:1`;
  const blocksOf = new Map<string, Block[]>();
  const free: Block[] = [];
  for (const b of exp.blocks) {
    if (b.taskId)
      blocksOf.set(b.taskId, [...(blocksOf.get(b.taskId) ?? []), b]);
    else free.push(b);
  }
  const kind = (b: Block) => b.kind.toLowerCase();
  const brief = free
    .filter((b) => kind(b) === "brief")
    .map((b) => b.content)
    .join("\n\n");
  const taskText = (t: GoTask) =>
    (blocksOf.get(t.id) ?? [])
      .map((b) => (b.title ? `**${b.title}**\n\n` : "") + content(b))
      .join("\n\n");
  const workers = exp.tasks.filter((t) => t.startTime);
  const items = exp.tasks.filter((t) => !t.startTime);

  const projects = snapshot.projects.map((p: GoProject) => ({
    id: p.id,
    name: p.name,
    directory: p.directory,
    conventions: p.remote ? `Remote: ${p.remote}` : "",
    locations: {},
    workflows: [],
    updatedAt: at(p.createTime),
  }));
  const main = projects.find((p) => p.id === wish.projectIds[0]);

  const events = [
    ...free
      .filter((b) => ["log", "decision"].includes(kind(b)))
      .map((b) => ({
        stepId,
        id: b.id,
        time: at(b.createTime),
        type:
          kind(b) === "decision" ? ("decision" as const) : ("note" as const),
        title: b.title || b.kind,
        detail: content(b),
        actor: "agent" as const,
      })),
    ...exp.events
      .filter((e) =>
        [TaskEventKind.STATUS, TaskEventKind.ERROR].includes(e.kind),
      )
      .map((e) => {
        const task = exp.tasks.find((t) => t.id === e.taskId);
        return {
          stepId,
          id: e.id,
          time: at(e.createTime),
          type:
            e.kind === TaskEventKind.ERROR
              ? ("error" as const)
              : ("agent" as const),
          title: `${task?.code ?? ""} ${e.text}`.trim().slice(0, 200),
          detail: e.text,
          agentId: task?.code,
        };
      }),
  ].sort((a, b) => a.time.localeCompare(b.time));

  // Djinn proposes to grant a ready wish: only then does the step show a result, and the button to grant it. A wish
  // still at work shows none, nor a move to make.
  const granted = wish.state === WishState.GRANTED;
  const stepStatus = granted
    ? "completed"
    : wish.ready
      ? "awaiting_human"
      : "running";
  const task = {
    id: wish.id,
    title: wish.title,
    fromWish: true,
    brief,
    project: main?.directory ?? "",
    ...(main ? { projectId: main.id } : {}),
    provider: "claude",
    model: "",
    phase: "execution",
    status: "waiting",
    createdAt: created,
    questions: exp.questions.map((q) => ({
      stepId,
      id: q.code,
      title: q.text,
      context: q.context,
      recommendation: q.recommendation,
      options: q.options.map(option),
      blocking: false,
      unlocks: "",
      ...(q.answer
        ? {
            answer: answerText(q.answer.choice, q.options, q.answer.note),
            answeredAt: at(q.answer.createTime),
          }
        : {}),
    })),
    agents: workers.map((t) => ({
      stepId,
      id: t.code,
      name: t.code,
      role: t.title,
      provider: "claude",
      model: "",
      status: workerStatus[t.status] ?? "queued",
      ...(t.status === TaskStatus.INTERRUPTED || t.status === TaskStatus.STOPPED
        ? { waitReason: t.error || "Stopped" }
        : {}),
      summary: taskText(t) || t.error,
      progress: t.status === TaskStatus.DONE ? 100 : 0,
      ...(t.branch ? { branch: t.branch } : {}),
    })),
    events,
    artifacts: free
      .filter((b) => !["log", "decision", "brief"].includes(kind(b)))
      .map((b) => ({
        stepId,
        id: b.id,
        title: b.title || b.kind,
        type: artifactType(b),
        content: content(b),
        updatedAt: at(b.updateTime ?? b.createTime),
        revision: 1,
        editedBy: "agent",
      })),
    feedback: [],
    actions: [],
    instructions: [],
    workItems: items.slice(0, 200).map((t) => {
      const detail = taskText(t);
      return {
        id: t.id,
        title: `${t.code} · ${t.title}`.slice(0, 1000),
        status: itemStatus[t.status] ?? "pending",
        updatedAt: at(t.endTime ?? t.createTime),
        stepId,
        ticket: t.code,
        ...(detail
          ? {
              detail:
                detail.length > MAX_DETAIL
                  ? detail.slice(0, MAX_DETAIL - 1) + "…"
                  : detail,
            }
          : {}),
      };
    }),
    steps: [
      {
        id: stepId,
        type: "implementation",
        title: wish.title.slice(0, 1000),
        objective: brief || wish.title,
        status: stepStatus,
        exitCriteria: [],
        expectedArtifacts: [],
        skills: [],
        startedAt: created,
        ...(granted && wish.grantTime
          ? {
              completedAt: at(wish.grantTime),
              approvedAt: at(wish.grantTime),
              approvedBy: "human",
            }
          : {}),
      },
    ],
    activeStepId: stepId,
    selectedStepId: stepId,
    progressionPolicy: "manual",
    workflowMode: "fixed",
    titleSource: "human",
    planCompleted: false,
    demo: false,
    configuration: {
      prototype: "",
      review: "",
      deliverables: [],
      concurrency: 4,
    },
  };
  return {
    format: "djinn-session",
    version: 2,
    exportedAt: at(exp.createTime),
    projects,
    task,
  };
}

function iso(ts?: Timestamp): string | undefined {
  return ts ? timestampDate(ts).toISOString() : undefined;
}
