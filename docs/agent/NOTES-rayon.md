# Study notes: Rayon's design, for the M5.3 fork-join pool

What to copy from Rayon (`rayon-core` plus `rayon::iter::plumbing`) when we build `pts/runtime`'s pool in C11. Sources: the rayon-core `join`, `registry`, `latch` and `sleep` modules; Lê, Pop, Cohen and Zappa Nardelli, *Correct and Efficient Work-Stealing for Weak Memory Models* (PPoPP 2013); Chase and Lev, *Dynamic Circular Work-Stealing Deque* (SPAA 2005).

## 1. Shape of the pool (the "registry")

- N worker threads (N = core count, or `PTS_THREADS`), separate from the event-loop threads (D-L16).
- Each worker owns one **Chase–Lev deque**. The owner pushes and pops at the *bottom* (LIFO, cache-hot); thieves steal from the *top* (FIFO, oldest and therefore biggest pieces of work).
- One global **injector** queue (a plain MPMC queue, mutex-protected is fine) for work submitted from threads that aren't workers.
- Worker main loop: pop own deque → else steal from other workers (start at a random victim, go round) → else pop the injector → else spin briefly, then sleep.
- Every worker thread must be registered with bdwgc (create it with `GC_pthread_create` or call `GC_register_my_thread`), because jobs and their captures live on worker stacks and in the GC heap.

## 2. `join(a, b)`

The whole design hangs off this one primitive.

```
join(a, b):
  job_b = StackJob(b)            // lives in this C stack frame; no allocation
  push(my_deque, &job_b)         // advertise b for stealing
  ra = a()                       // run a inline, right now
  if pop(my_deque) == &job_b:    // nobody stole b: the common case
      rb = b()                   // run it inline too, no synchronisation at all
  else:                          // b was stolen
      wait_until(job_b.latch)    // ... but help while waiting (§4)
      rb = job_b.result
  return (ra, rb)
```

- The job can live on the stack because `join` never returns before `b` finishes. This also makes `join` allocation-free.
- **Exceptions:** if `a` throws, still wait for `b` to finish (it points into our frame), then rethrow. If `b` threw on another thread, its job stores the error and `join` rethrows it on the caller. If both throw, rethrow `a`'s.
- `join_context` passes each closure a `migrated` flag: true when it runs on a different thread from the one that called `join`. Adaptive splitting needs it (§5).
- **Called from outside the pool** (for us: an event-loop thread doing `.par`): Rayon injects the whole operation into the injector and blocks the caller on a mutex/condvar latch. The brief instead wants the starting thread to *help* (D-L16). Plan: give each loop thread a deque slot too, used only while it is inside a parallel operation, so it runs the root of the computation itself, pushes splits that idle workers can steal, and helps while waiting exactly like a worker. Thieves scan worker deques plus active loop-thread deques.

## 3. The Chase–Lev deque (weak-memory version, Lê et al.)

A growable circular array plus two indices, `top` (thieves) and `bottom` (owner).

```
push(x):                         // owner only
  b = bottom (relaxed); t = top (acquire); a = array (relaxed)
  if b - t > a.size - 1: a = grow(a, b, t)
  a[b % size] = x (relaxed)
  fence(release)
  bottom = b + 1 (relaxed)

pop():                           // owner only
  b = bottom - 1; a = array; bottom = b (relaxed)
  fence(seq_cst)
  t = top (relaxed)
  if t <= b:
     x = a[b % size]
     if t == b:                  // last element: race with thieves
        if !CAS(top, t, t+1, seq_cst): x = EMPTY
        bottom = b + 1
     return x
  bottom = b + 1; return EMPTY

steal():                         // any thread
  t = top (acquire); fence(seq_cst); b = bottom (acquire)
  if t < b:
     x = array(consume/acquire)[t % size]
     if !CAS(top, t, t+1, seq_cst): return RETRY
     return x
  return EMPTY
```

