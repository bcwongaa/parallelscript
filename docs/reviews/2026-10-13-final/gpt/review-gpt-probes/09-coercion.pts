const trace: string[] = [];
class Amount {
  valueOf(): any { trace.push("valueOf"); return 7; }
  toString(): any { trace.push("toString"); return "seven"; }
}
const amount: any = new Amount();
console.log(amount + 1, String(amount), Number(amount), `${amount}`, trace);
const odd: any = { valueOf() { return {}; }, toString() { return {}; } };
try { console.log(Number(odd)); } catch (e) { console.log((e as Error).name); }
console.log(+"", +"0x10", +"-0", Object.is(+"-0", -0));
