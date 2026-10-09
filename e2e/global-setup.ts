// Starts `djinn up --browser` on a free port, waits until it answers, and hands its URL (with the token) to the
// specs through DJINN_URL. The teardown stops it with SIGINT and checks that it exits cleanly; on Windows it kills it.
import { spawn } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";

const binary = path.resolve(
  __dirname,
  "../bin/djinn-e2e" + (process.platform === "win32" ? ".exe" : ""),
);

export default async function globalSetup() {
  // A data directory of its own: the tests never touch the developer's state nor the address of their djinn.
  const home = fs.mkdtempSync(path.join(os.tmpdir(), "djinn-e2e-"));
  // A fake claude and a fake agy (Antigravity), first on the PATH of djinn: the lead's terminal runs them, and they
  // only say how they were called.
  const bin = path.join(home, "bin");
  fs.mkdirSync(bin);
  if (process.platform !== "win32")
    for (const agent of ["claude", "agy"])
      fs.writeFileSync(
        path.join(bin, agent),
        `#!/bin/sh\necho "fake-${agent} $* in $(pwd)"\nexec cat\n`,
        { mode: 0o755 },
      );
  // The window's terminal opens in a folder of its own: without a project, djinn would ask for one instead.
  const shell = path.join(home, "shell");
  fs.mkdirSync(shell);
  const djinn = spawn(binary, ["up", "--browser", "--terminal-dir", shell], {
    stdio: ["ignore", "pipe", "inherit"],
    // The terminal of the window runs a plain POSIX shell, whatever the developer's own shell is.
    env: {
      ...process.env,
      DJINN_HOME: home,
      PATH: bin + path.delimiter + process.env.PATH,
      ...(process.platform === "win32" ? {} : { SHELL: "/bin/sh" }),
    },
  });
  const exited = new Promise<number | null>((resolve) =>
    djinn.once("exit", (code) => resolve(code)),
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
  process.env.DJINN_URL = url;
  process.env.DJINN_E2E_HOME = home;

  return async () => {
    if (process.platform === "win32") {
      // Windows sends another process no signal: djinn ends at once, as after a crash. A process it started may
      // still hold a file of its data directory a moment.
      djinn.kill();
      await exited;
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
    fs.rmSync(home, { recursive: true, force: true });
    if (code !== 0) throw new Error(`djinn exited with ${code} on SIGINT`);
  };
}
