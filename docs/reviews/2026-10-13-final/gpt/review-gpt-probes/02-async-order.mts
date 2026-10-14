const trace: string[] = [];
async function work(): Promise<void> {
  trace.push("start");
  await 1;
  trace.push("await");
  await Promise.resolve().then(() => trace.push("inner"));
  trace.push("done");
}
const result = work();
Promise.resolve().then(() => trace.push("then"));
queueMicrotask(() => trace.push("queued"));
await result;
trace.push("main");
console.log(trace.join(","));
const settled = await Promise.allSettled([Promise.resolve(1), Promise.reject("bad")]);
console.log(JSON.stringify(settled));
