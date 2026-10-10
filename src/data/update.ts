// Whether a newer Djinn waits at the path of the running one (UiService.WatchUpdate), or a build
// committed waits to be installed, and the click that installs it and restarts on it
// (UiService.Update). Nothing installs nor restarts without that click.
import { type Transport, createClient } from "@connectrpc/connect";

import { InstallStep, UiService } from "../../gen/ts/ui/v1/ui_pb";

export { InstallStep };

export interface UpdateState {
  // Version of the running Djinn.
  current: string;
  // Version of the newer Djinn waiting; empty for none.
  ready: string;
  // The terminals the last restart could not run again, one line each.
  notResumed: string[];
  // Where the release notes of the newer Djinn are; empty for none.
  notesUrl: string;
  // The last batch committed into a wish's integration branch and not installed; undefined for none.
  build?: BuildProposal;
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

// A batch of finished work committed into a wish's integration branch, to install (T07).
export interface BuildProposal {
  wishTitle: string;
  project: string;
  branch: string;
  sha: string;
  // The tasks of the batch, by code.
  tasks: string[];
  // The titles of the commits it brought, the latest first.
  changes: string[];
  // What changed, one per task: its code, title and worker's summary.
  summaries: TaskSummary[];
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
          last = {
            current: res.current,
            ready: res.ready,
            notResumed: [...res.notResumed],
            notesUrl: res.notesUrl,
            build: res.build && {
              wishTitle: res.build.wishTitle,
              project: res.build.project,
              branch: res.build.branch,
              sha: res.build.sha,
              tasks: [...res.build.tasks],
              changes: [...res.build.changes],
              summaries: res.build.summaries.map((s) => ({
                code: s.code,
                title: s.title,
                summary: s.summary,
              })),
            },
            installing: res.installing && {
              sha: res.installing.sha,
              step: res.installing.step,
              waiting: res.installing.waiting,
            },
          };
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
