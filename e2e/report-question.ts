// The shape of a real wish (Dew, 12/10/2026): a worker's report, and an open question whose long Markdown context
// says what happened and names the report, with a djinn command in an option. The screens test
// (tests/screens.test.mjs) and the e2e case (e2e/question-report.spec.ts) both read it.

export const REPORT_TITLE = "Payroll for acme: feasibility";

export const REPORT = `## What the study found
- **The probe exists:** TestCheckCredentials, with \`PAYROLL_CHECK=1 PAYROLL_CLIENT=acme\`. It reads the secret as the script does, without anyone opening it.
- **Today, one refusal of the payroll API fails the whole folder**, and in production no CSV of the client is written.
- **The refused call is not confirmed yet:** probably "job data", 13 fields in one request, the base salary among them.

## What the fix costs
A sentinel on the refusal, the calls going on, the refusal remembered per folder, the CSV header unchanged and its
cells empty. Two other scrapers of the repository already do the same.

## What is left to know
The exact step, the HTTP status, the message, and whether every employee is refused or only some.`;

export const QUESTION =
  "The payroll API is down: the workers of this wish need the right to run commands in the repository. Will you run the command?";

// The command of the first option, for the wish it is asked in.
export function allowCommand(wishId: string): string {
  return `djinn wish allow ${wishId} --mode auto`;
}

export function options(wishId: string): string[] {
  return [
    `Done: I ran ${allowCommand(wishId)}`,
    "No: I run the probe myself and paste the step, the HTTP status and the message in my answer",
  ];
}

// The first section, then the rest that the first view leaves out.
export const CONTEXT_FIRST = "Where we stand";
export const CONTEXT_LATER = "What starts with your answer";

export const CONTEXT = `## ${CONTEXT_FIRST}
- **Study W1: done on the code only.** The workers of this wish have no right in the repository: every command they tried (the probe, gcloud, djinn) was refused. Nothing was simulated, the repository is untouched.
- **Your decisions:** Q01 A (the workers run the probe), Q02 A+E (skip the refused call, empty columns), Q03 A (the last stored salary stays).
- **Why nothing runs:** Q01 A needs a command that grants this right; granting a right is your move. I said so in my terminal, not here: that was a mistake.

## The report in short (block "${REPORT_TITLE}" of this wish)
- **The probe exists:** TestCheckCredentials, \`PAYROLL_CHECK=1 PAYROLL_CLIENT=acme\`.
- **Today, one refusal fails the folder, and in production no CSV of the client is written.**
- **The refused call is not confirmed yet:** probably "job data", 13 fields in one request.
- **The implementation of A+E is small:** a sentinel on the refusal, the calls going on, empty cells.

## ${CONTEXT_LATER}
W2, the implementation, on the branch payroll-skip-forbidden (not main; Djinn asks you before pushing it):
1. run the probe on acme and note the real error, with no employee data in what is written;
2. code A+E;
3. a test that replays the recorded real error as it is.
With B, W2 starts from the error you paste.`;

export const RECOMMENDATION =
  "A: W2 can then run the probe and its tests on the real API itself, which the implementation needs anyway.";
