// Recursive Fibonacci: calls and number arithmetic. Node and Bun run this file too.
function fib(n: number): number {
  return n < 2 ? n : fib(n - 1) + fib(n - 2);
}

console.log(fib(38));
