package driver_test

import (
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/pts/driver"
	"github.com/microsoft/typescript-go/pts/lower"
	"gotest.tools/v3/assert"
)

func TestConstructedClassReceiversExcludeUnreachableLiteralMethodCandidates(t *testing.T) {
	t.Parallel()
	for _, property := range []string{"method", "forwarder"} {
		for _, unknown := range []bool{false, true} {
			name := property + "/known"
			if unknown {
				name = property + "/unknown"
			}
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				literal := "{ make<T>(value: T): Box<T> { return new Box(value); } }"
				target := "pts_fn_make_C0("
				if property == "forwarder" {
					literal = "{ make: source }"
					target = "pts_fn_source_C0("
				}
				argument := "new Concrete()"
				if unknown {
					argument = "held[0]"
				}
				entry := writeSource(t, `class Box<T> { constructor(public value: T) {} }
class C0 { tag = 0; }
interface Maker { make<T>(value: T): Box<T>; }
class Concrete implements Maker { make<T>(value: T): Box<T> { return new Box(value); } }
function source<T>(value: T): Box<T> { return new Box(value); }
function use(receiver: Maker): void { const box: Box<C0> = receiver.make(new C0()); console.log(box.value.tag); }
const literal: Maker = `+literal+`;
const held: any[] = [literal];
use(`+argument+`);
console.log(held.length);
`)

				c, err := driver.EmitCWith(t.Context(), entry, lower.AllInstances)

				assert.NilError(t, err)
				assert.Check(t, strings.Contains(c, "pts_fn_Concrete_make_C0("), "the required or unknown-compatible class target remains")
				present := strings.Contains(c, target)
				assert.Check(t, present == unknown, "%s: present=%t; only unknown receiver flow can reach this literal candidate", target, present)
			})
		}
	}
}
