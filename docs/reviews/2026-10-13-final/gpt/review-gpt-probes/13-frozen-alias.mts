const original = { name: "a", values: [1, 2] };
const alias: any = original;
Object.freeze(original);
try { alias.name = "b"; } catch (e) { console.log((e as Error).name); }
original.values.push(3);
console.log(original.name, original.values, Object.isFrozen(original), Object.isFrozen(original.values));
const map = new Map([["a", 1]]);
Object.freeze(map); map.set("b", 2); console.log([...map]);
const array = [1, 2]; Object.freeze(array);
try { array.splice(0, 1); } catch (e) { console.log((e as Error).name, array); }
