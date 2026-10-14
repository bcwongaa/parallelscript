class Completed implements Iterable<number> {
  [Symbol.iterator](): any {
    return {
      next(): IteratorResult<number> {
        return { get done(): true { console.log("done"); return true; },
          get value(): number { console.log("value"); throw new Error("unneeded value"); } };
      }
    };
  }
}
try { for (const value of new Completed()) console.log(value); }
catch (error: any) { console.log(error.message); }
