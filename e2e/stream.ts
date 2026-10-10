// Reads demo.v1.DemoService.Count and shows each value as it arrives: the value moves only if the transport
// delivers the server stream message by message, not buffered until the end.
import { createClient } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import { DemoService } from "../gen/ts/demo/v1/demo_pb";

const value = document.getElementById("value")!;
const status = document.getElementById("status")!;

// A relative base URL: the page is served by djinn, over http:// in a browser or wails:// in the window.
const client = createClient(
  DemoService,
  createConnectTransport({ baseUrl: "/" }),
);

async function run() {
  try {
    status.textContent = "streaming";
    for await (const res of client.count({ upTo: 600 })) {
      value.textContent = String(res.value);
    }
    status.textContent = "done";
  } catch (err) {
    status.textContent = `error: ${String(err)}`;
  }
}

void run();
