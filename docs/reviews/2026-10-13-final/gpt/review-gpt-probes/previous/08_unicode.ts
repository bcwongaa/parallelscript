const text = "A😀é\ud800Z";
console.log(text.length, text.charCodeAt(1), text.charCodeAt(2), text.codePointAt(1));
console.log(JSON.stringify(text.slice(1, 3)), JSON.stringify(text[1]));
console.log(text.isWellFormed(), JSON.stringify(text.toWellFormed()));
console.log(Array.from(text).map(value => value.length).join(","));
console.log("ΟΣ".toLowerCase(), "straße".toUpperCase());
console.log(JSON.stringify("\ud83d" + "\ude00"));
