class Box<T> {
  value: T;
  constructor(value: T) { this.value = value; }
}
function use(factory: <T>(value: T) => Box<T>): void {
  try { console.log("box", factory(9).value); }
  catch (error: any) { console.log(error.name, error.message); }
}
use(<T>(value: T) => new Box(value));
