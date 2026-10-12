// Whether a newer Djinn waits at the path of the running one (UiService.WatchUpdate), or a build
// committed waits to be installed, and the click that installs it and restarts on it
// (UiService.Update). Nothing installs nor restarts without that click.
import { type Transport, createClient } from "@connectrpc/connect";

import {
  type Build,
  InstallStep,
  UiService,
  type UiServiceWatchUpdateResponse,
} from "../../gen/ts/ui/v1/ui_pb";

export { InstallStep };

export interface UpdateState {
  // Version of the running Djinn.
  current: string;
  // Version of the newer Djinn waiting; empty for none.
  ready: string;
  // The newer Djinn waiting, named as a build: where it comes from, what it holds against the running one; undefined
  // when Djinn cannot tell.
  readyBuild?: BuildProposal;
  // The terminals the last restart could not run again, one line each.
  notResumed: string[];
  // Where the release notes of the newer Djinn are; empty for none.
  notesUrl: string;
  // The last build pushed of each integration branch and not installed, the running Djinn's branch first.
  builds: BuildProposal[];
  // The install of a build that runs now, and where it stands; undefined for none.
  installing?: Installing;
}

// An install that runs now: the commit, its step, and what it waits for while it waits.
export interface Installing {
  sha: string;
  step: InstallStep;
  waiting: string;
}

// A task's summary in a build proposal.
export interface TaskSummary {
  code: string;
  title: string;
  summary: string;
}

// A build of an integration branch, to install (T07), or the newer Djinn waiting, named as one.
export interface BuildProposal {
  wishTitle: string;
  project: string;
  branch: string;
  sha: string;
  // The tasks it brings, by code: merged since the running Djinn when base is set, else in the push.
  tasks: string[];
  // The titles of the commits it brings, the latest first.
  changes: string[];
  // What changed, one per task: its code, title and worker's summary.
  summaries: TaskSummary[];
  // The commit of the running Djinn it is compared with; empty when it is not.
  base: string;
  // The running Djinn was built from its branch.
  running: boolean;
  // How many commits it adds to the running Djinn: changes holds the latest.
  changesCount: number;
  // The commits the running Djinn holds and it does not, the latest first, and how many.
  lacks: string[];
  lacksCount: number;
}

// buildProposal reads a build as the server sends it.
export function buildProposal(b: Build): BuildProposal {
  return {
    wishTitle: b.wishTitle,
    project: b.project,
    branch: b.branch,
    sha: b.sha,
    tasks: [...b.tasks],
    changes: [...b.changes],
    summaries: b.summaries.map((s) => ({
      code: s.code,
      title: s.title,
      summary: s.summary,
    })),
    base: b.base,
    running: b.running,
    changesCount: b.changesCount,
    lacks: [...b.lacks],
    lacksCount: b.lacksCount,
  };
}

// updateState reads the update as the server sends it.
export function updateState(res: UiServiceWatchUpdateResponse): UpdateState {
  return {
    current: res.current,
    ready: res.ready,
    readyBuild: res.readyBuild && buildProposal(res.readyBuild),
    notResumed: [...res.notResumed],
    notesUrl: res.notesUrl,
    builds: res.builds.map(buildProposal),
    installing: res.installing && {
      sha: res.installing.sha,
      step: res.installing.step,
      waiting: res.installing.waiting,
    },
  };
}

export interface DjinnUpdate {
  // Calls callback with the state at once when known, then at each change.
  subscribe(callback: (state: UpdateState) => void): () => void;
  // Restarts Djinn on the newer one; with build, the commit of the build proposed, installs it first. The
  // page loses its server for a moment, then the new one answers; version is empty when nothing restarts.
  update(
    build?: string,
  ): Promise<{ version: string; terminals: number; installed: string }>;
}

export function createUpdate(transport: Transport, retry = 1000): DjinnUpdate {
  const ui = createClient(UiService, transport);
  const listeners = new Set<(state: UpdateState) => void>();
  let last: UpdateState | undefined;
  let started = false;

  async function watch() {
    for (;;) {
      try {
        for await (const res of ui.watchUpdate({})) {
          last = updateState(res);
          listeners.forEach((listener) => listener(last!));
        }
      } catch {
        // Djinn restarted, or the link broke: watch again.
      }
      await new Promise((r) => setTimeout(r, retry));
    }
  }

  return Object.freeze({
    subscribe: (callback: (state: UpdateState) => void) => {
      listeners.add(callback);
      if (last) callback(last);
      if (!started) {
        started = true;
        void watch();
      }
      return () => void listeners.delete(callback);
    },
    update: async (build?: string) => {
      const res = await ui.update({ build: build ?? "" });
      return {
        version: res.version,
        terminals: res.terminals,
        installed: res.installed,
      };
    },
  });
}
