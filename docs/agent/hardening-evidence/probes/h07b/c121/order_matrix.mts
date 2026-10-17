// C121 matrix: AbortSignal.any against the DOM's "create a dependent abort signal" and "signal abort".
function log(...parts: unknown[]): void {
  console.log(parts.map(String).join(" "));
}

// 1. Followers' reasons before the source's event; their events after it, in the order they were made.
{
  log("-- order");
  const a = new AbortController();
  a.signal.addEventListener("abort", () => log("a listener 1; d1", d1.aborted, "d2", d2.aborted));
  const d1 = AbortSignal.any([a.signal]);
  d1.addEventListener("abort", () => log("d1 listener; a", a.signal.aborted, "d2", d2.aborted));
  const d2 = AbortSignal.any([a.signal]);
  d2.addEventListener("abort", () => log("d2 listener"));
  a.signal.addEventListener("abort", () => log("a listener 2"));
  a.abort("r");
}

// 2. Flattening: a follower of a follower follows the first one's sources.
{
  log("-- flatten");
  const a = new AbortController();
  const inner = AbortSignal.any([a.signal]);
  const outer = AbortSignal.any([inner]);
  a.signal.addEventListener("abort", () => log("a listener; inner", inner.aborted, "outer", outer.aborted));
  inner.addEventListener("abort", () => log("inner listener; outer", outer.aborted));
  outer.addEventListener("abort", () => log("outer listener; reason", outer.reason));
  a.abort("flat");
}

// 3. A follower given with its own source, and duplicates: one event each.
{
  log("-- duplicates");
  const a = new AbortController();
  const inner = AbortSignal.any([a.signal]);
  const both = AbortSignal.any([inner, a.signal, inner]);
  const twice = AbortSignal.any([a.signal, a.signal]);
  let bothEvents = 0;
  let twiceEvents = 0;
  both.addEventListener("abort", () => bothEvents++);
  twice.addEventListener("abort", () => twiceEvents++);
  a.signal.addEventListener("abort", () => log("a listener; both", both.aborted, "twice", twice.aborted));
  a.abort("dup");
  log("both", both.reason, bothEvents, "twice", twice.reason, twiceEvents);
}

// 4. Two sources aborting one after the other: the first reason, one event.
{
  log("-- two sources");
  const a = new AbortController();
  const b = new AbortController();
  const d = AbortSignal.any([a.signal, b.signal]);
  let events = 0;
  d.addEventListener("abort", () => events++);
  b.abort("b first");
  a.abort("a second");
  log("d", d.reason, events, "a", a.signal.reason);
}

// 5. Already aborted signals given: the first aborted one's reason, at once; no event.
{
  log("-- already aborted");
  const a = new AbortController();
  const d = AbortSignal.any([a.signal]);
  a.abort("gone");
  const b = new AbortController();
  const again = AbortSignal.any([b.signal, d]);
  let events = 0;
  again.addEventListener("abort", () => events++);
  const first = AbortSignal.any([AbortSignal.abort("one"), AbortSignal.abort("two")]);
  log("again", again.aborted, again.reason, events, "first", first.reason);
  b.abort("later");
  log("after b", again.reason, events);
}

// 6. Reason identity, given and default.
{
  log("-- identity");
  const a = new AbortController();
  const reason = { why: "object" };
  const d = AbortSignal.any([a.signal]);
  const dd = AbortSignal.any([d]);
  a.abort(reason);
  log("given", d.reason === reason, dd.reason === reason);
  const b = new AbortController();
  const e = AbortSignal.any([b.signal]);
  b.abort();
  log("default", e.reason === b.signal.reason, e.reason instanceof DOMException, e.reason.name);
}

// 7. onabort among a follower's listeners: where it was first set.
{
  log("-- onabort");
  const a = new AbortController();
  const d = AbortSignal.any([a.signal]);
  d.addEventListener("abort", () => log("listener 1"));
  d.onabort = () => log("onabort first");
  d.addEventListener("abort", () => log("listener 2"));
  d.onabort = () => log("onabort replaced");
  a.signal.onabort = () => log("source onabort; d", d.aborted);
  a.abort();
}

// 9. Listeners that abort other signals during the dispatch.
{
  log("-- abort in dispatch");
  const a = new AbortController();
  const c = new AbortController();
  a.signal.addEventListener("abort", () => {
    log("a listener aborts c; e", e.aborted);
    c.abort("from a's listener");
  });
  const e = AbortSignal.any([c.signal, a.signal]);
  e.addEventListener("abort", () => log("e listener; reason", e.reason));
  c.signal.addEventListener("abort", () => log("c listener"));
  const f = AbortSignal.any([c.signal]);
  f.addEventListener("abort", () => log("f listener; reason", f.reason));
  a.abort("a's reason");
  log("e", e.reason, "f", f.reason);
}

// 9b. A follower's listener aborts the follower's other source.
{
  log("-- follower aborts");
  const a = new AbortController();
  const b = new AbortController();
  const d = AbortSignal.any([a.signal]);
  const g = AbortSignal.any([b.signal, a.signal]);
  d.addEventListener("abort", () => {
    log("d listener; g", g.aborted, g.reason);
    b.abort("b from d");
  });
  g.addEventListener("abort", () => log("g listener; reason", g.reason));
  b.signal.addEventListener("abort", () => log("b listener"));
  a.abort("a");
}

// 8. A timeout signal as a source.
{
  log("-- timeout");
  const t = AbortSignal.timeout(5);
  const d = AbortSignal.any([t]);
  t.addEventListener("abort", () => log("timeout listener; d", d.aborted));
  d.addEventListener("abort", () => log("d listener;", d.reason === t.reason, d.reason.name));
  await new Promise((resolve) => setTimeout(resolve, 50));
  log("done", d.aborted);
}
