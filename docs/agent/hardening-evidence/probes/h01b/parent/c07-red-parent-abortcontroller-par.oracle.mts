// Sequential Node equivalent of c07-red-parent-abortcontroller-par.pts (range(0, n).par.map(f)
// becomes Array.from({ length: n }, (_, i) => f(i))).
const COUNT = 20_000;

// "ok", or what went wrong for controller i.
function constructAndAbort(i: number): string {
  try {
    const controller = new AbortController();
    controller.abort(i);
    return controller.signal.aborted && controller.signal.reason === i ? "ok" : "wrong state";
  } catch (error) {
    return error instanceof Error ? `${error.name}: ${error.message}` : "non-Error thrown";
  }
}

// How many times each outcome occurs, outcomes sorted.
function tally(outcomes: string[]): string[] {
  const counts = new Map<string, number>();
  for (const outcome of outcomes) {
    counts.set(outcome, (counts.get(outcome) ?? 0) + 1);
  }
  return [...counts.keys()].sort().map((outcome) => `${outcome}: ${counts.get(outcome)}`);
}

console.log(tally(Array.from({ length: COUNT }, (_, i) => constructAndAbort(i))).join("\n"));
