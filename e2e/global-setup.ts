// Starts one `djinn up --browser` per Playwright worker, each on a free port with a data folder of its own, waits
// until each answers, and hands worker i its URL (with the token) and its data folder through DJINN_URL_i and
// DJINN_E2E_HOME_i; playwright.config.ts, loaded again in each worker, turns its own into DJINN_URL and DJINN_E2E_HOME,
// which the specs read. The teardown stops them with SIGINT and checks that each exits cleanly; on Windows it kills
// them.
import { spawn } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import type { FullConfig } from "@playwright/test";

import { fakeAgents, withAgents } from "./agents";

const binary = path.resolve(
  __dirname,
  "../bin/djinn-e2e" + (process.platform === "win32" ? ".exe" : ""),
);

// Where a djinn's logs go before its data directory is removed, one folder per worker: a push check's djinn that died
// mid-run left nothing to say why, every spec after it refused (W248).
const kept = (slot: number) =>
  path.resolve(__dirname, "../test-results/e2e/djinn-logs", String(slot));

// keepLogs copies the djinn's logs (djinn.log, crash.txt, goroutine dumps) out of its data directory.
function keepLogs(slot: number, home: string) {
  fs.cpSync(path.join(home, "logs"), kept(slot), {
    recursive: true,
    force: true,
  });
}

// started is a djinn of the suite: worker slot's.
type started = {
  slot: number;
  home: string;
  url: string;
  stop: () => Promise<void>;
};

