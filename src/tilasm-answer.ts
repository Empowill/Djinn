// Whether a discussion waits for the document agent, as Djinn decides it (plan.AwaitsAgent): open, and nothing
// answered the developer's last message, neither a message by someone else after it nor a proposal linked to the
// discussion made after it, pending, accepted or rejected.
import type { TilasmProposal, TilasmThread } from "../gen/ts/plan/v1/tilasm_pb";

type Stamp = { seconds: bigint; nanos: number } | undefined;

// The order of two stamps: negative when a comes first.
const compare = (a: Stamp, b: Stamp) => {
  const as = a?.seconds ?? 0n;
  const bs = b?.seconds ?? 0n;
  if (as !== bs) return as < bs ? -1 : 1;
  return (a?.nanos ?? 0) - (b?.nanos ?? 0);
};

// waitsForAnswer tells whether the agent owes the developer an answer in thread; proposals may hold any proposals:
// only the discussion's count.
export function waitsForAnswer(
  thread: TilasmThread,
  proposals: TilasmProposal[],
): boolean {
  const last = thread.messages.at(-1);
  if (thread.resolved || !last || last.author !== "developer") return false;
  return !proposals.some(
    (p) =>
      p.threadId === thread.id && compare(p.createTime, last.createTime) >= 0,
  );
}
