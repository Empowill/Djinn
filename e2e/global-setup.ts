// Starts `djinn up --browser` on a free port, waits until it answers, and hands its URL (with the token) to the
// specs through DJINN_URL. The teardown stops it with SIGINT and checks that it exits cleanly; on Windows it kills it.
import { spawn } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";

import { fakeAgents, withAgents } from "./agents";

const binary = path.resolve(
  __dirname,
  "../bin/djinn-e2e" + (process.platform === "win32" ? ".exe" : ""),
);

// Where the djinn's logs go before its data directory is removed: a push check's djinn that died mid-run left
// nothing to say why, every spec after it refused (W248).
const kept = path.resolve(__dirname, "../test-results/e2e/djinn-logs");

// keepLogs copies the djinn's logs (djinn.log, crash.txt, goroutine dumps) out of its data directory.
function keepLogs(home: string) {
  fs.cpSync(path.join(home, "logs"), kept, { recursive: true, force: true });
}

export default async function globalSetup() {
  delete process.env.DJINN_ADDR;
  // A data directory of its own: the tests never touch the developer's state nor the address of their djinn.
  const home = fs.mkdtempSync(path.join(os.tmpdir(), "djinn-e2e-"));
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
  // The teardown stops it: an end before that fails every spec after it, so it is said at once and kept. Said at the
  // teardown, the line would push a failed spec's name out of the last lines a push check keeps.
  let stopping = false;
  const exited = new Promise<number | null>((resolve) =>
    djinn.once("exit", (code, signal) => {
      const line = `djinn exited at ${new Date().toISOString()}: code ${code}, signal ${signal}\n`;
      fs.mkdirSync(kept, { recursive: true });
      fs.appendFileSync(path.join(kept, "exit.txt"), line);
      if (!stopping) process.stderr.write(line);
      resolve(code);
    }),
  );

  const url = await new Promise<string>((resolve, reject) => {
    const timer = setTimeout(
      () => reject(new Error("djinn printed no URL")),
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
    djinn.once("exit", (code) => reject(new Error(`djinn exited: ${code}`)));
  });

  const token = new URL(url).searchParams.get("token")!;
  const deadline = Date.now() + 10_000;
  for (;;) {
    const res = await fetch(new URL("/", url), {
      headers: { Authorization: `Bearer ${token}` },
    }).catch(() => undefined);
    if (res?.ok) break;
    if (Date.now() > deadline)
      throw new Error(`djinn does not answer on ${url}`);
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
  process.env.DJINN_URL = url;
  process.env.DJINN_E2E_HOME = home;

  return async () => {
    if (process.platform === "win32") {
      // Windows sends another process no signal: djinn ends at once, as after a crash. A process it started may
      // still hold a file of its data directory a moment.
      stopping = true;
      djinn.kill();
      await exited;
      keepLogs(home);
      fs.rmSync(home, {
        recursive: true,
        force: true,
        maxRetries: 20,
        retryDelay: 250,
      });
      return;
    }
    stopping = true;
    djinn.kill("SIGINT");
    const code = await exited;
    keepLogs(home);
    fs.rmSync(home, { recursive: true, force: true });
    if (code !== 0) throw new Error(`djinn exited with ${code} on SIGINT`);
  };
}
