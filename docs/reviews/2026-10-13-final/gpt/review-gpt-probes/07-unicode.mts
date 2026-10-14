const text = "A😀é\ud800Z\udc00";
console.log(text.length, JSON.stringify(text), text.isWellFormed(), JSON.stringify(text.toWellFormed()));
console.log(Array.from(text).map((s) => s.codePointAt(0)));
console.log(Array.from({ length: text.length }, (_, i) => text.charCodeAt(i)));
console.log(JSON.stringify(text.slice(1, 2)), JSON.stringify(text.slice(2, 3)));
console.log("é".normalize(), "Å".normalize("NFKD"), "straße".toUpperCase());
console.log("a😀b".replace(/./gu, (m) => `[${m}]`));
console.log("a😀b".split(/(?:)/u));
