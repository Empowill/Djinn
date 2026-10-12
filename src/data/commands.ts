// What the commands of the projects cost, grouped so that a short list tells where the time goes: by project, by the
// gate each command ran under, then by command with its arguments folded in.
//
// The grouping rule (commandGroup), kept simple on purpose:
//   1. `djinn gate run <gate> -- <command>` is unwrapped: <gate> is the gate, <command> what is grouped.
//   2. The command's first word is the tool; the words after it belong to the group's name (its subcommands) until
//      the first argument: a word starting with "-" (a flag, or "--"), holding "/", "\", "=" or a quote, starting with
//      ".", "~" or a digit, or ending in a file extension ("x.spec.ts"). Four words at most.
//   3. Everything from that argument on is the command's arguments. A command with arguments is a group apart from the
//      same command without any: `go tool task e2e` (the whole suite) and `go tool task e2e -- e2e/x.spec.ts` (some
//      specs) are two lines.
// Case is ignored, as the store ignores it.
import type { CommandCost } from "../../gen/ts/machine/v1/machine_pb";

export interface CommandGroup {
  // The gate the command names when it is a `djinn gate run`; empty otherwise.
  gate: string;
  // The tool and its subcommands: "go tool task e2e".
  head: string;
  // What follows them, as run; empty when nothing does.
  args: string;
  // The group: the head, with " …" when it has arguments.
  key: string;
}

const MAX_HEAD = 4;

function isArgument(word: string): boolean {
  return (
    /^[-.~0-9'"]/.test(word) ||
    /[/\\='"]/.test(word) ||
    /\.[A-Za-z0-9]{1,5}$/.test(word)
  );
}

// commandGroup splits a command as run into the group it counts in and its arguments (see the rule above).
export function commandGroup(command: string): CommandGroup {
  let words = command.trim().split(/\s+/).filter(Boolean);
  let gate = "";
  const tool = (words[0] ?? "").split(/[/\\]/).pop()?.toLowerCase() ?? "";
  if (
    (tool === "djinn" || tool === "djinn.exe") &&
    words[1] === "gate" &&
    words[2] === "run" &&
    words[3] &&
    !words[3].startsWith("-")
  ) {
    gate = words[3].toLowerCase();
    const dash = words.indexOf("--", 4);
    words = dash >= 0 ? words.slice(dash + 1) : words.slice(4);
  }
  const head = words.slice(0, 1);
  let i = 1;
  while (i < words.length && head.length < MAX_HEAD && !isArgument(words[i])) {
    head.push(words[i]);
    i++;
  }
  const name = head.join(" ").toLowerCase();
  const args = words.slice(i).join(" ");
  return { gate, head: name, args, key: args ? `${name} …` : name };
}

// CommandLine is one group of commands under one gate of a project.
export interface CommandLine {
  key: string;
  head: string;
  // Whether the group's commands take arguments: "go tool task e2e …".
  withArgs: boolean;
  // The different arguments run, the most run first.
  variants: string[];
  // Runs asked (the gate was asked for them), and runs that ran to their end (measured).
  asked: number;
  ran: number;
  // Time the runs took, in seconds, in total.
  seconds: number;
  // Time spent waiting for the gate, and holding it, in total, in seconds.
  waitSeconds: number;
  heldSeconds: number;
}

// GateLine is a gate (a named lock: one command holds it at a time, the others wait) and the commands run under it.
export interface GateLine {
  // Empty for commands run under no known gate.
  gate: string;
  commands: CommandLine[];
  asked: number;
  ran: number;
  seconds: number;
  waitSeconds: number;
  heldSeconds: number;
}

export interface ProjectLine {
  projectId: string;
  name: string;
  gates: GateLine[];
  seconds: number;
}

// mean is a total over the runs that ran; 0 when none did.
export function mean(total: number, ran: number): number {
  return ran > 0 ? total / ran : 0;
}

// gateTimes is what a command's runs waited for their gate and held it, in total: the mean of its recent runs (the
// last 200 the store keeps) over all its runs.
function gateTimes(c: CommandCost): { wait: number; held: number } {
  const recent = c.recentRuns ?? [];
  if (recent.length === 0)
    return {
      wait: c.p50WaitSeconds * c.runs,
      held: c.p50HeldSeconds * c.runs,
    };
  let wait = 0;
  let held = 0;
  for (const r of recent) {
    wait += r.waitSeconds;
    held += r.heldSeconds;
  }
  return {
    wait: (wait / recent.length) * c.runs,
    held: (held / recent.length) * c.runs,
  };
}

function byTime<T extends { seconds: number }>(a: T, b: T): number {
  return b.seconds - a.seconds;
}

// groupCommands groups the costs of the projects in projectIds (all when undefined), the projects, gates and commands
// that took the most time first.
export function groupCommands(
  costs: readonly CommandCost[],
  projectIds?: ReadonlySet<string>,
  projectNames?: ReadonlyMap<string, string>,
): ProjectLine[] {
  const projects = new Map<string, ProjectLine>();
  // The arguments of each command line, with how many runs each.
  const variants = new Map<CommandLine, Map<string, number>>();
  for (const c of costs) {
    if (projectIds && !projectIds.has(c.projectId)) continue;
    const group = commandGroup(c.command);
    const gate = (
      c.gate ||
      c.recentRuns?.at(-1)?.gate ||
      group.gate ||
      ""
    ).toLowerCase();
    let project = projects.get(c.projectId);
    if (!project) {
      project = {
        projectId: c.projectId,
        name:
          c.projectName || projectNames?.get(c.projectId) || c.projectId || "",
        gates: [],
        seconds: 0,
      };
      projects.set(c.projectId, project);
    }
    let g = project.gates.find((x) => x.gate === gate);
    if (!g) {
      g = {
        gate,
        commands: [],
        asked: 0,
        ran: 0,
        seconds: 0,
        waitSeconds: 0,
        heldSeconds: 0,
      };
      project.gates.push(g);
    }
    let line = g.commands.find((x) => x.key === group.key);
    if (!line) {
      line = {
        key: group.key,
        head: group.head,
        withArgs: !!group.args,
        variants: [],
        asked: 0,
        ran: 0,
        seconds: 0,
        waitSeconds: 0,
        heldSeconds: 0,
      };
      g.commands.push(line);
      variants.set(line, new Map());
    }
    const asked = Math.max(c.asked ?? 0, c.runs);
    const seconds = c.seconds * c.runs;
    const { wait, held } = gateTimes(c);
    for (const x of [line, g]) {
      x.asked += asked;
      x.ran += c.runs;
      x.seconds += seconds;
      x.waitSeconds += wait;
      x.heldSeconds += held;
    }
    project.seconds += seconds;
    if (group.args) {
      const seen = variants.get(line)!;
      seen.set(group.args, (seen.get(group.args) ?? 0) + Math.max(asked, 1));
    }
  }
  for (const [line, seen] of variants)
    line.variants = [...seen.entries()]
      .sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]))
      .map(([args]) => args);
  const out = [...projects.values()].sort(byTime);
  for (const p of out) {
    p.gates.sort(byTime);
    for (const g of p.gates) g.commands.sort(byTime);
  }
  return out;
}
