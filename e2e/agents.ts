// The fake agents every djinn of the e2e runs with. A CI runner has no agent installed: with none ready, the setup of
// the agents opens by itself over the window and takes the clicks of every spec. A fake claude first on its PATH, whose
// `claude auth status` exits 0, makes an agent ready, as on a developer's machine, and keeps the developer's real
// agents out of the specs. The fakes only say how they were called; the lead's terminal runs them.
import fs from "node:fs";
import path from "node:path";

// fakeAgents writes a fake claude and a fake agy (Antigravity) in bin. When claude runs a driven batch (-p with
// DJINN_LEAD=1), it reads one message and writes one stream-json turn so no model is called; in a terminal or a
// worker, the fakes say how they were called and keep their input open.
export function fakeAgents(bin: string) {
  fs.mkdirSync(bin, { recursive: true });
  for (const agent of ["claude", "agy"])
    if (process.platform === "win32")
      fs.writeFileSync(
        path.join(bin, `${agent}.cmd`),
        agent === "claude"
          ? `@if "%~1"=="-p" if "%DJINN_LEAD%"=="1" (\r\n  @echo {"type":"assistant","session_id":"019d0000-0000-7000-8000-000000000099","message":{"model":"claude-opus-4-7","content":[{"type":"text","text":"Handled the batch."}]}}\r\n  @echo {"type":"result","subtype":"success","session_id":"019d0000-0000-7000-8000-000000000099","total_cost_usd":0.01}\r\n  @exit /b 0\r\n)\r\n@echo fake-${agent} %* in %CD%\r\n`
          : `@echo fake-${agent} %* in %CD%\r\n`,
      );
    else
      fs.writeFileSync(
        path.join(bin, agent),
        `#!/bin/sh\nargs=""\nheadless=0\nwhile [ $# -gt 0 ]; do\n  case "$1" in\n    --allowedTools|--project) shift 2 ;;\n    -p) headless=1; shift ;;\n    *) args="\${args:+$args }$1"; shift ;;\n  esac\ndone\nif [ "$headless" = "1" ] && [ "$DJINN_LEAD" = "1" ]; then\n  read -r _ || true\n  echo '{"type":"assistant","session_id":"019d0000-0000-7000-8000-000000000099","message":{"model":"claude-opus-4-7","content":[{"type":"text","text":"Handled the batch."}]}}'\n  echo '{"type":"result","subtype":"success","session_id":"019d0000-0000-7000-8000-000000000099","total_cost_usd":0.01}'\n  exit 0\nfi\necho "fake-${agent} $args in $(pwd)"\nexec cat\n`,
        { mode: 0o755 },
      );
}

// agentsBin is the folder of the fake agents global-setup.ts writes.
export const agentsBin = () => path.join(process.env.DJINN_E2E_HOME!, "bin");

// withAgents is env with bin first on its PATH. Windows names it Path, and two names would leave djinn either one.
export function withAgents(
  env: NodeJS.ProcessEnv,
  bin = agentsBin(),
): NodeJS.ProcessEnv {
  const out = { ...env };
  let current = "";
  for (const name of Object.keys(out))
    if (name.toUpperCase() === "PATH") {
      current = out[name] ?? "";
      delete out[name];
    }
  out.PATH = bin + path.delimiter + current;
  return out;
}