// startOne starts the djinn of worker slot and waits until it answers with an agent ready.
async function startOne(slot: number): Promise<started> {
  // A data directory of its own: the tests never touch the developer's state nor the address of their djinn.
  const home = fs.mkdtempSync(path.join(os.tmpdir(), `djinn-e2e-${slot}-`));
  // A fake claude and a fake agy (Antigravity), first on the PATH of djinn (agents.ts).
  const bin = path.join(home, "bin");
  fakeAgents(bin);
  // The window's terminal opens in a folder of its own: without a project, djinn would ask for one instead.
  const shell = path.join(home, "shell");
  fs.mkdirSync(shell);
  // djinn's git sees what a CI runner's sees, not the developer's: no identity (a spec gives its project's repository
  // one: Djinn's merge commits take it), files kept as committed.
  const gitconfig = path.join(home, "gitconfig");
  fs.writeFileSync(
    gitconfig,
    "[core]\n\tautocrlf = false\n\tlongpaths = true\n",
  );
  const env: NodeJS.ProcessEnv = {
    ...process.env,
    GIT_CONFIG_GLOBAL: gitconfig,
  };
  for (const name of [
    "GIT_AUTHOR_NAME",
    "GIT_AUTHOR_EMAIL",
    "GIT_COMMITTER_NAME",
    "GIT_COMMITTER_EMAIL",
    "EMAIL",
  ])
    delete env[name];
  const djinn = spawn(binary, ["up", "--browser", "--terminal-dir", shell], {
    stdio: ["ignore", "pipe", "inherit"],
    // The terminal of the window runs a plain POSIX shell, whatever the developer's own shell is.
    env: {
      ...withAgents(env, bin),
      DJINN_HOME: home,
      ...(process.platform === "win32" ? {} : { SHELL: "/bin/sh" }),
    },
  });
  // The teardown stops it: an end before that fails every spec of its worker after it, so it is said at once and
  // kept. Said at the teardown, the line would push a failed spec's name out of the last lines a push check keeps.
  let stopping = false;
  const exited = new Promise<number | null>((resolve) =>
    djinn.once("exit", (code, signal) => {
      const line = `djinn ${slot} exited at ${new Date().toISOString()}: code ${code}, signal ${signal}\n`;
      fs.mkdirSync(kept(slot), { recursive: true });
      fs.appendFileSync(path.join(kept(slot), "exit.txt"), line);
      if (!stopping) process.stderr.write(line);
      resolve(code);
    }),
  );

  const url = await new Promise<string>((resolve, reject) => {
    const timer = setTimeout(
      () => reject(new Error(`djinn ${slot} printed no URL`)),
      10_000,
    );
    let out = "";
    djinn.stdout.on("data", (chunk: Buffer) => {
      out += chunk.toString();
      const match = out.match(/http:\/\/127\.0\.0\.1:\d+\/\?token=[0-9a-f]+/);
      if (match) {
        clearTimeout(timer);
        resolve(match[0]);
      }
    });
    djinn.once("exit", (code) =>
      reject(new Error(`djinn ${slot} exited: ${code}`)),
    );
  });

  const token = new URL(url).searchParams.get("token")!;
  const deadline = Date.now() + 10_000;
  for (;;) {
    const res = await fetch(new URL("/", url), {
      headers: { Authorization: `Bearer ${token}` },
    }).catch(() => undefined);
    if (res?.ok) break;
    if (Date.now() > deadline)
      throw new Error(`djinn ${slot} does not answer on ${url}`);
    await new Promise((r) => setTimeout(r, 100));
  }
  // No agent ready, the setup of the agents would cover the window and take every spec's clicks: say it once, here.
  const res = await fetch(new URL("/ui.v1.UiService/GetEnvironment", url), {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      Authorization: `Bearer ${token}`,
    },
    body: JSON.stringify({ agents: true }),
  });
  const { providers = [] } = (await res.json()) as {
    providers?: { id: string; state?: string }[];
  };
  if (!providers.some((p) => p.state === "PROVIDER_STATE_READY")) {
    djinn.kill();
    throw new Error(
      `no agent is ready in the e2e djinn (${JSON.stringify(providers)}): its fake claude (agents.ts) is not found or its auth status fails`,
    );
  }

  const stop = async () => {
    stopping = true;
    if (process.platform === "win32") {
      // Windows sends another process no signal: djinn ends at once, as after a crash. A process it started may
      // still hold a file of its data directory a moment.
      djinn.kill();
      await exited;
      keepLogs(slot, home);
      fs.rmSync(home, {
        recursive: true,
        force: true,
        maxRetries: 20,
        retryDelay: 250,
      });
      return;
    }
    djinn.kill("SIGINT");
    const code = await exited;
    keepLogs(slot, home);
    fs.rmSync(home, { recursive: true, force: true });
    if (code !== 0)
      throw new Error(`djinn ${slot} exited with ${code} on SIGINT`);
  };
  return { slot, home, url, stop };
}

export default async function globalSetup(config: FullConfig) {
  delete process.env.DJINN_ADDR;
  // One djinn per worker, started together: a worker's specs share theirs, in the order Playwright gives them, and
  // two workers never touch the same one.
  const settled = await Promise.allSettled(
    Array.from({ length: config.workers }, (_, slot) => startOne(slot)),
  );
  const djinns = settled.flatMap((s) =>
    s.status === "fulfilled" ? [s.value] : [],
  );
  const failed = settled.flatMap((s) =>
    s.status === "rejected" ? [s.reason as Error] : [],
  );
  if (failed.length > 0) {
    await Promise.allSettled(djinns.map((d) => d.stop()));
    throw failed[0];
  }
  for (const d of djinns) {
    process.env[`DJINN_URL_${d.slot}`] = d.url;
    process.env[`DJINN_E2E_HOME_${d.slot}`] = d.home;
  }
  // The runner's own process (the reporters, a global teardown) sees the first one.
  process.env.DJINN_URL = djinns[0].url;
  process.env.DJINN_E2E_HOME = djinns[0].home;

  return async () => {
    const ends = await Promise.allSettled(djinns.map((d) => d.stop()));
    const errors = ends.flatMap((e) =>
      e.status === "rejected" ? [String((e.reason as Error).message)] : [],
    );
    if (errors.length > 0) throw new Error(errors.join("; "));
  };
}
