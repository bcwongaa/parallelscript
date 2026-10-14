let value = 1;
function* numbers(): Generator<number, number, number> {
    return value + (yield 0);
}
const iterator = numbers();
console.log(iterator.next());
value = 2;
console.log(iterator.next(10));
let calls = 0;
const object = { get amount(): number { calls++; return calls; } };
function* properties(): Generator<number, number, number> {
    return object.amount + (yield 0);
}
const propertiesIterator = properties();
console.log(propertiesIterator.next(), calls);
console.log(propertiesIterator.next(10), calls);
