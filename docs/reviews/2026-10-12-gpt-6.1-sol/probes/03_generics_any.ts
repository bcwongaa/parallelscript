function identity<T>(value: T): T { return value; }
console.log(identity<any>({ count: 7 }).count);
const data: any = JSON.parse('{"count":8}');
const count: number = data.count;
console.log(count, data.missing === undefined);
class Box<T> {
    value: T;
    constructor(value: T) { this.value = value; }
}
function make<T>(value: T): Box<T> { return new Box(value); }
function exercise(factory: <T>(value: T) => Box<T>): void {
    try {
        const box: Box<number> = factory(9);
        console.log("box", box.value);
    } catch (error: any) {
        console.log("generic", error.name, error.message);
    }
}
exercise(make);
try {
    const wrong: any = "text";
    const number: number = wrong;
    console.log("boundary", number);
} catch (error: any) {
    console.log("boundary", error.name);
}
