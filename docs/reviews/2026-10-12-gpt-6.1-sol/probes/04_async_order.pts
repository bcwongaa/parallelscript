const trace: string[] = [];
async function work(): Promise<number> {
    trace.push("start");
    try {
        await Promise.resolve();
        trace.push("resume");
        throw new Error("work");
    } finally {
        trace.push("finally");
    }
}
const result = work().catch((error: Error) => { trace.push(error.message); return 7; });
Promise.resolve().then(() => trace.push("then"));
queueMicrotask(() => trace.push("micro"));
trace.push("sync");
console.log(await result);
await Promise.resolve();
console.log(trace.join(","));
