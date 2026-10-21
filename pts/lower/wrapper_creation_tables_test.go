package lower_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/pts/diagnostic"
	"github.com/microsoft/typescript-go/pts/ir"
	"gotest.tools/v3/assert"
)

func TestWrapperCreationTablesKeepEveryRequiredSignatureAndExcludeOtherSignatures(t *testing.T) {
	t.Parallel()
	for _, receiver := range []string{"class", "interface"} {
		t.Run(receiver, func(t *testing.T) {
			t.Parallel()
			var source strings.Builder
			source.WriteString(`class Box<T> { constructor(public value: T) {} }
type Factory = <T>(value: T) => Box<T>;
interface Maker { make<T>(value: T): Box<T>; }
`)
			for i := range 3 {
				fmt.Fprintf(&source, "class C%d { tag = %d; }\n", i, i)
				fmt.Fprintf(&source, "class M%d implements Maker { make<T>(value: T): Box<T> { return new Box(value); } }\n", i)
				fmt.Fprintf(&source, "function use%d(f: Factory): void { const box: Box<C%d> = f(new C%d()); console.log(box.value.tag); }\n", i, i, i)
				receiverType := fmt.Sprintf("M%d", i)
				if receiver == "interface" {
					receiverType = "Maker"
				}
				fmt.Fprintf(&source, "function read%d(receiver: %s): Factory { return receiver.make; }\n", i, receiverType)
			}
			for i := range 3 {
				fmt.Fprintf(&source, "use%d(read%d(new M%d()));\n", i, i, i)
			}

			program, errors := lowerSource(t, source.String())

			assert.DeepEqual(t, errors, []diagnostic.Diagnostic(nil))
			wrappers := 0
			for value := range 3 {
				name := fmt.Sprintf("read%d.make", value)
				var wrapper *ir.Func
				for _, fn := range program.Funcs {
					if fn.Name == name {
						assert.Assert(t, wrapper == nil, "one wrapper creation per source site")
						wrapper = fn
					}
				}
				assert.Assert(t, wrapper != nil, "%s must retain its erased wrapper", name)
				wrappers++
				for demand := range 3 {
					want := fmt.Sprintf("C%d", demand)
					present := false
					for _, instance := range wrapper.Instances {
						if len(instance.Signature.Params) == 1 && instance.Signature.Params[0].String() == want {
							present = true
							assert.Assert(t, instance.Func.Shares == wrapper, "the required signature shares the actual wrapper's captures")
						}
					}
					assert.Check(t, present == (value == demand), "%s for %s: present=%t, required=%t", name, want, present, value == demand)
				}
				assert.Check(t, len(wrapper.Instances) == 1, "%s must have exactly its required signature, not %d", name, len(wrapper.Instances))
			}
			assert.Equal(t, wrappers, 3)
			if receiver == "interface" {
				for value := range 3 {
					for demand := range 3 {
						name := fmt.Sprintf("M%d.make_C%d", value, demand)
						present := false
						for _, fn := range program.Funcs {
							present = present || fn.Name == name
						}
						assert.Check(t, present, "%s stays: wrapper identity does not prove a receiver class", name)
					}
				}
			}
		})
	}
}

func TestUnusedMethodWrapperCreationsHaveNoInstanceTable(t *testing.T) {
	t.Parallel()
	program, errors := lowerSource(t, `class Box<T> { constructor(public value: T) {} }
class C0 { tag = 0; }
type Factory = <T>(value: T) => Box<T>;
class Maker { make<T>(value: T): Box<T> { return new Box(value); } }
function unused0(): Factory { return new Maker().make; }
function unused1(): Factory { return new Maker().make; }
function known(): Factory { return new Maker().make; }
const held: any[] = [unused0(), unused1()];
function use(f: Factory): void { const box: Box<C0> = f(new C0()); console.log(box.value.tag); }
use(known());
console.log(held.length);
`)

	assert.DeepEqual(t, errors, []diagnostic.Diagnostic(nil))
	found := 0
	for _, fn := range program.Funcs {
		if fn.Name == "unused0.make" || fn.Name == "unused1.make" || fn.Name == "known.make" {
			found++
			want := 0
			if fn.Name == "known.make" {
				want = 1
			}
			assert.Check(t, len(fn.Instances) == want, "%s: %d descriptor signatures; want %d", fn.Name, len(fn.Instances), want)
		}
	}
	assert.Equal(t, found, 3)
}

func TestWrappersInSharedGenericCodeKeepEveryCompatibleSignature(t *testing.T) {
	t.Parallel()
	program, errors := lowerSource(t, `class Box<T> { constructor(public value: T) {} }
class C0 { tag = 0; }
class C1 { tag = 1; }
type Factory = <T>(value: T) => Box<T>;
const source = <T,>(value: T): Box<T> => new Box(value);
function outer<U>(marker: U): Factory { console.log(marker); return source.bind(undefined); }
function use0(f: Factory): void { const box: Box<C0> = f(new C0()); console.log(box.value.tag); }
function use1(f: Factory): void { const box: Box<C1> = f(new C1()); console.log(box.value.tag); }
const first = outer(1);
const second = outer("s");
use0(first);
use1(second);
`)

	assert.DeepEqual(t, errors, []diagnostic.Diagnostic(nil))
	wrappers := 0
	for _, fn := range program.Funcs {
		if fn.JSName == "bound" && strings.HasPrefix(fn.Name, "outer_") && fn.Shares == nil {
			wrappers++
			for _, want := range []string{"C0", "C1"} {
				present := false
				for _, instance := range fn.Instances {
					present = present || len(instance.Signature.Params) == 1 && instance.Signature.Params[0].String() == want
				}
				assert.Check(t, present, "%s needs %s: shared generic AST sites are not per-instance origins", fn.Name, want)
			}
		}
	}
	assert.Equal(t, wrappers, 2)
}
