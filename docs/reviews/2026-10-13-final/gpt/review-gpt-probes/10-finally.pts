const trace: string[] = [];
async function finish(): Promise<number> {
  try { await Promise.resolve(); return 1; }
  finally { await Promise.resolve(); trace.push("finally"); return 2; }
}
console.log(await finish(), trace);
function* gen(): Generator<number, string> {
  try { yield 1; yield 2; return "end"; }
  finally { trace.push("close"); }
}
for (const x of gen()) { console.log(x); break; }
for (let i = 0; i < 4; i++) {
  try { if (i % 2 === 0) continue; if (i === 3) break; trace.push("body"); }
  finally { trace.push(String(i)); }
}
console.log(trace);
try { await (async () => { try { throw "first"; } finally { await 0; throw "second"; } })(); }
catch (e) { console.log(e); }