- Elements are pointers to jobs, so a slot read is one word (fits the race-safety invariant, D-L13).
- **Growing:** copy into a buffer twice the size and publish it. A slow thief may still read the old buffer, so it can't be freed immediately. Rayon (via crossbeam) uses epoch reclamation; we can allocate deque buffers with bdwgc and let the collector free them once no thread references them.
- Test it alone first: single-owner push/pop, many thieves, under TSan. bdwgc may need TSan suppressions.

## 4. Helping while waiting

`wait_until(latch)` on a worker doesn't block. It loops:
1. if the latch is set, return;
2. pop own deque, or steal (from others, then the injector), and run what it gets;
3. if there is nothing to run, spin a bit, then sleep until either the latch is set or new work appears.

Latches in Rayon:
- **SpinLatch:** for worker-to-worker joins. Its states are `UNSET → SLEEPY → SLEEPING → SET`. Whoever sets it wakes the specific owning worker if that worker went to sleep.
- **LockLatch:** a mutex and condvar, for external threads that really block.
- **CountLatch:** a counter, for `scope` and `spawn`; set when it reaches zero.

**Sleeping without lost wake-ups:** Rayon keeps a global "jobs event" counter and a count of sleeping threads. A worker that finds no work spins for about 32 rounds (yielding), then announces that it is sleepy, re-checks for work, and only then sleeps on its own condvar. Pushing a job bumps the counter and wakes one sleeper if any thread is sleeping. Start simpler (spin, then a futex or condvar with a generation counter), but keep the "re-check after announcing" step, or wake-ups get lost.

## 5. Adaptive splitting (`rayon::iter::plumbing::bridge`)

Parallel iterators are a producer (something that can `split_at(mid)`) and a consumer (folds items and reduces two results into one). The recursion:

```
helper(len, migrated, splitter, producer, consumer):
  if len/2 >= splitter.min_len && splitter.try_split(migrated):
     (lp, rp) = producer.split_at(len/2); (lc, rc, reducer) = consumer.split_at(len/2)
     (l, r) = join_context(ctx -> helper(len/2,     ctx.migrated, splitter, lp, lc),
                           ctx -> helper(len-len/2, ctx.migrated, splitter, rp, rc))
     return reducer(l, r)
  return producer.fold_sequentially(consumer)
```

```
try_split(stolen):
  if stolen: splits = max(num_threads, splits / 2); return true   // work moved: idle threads exist, split more
  if splits > 0: splits /= 2; return true
  return false
```

- `splits` starts at `num_threads`, so an operation where nothing is stolen makes about `num_threads` leaves (log2 levels): very little overhead.
- When a piece **is** stolen, there are idle threads around, so the thief resets its budget and keeps splitting. Load balances automatically, without a fixed chunk size.
- `with_min_len` and `with_max_len` bound the leaf size. For cheap per-item callbacks a minimum leaf (say 1–4K items) avoids join overhead; we can pick one per method later from benchmarks.

## 6. The `.par` methods on top of this (M5.4)

- **`map` (order-preserving):** preallocate the output array of length n; every leaf writes its own disjoint index range (Rayon's `CollectConsumer`). No reduce step beyond checking that every leaf finished.
- **`filter` and `flatMap`:** each leaf builds a local chunk; reducing concatenates (or links chunks and concatenates once at the end). Order is preserved because `reducer(l, r)` keeps left before right.
- **`reduce(fn, init)`:** fold each leaf from `init`, then combine with `fn`. Document that `fn` must be associative and that `init` must be an identity.
- **`sum`, `count`, `min`, `max`:** special cases of reduce.
- **`some`, `every`, `find`:** a shared "found" flag (atomic) that leaves check between items, for short-circuiting. `find` returns *any* match, as the brief says.
- **`sort`/`sortBy`:** parallel merge sort, as in Rayon's `par_sort` (sort leaves with a stable sequential sort, then merge in parallel by splitting the larger run at its midpoint and binary-searching the split point in the other run).
- **`parallel for`:** `forEach` with a shared cancel flag. The first error is stored with a CAS, later leaves see the flag and stop, and the stored error is rethrown after the root `join` returns (D-L16).
- **`PTS_THREADS=1`:** skip the pool and run sequentially on the caller (D-O06).
