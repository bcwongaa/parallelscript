// addEventListener's `signal` option as Node has it (H07b1): the signal's own listener goes in
// first, so that aborting a signal removes a listener added to that signal itself before it
// could run, and it goes in for a listener that is there already too, whose abort then removes
// it. That listener runs once, also for an untrusted "abort" event, and even after an earlier
// listener stopped the event's propagation. A signal aborted already adds nothing.
function log(...parts: unknown[]): void {
  console.log(parts.map(String).join(" "));
}

{
  const controller = new AbortController();
  controller.signal.addEventListener("abort", () => log("removed before it ran: no"), { signal: controller.signal });
  controller.signal.addEventListener("abort", () => log("a later listener runs"));
  controller.abort();
}

{
  const owner = new AbortController();
  const target = new EventTarget();
  const listener: EventListener = () => log("the listener ran");
  target.addEventListener("ping", listener);
  target.addEventListener("ping", listener, { signal: owner.signal });
  target.dispatchEvent(new Event("ping"));
  owner.abort();
  target.dispatchEvent(new Event("ping"));
  log("after the abort: the listener added twice is gone");
}

{
  const target = new EventTarget();
  target.addEventListener("ping", () => log("SHOULD NOT RUN: its signal had aborted"), { signal: AbortSignal.abort() });
  target.dispatchEvent(new Event("ping"));
  log("a signal aborted already added nothing");
}

{
  const owner = new AbortController();
  const target = new EventTarget();
  target.addEventListener("ping", () => log("once, until the signal aborts"), { once: true, signal: owner.signal });
  target.addEventListener("ping", () => log("every time, until the signal aborts"), { signal: owner.signal });
  target.dispatchEvent(new Event("ping"));
  target.dispatchEvent(new Event("ping"));
  owner.abort();
  target.dispatchEvent(new Event("ping"));
  log("once and every time: gone");
}

{
  const owner = new AbortController();
  const target = new EventTarget();
  owner.signal.addEventListener("abort", (event) => {
    log("an earlier abort listener stops the event");
    event.stopImmediatePropagation();
  });
  target.addEventListener("ping", () => log("SHOULD NOT RUN: still there"), { signal: owner.signal });
  owner.signal.addEventListener("abort", () => log("SHOULD NOT RUN: after the stop"));
  owner.abort();
  target.dispatchEvent(new Event("ping"));
  log("stopped, and removed all the same");
}

{
  const owner = new AbortController();
  const target = new EventTarget();
  const listener: EventListener = () => log("the listener added again ran");
  target.addEventListener("ping", listener, { signal: owner.signal });
  owner.signal.dispatchEvent(new Event("abort"));
  target.addEventListener("ping", listener);
  owner.abort();
  target.dispatchEvent(new Event("ping"));
  log("an untrusted abort event used the removal up");
}

// expect:
// a later listener runs
// the listener ran
// after the abort: the listener added twice is gone
// a signal aborted already added nothing
// once, until the signal aborts
// every time, until the signal aborts
// every time, until the signal aborts
// once and every time: gone
// an earlier abort listener stops the event
// stopped, and removed all the same
// the listener added again ran
// an untrusted abort event used the removal up
