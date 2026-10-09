// The journal of a wish, as its page shows it (internal/render): the commands that changed it, from
// WishService.Snapshot, and the blocks of kind "log", the latest first. A command says in a line what it did, read
// from its request.
import { createRegistry } from "@bufbuild/protobuf";
import { type Timestamp, anyUnpack } from "@bufbuild/protobuf/wkt";

import {
  type Block,
  type Command,
  type Provider,
  type WishExport,
  file_plan_v1_plan,
} from "../../gen/ts/plan/v1/plan_pb";
import { providerName } from "../provider";

// The kind of the blocks that tell the story of the wish: they go to the journal, not to the notes.
export const LOG_KIND = "log";

// The most journal entries shown.
export const MAX_JOURNAL = 200;

export interface Entry {
  id: string;
  at?: Timestamp;
  // The command, as the command line names it: "question answer". Empty for a log block.
  command: string;
  summary: string;
  // A log block's content, in Markdown.
  note: string;
}

const registry = createRegistry(file_plan_v1_plan);

export function isLog(block: Block): boolean {
  return block.kind.toLowerCase() === LOG_KIND;
}

const kebab = (s: string) =>
  s.replace(/[A-Z]/g, (c, i) => (i ? "-" : "") + c.toLowerCase());

// commandName names a procedure as the command line does: /plan.v1.QuestionService/Answer is "question answer".
export function commandName(method: string): string {
  const [service, name] = method.replace(/^\//, "").split("/");
  if (!name) return method;
  const short = service
    .slice(service.lastIndexOf(".") + 1)
    .replace(/Service$/, "");
  return `${kebab(short)} ${kebab(name)}`;
}

// summary says in a line what a command did, as the page does.
function summary(command: Command, exp: WishExport): string {
  if (!command.request) return "";
  let req;
  try {
    req = anyUnpack(command.request, registry);
  } catch {
    return "";
  }
  if (!req) return "";
  const task = (id: string) => exp.tasks.find((x) => x.id === id)?.code ?? "";
  const r = req as unknown as Record<string, unknown>;
  switch (req.$typeName) {
    case "plan.v1.WishServiceMakeRequest":
    case "plan.v1.TaskServiceSpawnRequest":
      return String(r.title ?? "");
    case "plan.v1.QuestionServiceAskRequest":
      return String(r.text ?? "");
    case "plan.v1.QuestionServiceAnswerRequest": {
      const ref = (r.question as { ref?: { case?: string; value?: string } })
        ?.ref;
      const code =
        ref?.case === "id"
          ? (exp.questions.find((q) => q.id === ref.value)?.code ?? "")
          : (ref?.value ?? "");
      const choices = ["", "yes", "a", "b", "c", "d"];
      return `${code} ${choices[Number(r.choice)] ?? ""} ${String(r.note ?? "")}`.trim();
    }
    case "plan.v1.BlockServicePutRequest":
      return `${String(r.kind ?? "")} ${String(r.title ?? "")}`.trim();
    case "plan.v1.WishServiceAllowRequest": {
      const project =
        exp.projects.find((p) => p.id === r.projectId)?.name ?? "";
      const modes = ["", "none", "edit", "auto"];
      return `${modes[Number(r.mode)] ?? ""} ${project}`.trim();
    }
    case "plan.v1.WishServiceMoveRequest":
      return String(r.to ?? "");
    case "plan.v1.WishServiceSetProviderRequest":
      return providerName(Number(r.provider) as Provider);
  }
  if (typeof r.taskId === "string") return task(r.taskId);
  return "";
}

const ms = (ts?: Timestamp) =>
  ts ? Number(ts.seconds) * 1000 + ts.nanos / 1e6 : 0;

// journal is the wish's journal: its commands and its log blocks, the latest first, at most MAX_JOURNAL.
export function journal(
  exp: WishExport | undefined,
  blocks: readonly Block[],
): Entry[] {
  const entries: Entry[] = blocks.filter(isLog).map((b) => ({
    id: b.id,
    at: b.createTime,
    command: "",
    summary: b.title,
    note: b.content.trim(),
  }));
  for (const c of exp?.commands ?? [])
    entries.push({
      id: c.id,
      at: c.at,
      command: commandName(c.method),
      summary: summary(c, exp!),
      note: "",
    });
  entries.sort((a, b) => ms(b.at) - ms(a.at));
  return entries.slice(0, MAX_JOURNAL);
}
