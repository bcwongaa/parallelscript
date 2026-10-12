class Completed implements Iterable<number> {
    [Symbol.iterator](): any {
        return {
            next(): any {
                return { get done(): true { console.log("done"); return true; },
                    get value(): number { console.log("value"); throw new Error("unneeded value"); } };
            }
        };
    }
}
try { for (const value of new Completed()) console.log(value); }
catch (error: any) { console.log(error.message); }
class Closing implements Iterable<number> {
    [Symbol.iterator](): any {
        return {
            next(): any { return { done: false, value: 1 }; },
            return(): IteratorResult<number> { throw new Error("close"); }
        };
    }
}
try { for (const value of new Closing()) throw new Error("body"); }
catch (error: any) { console.log("caught", error.message); }
