package lower_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/pts/diagnostic"
	"github.com/microsoft/typescript-go/pts/ir"
	"gotest.tools/v3/assert"
)

func TestCapturedReceiverCallerUnionAtTheSourceLimit(t *testing.T) {
	t.Parallel()
	for _, count := range []int{256, 257} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			t.Parallel()
			var source strings.Builder
			source.WriteString(`class Box<T> { constructor(public value: T) {} }
class C0 { tag = 0; }
interface Maker { make<T>(value: T): Box<T>; }
type Factory = <T>(value: T) => Box<T>;
class M0 implements Maker { make<T>(value: T): Box<T> { return new Box(value); } }
class M1 implements Maker { make<T>(value: T): Box<T> { return new Box(value); } }
class M2 implements Maker { make<T>(value: T): Box<T> { return new Box(value); } }
function read(receiver: Maker): Factory { return receiver.make; }
`)
			for i := 1; i < count; i++ {
				fmt.Fprintf(&source, "class R%d extends M%d {}\nread(new R%d());\n", i, i%2, i)
			}
			source.WriteString("const factory = read(new M0()); const box: Box<C0> = factory(new C0()); console.log(box.value.tag);\n")

			program, errors := lowerSource(t, source.String())

			assert.DeepEqual(t, errors, []diagnostic.Diagnostic(nil))
			for receiver := range 3 {
				name := fmt.Sprintf("M%d.make_C0", receiver)
				present := slices.ContainsFunc(program.Funcs, func(fn *ir.Func) bool { return fn.Name == name })
				required := receiver < 2 || count == 257
				assert.Check(t, present == required, "%s at %d sources: present=%t required=%t", name, count, present, required)
			}
		})
	}
}

func TestCapturedReceiverTablesShareAllRuntimeCapturesAtOneWrapperSite(t *testing.T) {
	t.Parallel()
	for _, origin := range []string{"closed_callers", "unknown_caller", "destructured"} {
		t.Run(origin, func(t *testing.T) {
			t.Parallel()
			body := "return receiver.make;"
			if origin == "destructured" {
				body = "const { make } = receiver; return make;"
			}
			calls := "use0(read(new M0())); use1(read(new M1()));"
			if origin == "unknown_caller" {
				calls += "const held: any[] = [new M2()]; use0(read(held[0]));"
			}
			program, errors := lowerSource(t, `class Box<T> { constructor(public value: T) {} }
class C0 { tag = 0; }
class C1 { tag = 1; }
interface Maker { make<T>(value: T): Box<T>; }
type Factory = <T>(value: T) => Box<T>;
class M0 implements Maker { make<T>(value: T): Box<T> { return new Box(value); } }
class M1 implements Maker { make<T>(value: T): Box<T> { return new Box(value); } }
class M2 implements Maker { make<T>(value: T): Box<T> { return new Box(value); } }
function read(receiver: Maker): Factory { `+body+` }
function use0(f: Factory): void { console.log(f(new C0()).value.tag); }
function use1(f: Factory): void { console.log(f(new C1()).value.tag); }
`+calls)

			assert.DeepEqual(t, errors, []diagnostic.Diagnostic(nil))
			var wrapper *ir.Func
			for _, fn := range program.Funcs {
				if fn.Name == "read.make" && fn.Shares == nil {
					assert.Assert(t, wrapper == nil, "one wrapper source represents every read invocation")
					wrapper = fn
				}
			}
			assert.Assert(t, wrapper != nil)
			assert.Equal(t, len(wrapper.Captures), 1)
			assert.Equal(t, len(wrapper.Instances), 2)
			for _, name := range []string{"C0", "C1"} {
				index := slices.IndexFunc(wrapper.Instances, func(instance *ir.Instance) bool {
					return len(instance.Signature.Params) == 1 && instance.Signature.Params[0].String() == name
				})
				assert.Assert(t, index >= 0, "the shared wrapper needs %s", name)
				instance := wrapper.Instances[index].Func
				assert.Assert(t, instance.Shares == wrapper)
				assert.Assert(t, slices.Equal(instance.Captures, wrapper.Captures), "forwarding instances use the actual receiver capture, not a proven class substituted for it")
			}
			for receiver := range 3 {
				for demand := range 2 {
					name := fmt.Sprintf("M%d.make_C%d", receiver, demand)
					present := slices.ContainsFunc(program.Funcs, func(fn *ir.Func) bool { return fn.Name == name })
					required := receiver < 2 || origin != "closed_callers"
					assert.Check(t, present == required, "%s: present=%t required=%t", name, present, required)
				}
			}
		})
	}
}
