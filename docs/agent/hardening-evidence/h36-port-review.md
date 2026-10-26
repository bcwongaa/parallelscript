# H36 Mac-port review: acceptance BLOCKED

2026-10-26. Named `reviewer` `a69a16dcad71f01b6`, inherited `gpt-6.1-sol`, isolated worktree at base `f0a3cf56d` plus recovered port. This records the child's delivered partial findings and the provider termination; it is **not a completed independent review or READY verdict**. The earlier [H36 review 1](h36-review1.md) reviewed D-227's numeric work, not the Mac port.

## Provider termination

The agent terminated before final acceptance with this exact error:

```
API Error: 400 This content was flagged for possible cybersecurity risk. If this seems wrong, try rephrasing your request. If you’re doing authorized security work that requires more cyber permissive safeguards, apply for Daybreak access via https://platform.openai.com/settings/organization/status-and-access before retrying. (error type unknown, HTTP 400, model sent to the API: gpt-6.1-sol)
```

Per the owner's instruction, this assignment is not retried, rephrased, reassigned to another model/provider, or treated as completed. H36 remains unchecked, with independent acceptance blocked. H13 is the next independent task; this session only checkpoints H36's implementation and already-verified fixes.

## Partial findings delivered before termination

### F1 / C373: an early handoff drops a connection before the listener's startup message

`http.c`'s `pts_http_listen` activates listener 0, then posts each other listener's startup separately. A worker already started can accept a connection and post `adopt` to a later loop before that loop's startup has even been posted. The recovered `adopt` closes the accepted descriptor when `l->tcp == NULL`, although the server is not stopped and all connection-handling state is initialized.

The child executed a controlled **Linux surrogate compiling the actual Apple-specific `http.c` branches**, using the real runtime scheduler, queues, collector, libuv and TCP connections. Only the posting seam forces a legal pause before the last startup post. It delivered:

```
startup: exit 1
mode=startup before: listening=0 stopped=0; after: connection_kept=0 duplicate_open=0
stopped: exit 0
mode=stopped before: listening=0 stopped=1; after: connection_kept=0 duplicate_open=0
```

This is execution evidence for the queue-order defect, **not native Mac execution**. Parent retains the source as a permanent regression and independently executes it before/after the fix; parent results are in [the recovery evidence](h36-resume.md).

The child also inspected lifetime: a successfully registered server and its listeners are `GC_MALLOC_UNCOLLECTABLE` and retained; the queued envelope's interior listener pointer cannot outlive that storage. Post-stop adoption must close the descriptor. `start_listening` only sets `l->tcp`; it does not overwrite the identity or connection list, so pre-listen adoption has no missing initialization dependency.

### F2 / C372: unsynchronized, incoherent memory counters

The new runtime path calls `GC_get_heap_size` and `GC_get_free_bytes`, which vendored `gc.h:726–740` explicitly identifies as unsynchronized and requiring the allocator lock to avoid multiprocessor data races. The library samples them on either side of RSS measurement. Even individually locked getters would still permit free bytes from a later, larger heap to exceed the earlier heap total.

The documented `GC_get_heap_usage_safe` returns both coherently under one lock. The independent reproducer designed a deterministic interposition at the RSS call using real `GC_expand_hp`; parent reproduced `heapTotal=4259840`, `heapUsed=-12533760` before fixing it. The parent uses a single runtime snapshot and records RED/GREEN separately.

## Limits

There is no final whole-port verdict, no post-fix independent acceptance, and no assertion that the remaining review scope was completed. Static observations and already-delivered reproductions survive; they do not substitute for the blocked gate. C367's reduced connection-burst coverage and retained overload reproducer remain explicitly documented under H14 rather than being called fixed.
