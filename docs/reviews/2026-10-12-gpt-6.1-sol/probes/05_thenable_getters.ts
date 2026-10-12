let reads = 0;
const changing: any = {
    get then(): (resolve: (value: number) => void) => void {
        reads++;
        const value = reads;
        return (resolve: (value: number) => void): void => { resolve(value); };
    }
};
console.log("changing", await Promise.resolve(changing), reads);
const throwing: any = {
    get then(): any { throw new Error("getter"); }
};
try {
    const promise = Promise.resolve(throwing);
    console.log("returned");
    await promise;
} catch (error: any) {
    console.log("caught", error.message);
}
const promise = new Promise(resolve => resolve(throwing));
console.log("executor", await Promise.race([
    promise.then(() => "fulfilled", () => "rejected"),
    new Promise<string>(resolve => setTimeout(() => resolve("pending"), 30))
]));
