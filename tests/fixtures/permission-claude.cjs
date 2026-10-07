#!/usr/bin/env node
"use strict";
const args = process.argv.slice(2);
if (args.includes("--version")) {
  console.log("claude-permission-fixture 1");
  process.exit(0);
}
if (args[0] === "auth") {
  console.log('{"loggedIn":true}');
  process.exit(0);
}
const fs = require("node:fs");
const send = (value) => process.stdout.write(JSON.stringify(value) + "\n");
require("node:readline")
  .createInterface({ input: process.stdin })
  .on("line", (line) => {
    const message = JSON.parse(line);
    fs.appendFileSync(
      process.env.DJINN_CLAUDE_TRACE,
      JSON.stringify(message) + "\n",
    );
    if (message.type === "user")
      send({
        type: "control_request",
        request_id: "claude-native-request",
        request: {
          subtype: "can_use_tool",
          tool_name: "Read",
          input: { file_path: "/tmp/djinn-isolated-request.txt" },
          reason: "Test isolé Claude Code",
        },
      });
    if (message.type === "control_response") {
      send({
        type: "assistant",
        message: {
          content: [
            {
              type: "text",
              text: 'DJINN_EVENT:{"type":"step_result","data":{"status":"ready","summary":"Autorisation Claude traitée."}}',
            },
          ],
        },
      });
      send({
        type: "result",
        subtype: "success",
        is_error: false,
        result: "Permission resolved",
        usage: { input_tokens: 0, output_tokens: 0 },
      });
      // Keep reading like the native stream-json transport. Djinn must close
      // stdin after the result; otherwise this passage would remain alive.
    }
  });
