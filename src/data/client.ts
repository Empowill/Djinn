// The clients of the djinn server, generated from the protos. The page reads the services in their own shapes: no
// model of its own sits between them and the screens.
import {
  Code,
  ConnectError,
  type Transport,
  createClient,
} from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";

import {
  GateService,
  MachineService,
} from "../../gen/ts/machine/v1/machine_pb";
import {
  BlockService,
  MarkService,
  ProjectService,
  QuestionService,
  SkillService,
  TaskService,
  WishService,
} from "../../gen/ts/plan/v1/plan_pb";
import { UiService } from "../../gen/ts/ui/v1/ui_pb";

// djinnTransport reaches the djinn server at baseUrl in the binary Protobuf format: against JSON, it encodes and
// decodes about three times faster on both ends (docs/transport.md). fetch replaces the global one, for the tests.
export function djinnTransport(
  baseUrl: string,
  fetch?: typeof globalThis.fetch,
): Transport {
  return createConnectTransport({ baseUrl, useBinaryFormat: true, fetch });
}

export function createClients(transport: Transport) {
  return {
    wishes: createClient(WishService, transport),
    tasks: createClient(TaskService, transport),
    questions: createClient(QuestionService, transport),
    projects: createClient(ProjectService, transport),
    blocks: createClient(BlockService, transport),
    marks: createClient(MarkService, transport),
    skills: createClient(SkillService, transport),
    machine: createClient(MachineService, transport),
    gates: createClient(GateService, transport),
    ui: createClient(UiService, transport),
  };
}

export type Clients = ReturnType<typeof createClients>;

// message is what to show of an error: the server's own words, without the code Connect puts in front.
export function message(error: unknown): string {
  if (error instanceof ConnectError) return error.rawMessage;
  if (error instanceof Error) return error.message;
  return String(error);
}

// notFound tells an error that says the thing is gone, such as a task's events after djinn up restarted.
export function notFound(error: unknown): boolean {
  return error instanceof ConnectError && error.code === Code.NotFound;
}
