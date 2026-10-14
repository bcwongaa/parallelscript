class Recursive {
    get value(): number { return this.value + 1; }
}
try { console.log(new Recursive().value); }
catch (error: any) { console.log(error.name); }
