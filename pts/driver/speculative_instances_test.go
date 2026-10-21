package driver_test

// H09b5's RED test for C172(c): specialize pairs every value demand with every erased value of
// its erased function type (D-148, D-190), so code grows with escaping values x demand types.

import (
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/pts/driver"
	"gotest.tools/v3/assert"
)

func TestValuesThatNoTypedCallReachesGetNoInstances(t *testing.T) {
	t.Skip("known issue (H09b5c, C172): specialize pairs every value demand with every erased value of its erased function type")
	t.Parallel()
	// a0, a1 and a2 escape into an any[] that nothing reads back, so no typed call can reach
	// them; use's two demands (Box<C0>, Box<C1>) reach only the arrow passed to it. The base
	// made an instance of each escaping arrow for each demand type: 2 x 3 that no call uses
	// (review round 2's gen_escape.py; 150 arrows x 50 types made 92,388 lines of C).
	entry := writeSource(t, `class Box<T> {
  constructor(public value: T) {}
}
class C0 {
  tag = 0;
}
class C1 {
  tag = 1;
}
let total = 0;
const keep: any[] = [];
function use(f: <T>(x: T) => Box<T>): void {
  const b0: Box<C0> = f(new C0());
  total += b0.value.tag;
  const b1: Box<C1> = f(new C1());
  total += b1.value.tag;
}
function main(): void {
  const a0 = <T>(x: T): Box<T> => new Box(x);
  keep.push(a0);
  const a1 = <T>(x: T): Box<T> => new Box(x);
  keep.push(a1);
  const a2 = <T>(x: T): Box<T> => new Box(x);
  keep.push(a2);
  use(<T>(x: T): Box<T> => new Box(x));
}
main();
console.log(total, keep.length);
`)

	c, err := driver.EmitC(t.Context(), entry)

	assert.NilError(t, err)
	assert.Assert(t, strings.Contains(c, "pts_fn_main_arrow_C0") && strings.Contains(c, "pts_fn_main_arrow_C1"), "the arrow passed to use has its instances")
	for _, unused := range []string{"pts_fn_main_a0_C0", "pts_fn_main_a1_C1", "pts_fn_main_a2_C0"} {
		assert.Assert(t, !strings.Contains(c, unused), "%s is an instance no call can use", unused)
	}
}
