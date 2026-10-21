package driver_test

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/pts/driver"
	"github.com/microsoft/typescript-go/pts/lower"
	"gotest.tools/v3/assert"
)

func TestCapturedReceiverWrappersOnlyInstantiateTheirConstructedMethods(t *testing.T) {
	t.Parallel()
	for _, origin := range []string{
		"direct", "const_aliases", "closed_parameters", "returned_receiver", "returned_alias",
		"conditional", "merged_returns", "bind", "bind_chain", "nongeneric_adapter",
	} {
		t.Run(origin, func(t *testing.T) {
			t.Parallel()
			entry := writeSource(t, capturedReceiverFlowSource(origin))

			c, err := driver.EmitCWith(t.Context(), entry, lower.AllInstances)

			assert.NilError(t, err)
			merged := origin == "conditional" || origin == "merged_returns"
			assertCapturedReceiverPairs(t, c, func(receiver, demand int) bool {
				return receiver == demand || merged && receiver == 1 && demand == 0
			})
			if origin != "bind" && origin != "bind_chain" {
				for wrapper := range 3 {
					assertCapturedReceiverWrapperSignatures(t, c, fmt.Sprintf("read%d_make", wrapper), []int{wrapper})
				}
			}
		})
	}
}

func capturedReceiverFlowSource(origin string) string {
	var source strings.Builder
	source.WriteString(capturedReceiverPrelude())
	for i := range 3 {
		fmt.Fprintf(&source, "class C%d { tag = %d; }\n", i, i)
		fmt.Fprintf(&source, "class M%d implements Maker { make<T>(value: T): Box<T> { return new Box(value); } }\n", i)
		if origin == "nongeneric_adapter" {
			fmt.Fprintf(&source, "function use%d(f: (value: C%d) => Box<C%d>): void { console.log(f(new C%d()).value.tag); }\n", i, i, i, i)
		} else {
			fmt.Fprintf(&source, "function use%d(f: Factory): void { const box: Box<C%d> = f(new C%d()); console.log(box.value.tag); }\n", i, i, i)
		}
		switch origin {
		case "closed_parameters":
			fmt.Fprintf(&source, "function read%d(receiver: Maker): Factory { return receiver.make; }\n", i)
		case "returned_receiver", "returned_alias", "merged_returns":
			if origin == "merged_returns" && i == 0 {
				source.WriteString("function construct0(): Maker { if (choose()) return new M0(); return new M1(); }\n")
			} else {
				fmt.Fprintf(&source, "function construct%d(): Maker { return new M%d(); }\n", i, i)
			}
			if origin == "returned_alias" {
				fmt.Fprintf(&source, "const constructorAlias%d = construct%d; function read%d(): Factory { return constructorAlias%d().make; }\n", i, i, i, i)
			} else {
				fmt.Fprintf(&source, "function read%d(): Factory { return construct%d().make; }\n", i, i)
			}
		default:
			fmt.Fprintf(&source, "function read%d(): Factory { const receiver: Maker = new M%d(); ", i, i)
			switch origin {
			case "const_aliases":
				source.WriteString("const alias = receiver; const again = alias; return again.make;")
			case "conditional":
				if i == 0 {
					source.WriteString("const other: Maker = new M1(); return (choose() ? receiver : other).make;")
				} else {
					source.WriteString("return receiver.make;")
				}
			case "bind":
				source.WriteString("return receiver.make.bind(receiver);")
			case "bind_chain":
				source.WriteString("return receiver.make.bind(receiver).bind(undefined);")
			default:
				source.WriteString("return receiver.make;")
			}
			source.WriteString(" }\n")
		}
	}
	for i := range 3 {
		if origin == "closed_parameters" {
			fmt.Fprintf(&source, "use%d(read%d(new M%d()));\n", i, i, i)
		} else {
			fmt.Fprintf(&source, "use%d(read%d());\n", i, i)
		}
	}
	return source.String()
}

func capturedReceiverPrelude() string {
	return `class Box<T> { constructor(public value: T) {} }
interface Maker { make<T>(value: T): Box<T>; }
type Factory = <T>(value: T) => Box<T>;
function choose(): boolean { return true; }
`
}

func assertCapturedReceiverPairs(t *testing.T, c string, required func(receiver, demand int) bool) {
	t.Helper()
	pairs := 0
	for receiver := range 3 {
		for demand := range 3 {
			name := fmt.Sprintf("pts_fn_M%d_make_C%d(", receiver, demand)
			present := strings.Contains(c, name)
			if present {
				pairs++
			}
			assert.Check(t, present == required(receiver, demand), "%s: present=%t, required by all captured receiver sources=%t", name, present, required(receiver, demand))
			entry := "return " + name
			assert.Check(t, strings.Contains(c, entry) == required(receiver, demand), "%s must be registered in its class instance table exactly when required", name)
		}
	}
	t.Logf("3 captured receiver classes x 3 signatures: %d class-method pairs", pairs)
}

func assertCapturedReceiverWrapperSignatures(t *testing.T, c, wrapper string, required []int) {
	t.Helper()
	table := "pts_instances_closure_" + wrapper
	assert.Assert(t, strings.Contains(c, ".instances = "+table+","), "%s must register its forwarding descriptor", wrapper)
	entries := regexp.MustCompile(`(?m)^static [^\n]*\bpts_instance_closure_`+regexp.QuoteMeta(wrapper)+`_[0-9]+\(pts_closure \*pts_env, pts_class_C([0-9]+) \*a1\)`).FindAllStringSubmatch(c, -1)
	assert.Check(t, len(entries) == len(required), "%s: %d descriptor entries; want %d", wrapper, len(entries), len(required))
	for demand := range 3 {
		present, want := false, false
		for _, entry := range entries {
			present = present || entry[1] == fmt.Sprint(demand)
		}
		for _, signature := range required {
			want = want || signature == demand
		}
		assert.Check(t, present == want, "%s descriptor for C%d: present=%t, required=%t", wrapper, demand, present, want)
	}
}

func TestCapturedReceiverSharedWrapperSiteRepresentsEveryRuntimeCapture(t *testing.T) {
	t.Parallel()
	entry := writeSource(t, capturedReceiverPrelude()+`class C0 { tag = 0; }
class C1 { tag = 1; }
class C2 { tag = 2; }
class M0 implements Maker { make<T>(value: T): Box<T> { return new Box(value); } }
class M1 implements Maker { make<T>(value: T): Box<T> { return new Box(value); } }
class M2 implements Maker { make<T>(value: T): Box<T> { return new Box(value); } }
function read(receiver: Maker): Factory { return receiver.make; }
function separate(receiver: Maker): Factory { return receiver.make; }
function use0(f: Factory): void { const box: Box<C0> = f(new C0()); console.log(box.value.tag); }
function use1(f: Factory): void { const box: Box<C1> = f(new C1()); console.log(box.value.tag); }
function use2(f: Factory): void { const box: Box<C2> = f(new C2()); console.log(box.value.tag); }
const first = read(new M0());
const second = read(new M1());
use0(first);
use1(second);
use2(separate(new M2()));
`)

	c, err := driver.EmitCWith(t.Context(), entry, lower.AllInstances)

	assert.NilError(t, err)
	assertCapturedReceiverPairs(t, c, func(receiver, demand int) bool {
		return receiver < 2 && demand < 2 || receiver == 2 && demand == 2
	})
	assertCapturedReceiverWrapperSignatures(t, c, "read_make", []int{0, 1})
	assertCapturedReceiverWrapperSignatures(t, c, "separate_make", []int{2})
}

func TestCapturedReceiverBindUsesItsThisArgumentRatherThanTheMethodLookupObject(t *testing.T) {
	t.Parallel()
	entry := writeSource(t, capturedReceiverPrelude()+`class C0 { tag = 0; }
class Base { tag = "base"; make<T>(value: T): Box<T> { console.log(this.tag); return new Box(value); } }
class M0 extends Base { make<T>(value: T): Box<T> { console.log(this.tag); return new Box(value); } }
class M1 extends Base { make<T>(value: T): Box<T> { console.log(this.tag); return new Box(value); } }
class M2 extends Base { make<T>(value: T): Box<T> { console.log(this.tag); return new Box(value); } }
function read(original: Base, captured: Base): Factory { return original.make.bind(captured); }
const bound = read(new M0(), new M1());
const box: Box<C0> = bound(new C0());
console.log(box.value.tag);
`)

	c, err := driver.EmitCWith(t.Context(), entry, lower.AllInstances)

	assert.NilError(t, err)
	for receiver := range 3 {
		name := fmt.Sprintf("pts_fn_M%d_make_C0(", receiver)
		assert.Check(t, strings.Contains(c, name) == (receiver == 1), "%s: only bind's captured thisArg is the forwarded receiver", name)
	}
}

func TestCapturedReceiverUnknownSourcesKeepEveryCompatibleClassMethod(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name   string
		prefix string
		body   string
	}{
		{"any_readback", "", "const held: any[] = [new M1()]; use(read(held[0]));"},
		{"unknown_readback", "", "const held: unknown[] = [new M1()]; use(read(held[0] as Maker));"},
		{"mixed_known_unknown", "", "const held: any[] = [new M1()]; use(read(choose() ? new M0() : held[0]));"},
		{"mutable_binding", "", "let receiver: Maker = new M0(); receiver = new M1(); use(read(receiver));"},
		{"captured_assignment", "", "let receiver: Maker = new M0(); function change(): void { receiver = new M1(); } change(); use(read(receiver));"},
		{"escaped_wrapper_factory", "", "const alias = read; use(alias(new M1()));"},
		{"property_read", "", "const held = { receiver: new M1() }; use(read(held.receiver));"},
		{"asserted_receiver", "", "use(read(new M1() as Maker));"},
		{"generic_factory", "function construct<U>(marker: U): Maker { console.log(marker); return new M1(); }", "use(read(construct(1)));"},
		{"generic_constructor", "class Generic<U> implements Maker { constructor(public marker: U) {} make<T>(value: T): Box<T> { return new Box(value); } }", "use(read(new Generic(1)));"},
		{"constructor_replacement", "class Replacement extends M0 { constructor() { super(); return new M1(); } }", "use(read(new Replacement()));"},
		{"inherited_replacement", "class Replacement extends M0 { constructor() { super(); return new M1(); } } class Child extends Replacement {}", "use(read(new Child()));"},
		{"return_cycle", "function cycle(receiver: Maker, stop: boolean): Maker { if (stop) return receiver; return cycle(receiver, true); }", "use(read(cycle(new M1(), false)));"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			entry := writeSource(t, capturedReceiverPrelude()+`class C0 { tag = 0; }
class M0 implements Maker { make<T>(value: T): Box<T> { return new Box(value); } }
class M1 implements Maker { make<T>(value: T): Box<T> { return new Box(value); } }
class M2 implements Maker { make<T>(value: T): Box<T> { return new Box(value); } }
function read(receiver: Maker): Factory { return receiver.make; }
function use(f: Factory): void { const box: Box<C0> = f(new C0()); console.log(box.value.tag); }
`+tt.prefix+"\n"+tt.body+"\n")

			c, err := driver.EmitCWith(t.Context(), entry, lower.AllInstances)

			assert.NilError(t, err)
			for receiver := range 3 {
				name := fmt.Sprintf("pts_fn_M%d_make_C0(", receiver)
				assert.Check(t, strings.Contains(c, name), "%s remains for an uncertain captured receiver", name)
			}
		})
	}
}

func TestCapturedReceiverDestructuringRemainsConservative(t *testing.T) {
	t.Parallel()
	entry := writeSource(t, capturedReceiverPrelude()+`class C0 { tag = 0; }
class M0 implements Maker { make<T>(value: T): Box<T> { return new Box(value); } }
class M1 implements Maker { make<T>(value: T): Box<T> { return new Box(value); } }
class M2 implements Maker { make<T>(value: T): Box<T> { return new Box(value); } }
function read(receiver: Maker): Factory { const { make } = receiver; return make; }
const factory = read(new M1());
const box: Box<C0> = factory(new C0());
console.log(box.value.tag);
`)

	c, err := driver.EmitCWith(t.Context(), entry, lower.AllInstances)

	assert.NilError(t, err)
	for receiver := range 3 {
		name := fmt.Sprintf("pts_fn_M%d_make_C0(", receiver)
		assert.Check(t, strings.Contains(c, name), "%s remains: a synthetic pattern identifier is not the captured receiver expression", name)
	}
}

func TestCapturedReceiverGenericContextsKeepCompatibleMethods(t *testing.T) {
	t.Parallel()
	for _, context := range []string{"function", "class", "contextual_generic_return_origin"} {
		t.Run(context, func(t *testing.T) {
			t.Parallel()
			body := "const receiver: Maker = new M0(); console.log(marker); return receiver.make;"
			call := "const box: Box<C0> = factory(new C0()); console.log(box.value.tag);"
			var creation string
			switch context {
			case "function":
				creation = "function outer<U>(marker: U): Factory { " + body + " } const factory = outer(1);"
			case "class":
				creation = "class Outer<U> { constructor(public marker: U) {} read(): Factory { const marker = this.marker; " + body + " } } const factory = new Outer(1).read();"
			case "contextual_generic_return_origin":
				creation = "const outer: <U>(marker: U) => Maker = marker => { console.log(marker); return new M0(); }; const receiver = outer(1); const factory = receiver.make;"
			}
			entry := writeSource(t, capturedReceiverPrelude()+`class C0 { tag = 0; }
class M0 implements Maker { make<T>(value: T): Box<T> { return new Box(value); } }
class M1 implements Maker { make<T>(value: T): Box<T> { return new Box(value); } }
class M2 implements Maker { make<T>(value: T): Box<T> { return new Box(value); } }
`+creation+call)

			c, err := driver.EmitCWith(t.Context(), entry, lower.AllInstances)

			assert.NilError(t, err)
			for receiver := range 3 {
				name := fmt.Sprintf("pts_fn_M%d_make_C0(", receiver)
				assert.Check(t, strings.Contains(c, name), "%s remains: a generic receiver or wrapper context is not a per-instance source", name)
			}
		})
	}
}

func TestCapturedReceiverInheritedMethodsAndOverridesRemainAvailable(t *testing.T) {
	t.Parallel()
	entry := writeSource(t, capturedReceiverPrelude()+`class C0 { tag = 0; }
class C1 { tag = 1; }
class C2 { tag = 2; }
class Parent implements Maker { make<T>(value: T): Box<T> { return new Box(value); } }
class Inherited extends Parent {}
class Override extends Parent { make<T>(value: T): Box<T> { return new Box(value); } }
function read0(receiver: Maker): Factory { return receiver.make; }
function read1(receiver: Maker): Factory { return receiver.make; }
function read2(receiver: Maker): Factory { return receiver.make; }
const inherited = read0(new Inherited());
const overridden = read1(new Override());
const parent = read2(new Parent());
const first: Box<C0> = inherited(new C0());
const second: Box<C1> = overridden(new C1());
const third: Box<C2> = parent(new C2());
console.log(first.value.tag, second.value.tag, third.value.tag);
`)

	c, err := driver.EmitCWith(t.Context(), entry, lower.AllInstances)

	assert.NilError(t, err)
	for _, required := range []string{"Parent_make_C0", "Override_make_C1", "Parent_make_C2"} {
		assert.Check(t, strings.Contains(c, "return pts_fn_"+required+"("), "%s must remain registered for its captured receiver", required)
	}
	for _, excluded := range []string{"Override_make_C0", "Override_make_C2"} {
		assert.Check(t, !strings.Contains(c, "pts_fn_"+excluded+"("), "%s cannot be reached by these captures", excluded)
	}
}

func TestCapturedClassReceiversExcludeLiteralCandidatesOnlyWhenProven(t *testing.T) {
	t.Parallel()
	for _, property := range []string{"method", "forwarder"} {
		for _, unknown := range []bool{false, true} {
			name := fmt.Sprintf("%s/unknown=%t", property, unknown)
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				literal := "{ make<T>(value: T): Box<T> { return new Box(value); } }"
				target := "pts_fn_make_C0("
				if property == "forwarder" {
					literal, target = "{ make: source }", "pts_fn_source_C0("
				}
				receiver := "new Concrete()"
				if unknown {
					receiver = "held[0]"
				}
				entry := writeSource(t, capturedReceiverPrelude()+`class C0 { tag = 0; }
class Concrete implements Maker { make<T>(value: T): Box<T> { return new Box(value); } }
function source<T>(value: T): Box<T> { return new Box(value); }
const literal: Maker = `+literal+`;
const held: any[] = [literal];
function read(receiver: Maker): Factory { return receiver.make; }
const factory = read(`+receiver+`);
const box: Box<C0> = factory(new C0());
console.log(box.value.tag, held.length);
`)

				c, err := driver.EmitCWith(t.Context(), entry, lower.AllInstances)

				assert.NilError(t, err)
				assert.Check(t, strings.Contains(c, "pts_fn_Concrete_make_C0("), "the required or uncertain-compatible concrete method remains")
				assert.Check(t, strings.Contains(c, target) == unknown, "%s is reachable only when the captured receiver is unknown", target)
			})
		}
	}
}

func TestCapturedReceiverExhaustedProofsKeepCompatibleMethods(t *testing.T) {
	t.Parallel()
	for _, budget := range []string{"depth", "body_work"} {
		t.Run(budget, func(t *testing.T) {
			t.Parallel()
			var source strings.Builder
			source.WriteString(capturedReceiverPrelude())
			source.WriteString("class C0 { tag = 0; }\n")
			for receiver := range 3 {
				fmt.Fprintf(&source, "class M%d implements Maker { make<T>(value: T): Box<T> { return new Box(value); } }\n", receiver)
			}
			if budget == "depth" {
				source.WriteString("function read(): Factory { const r0: Maker = new M1();\n")
				for i := 1; i <= 140; i++ {
					fmt.Fprintf(&source, "const r%d = r%d;\n", i, i-1)
				}
				source.WriteString("return r140.make; }\n")
			} else {
				source.WriteString("function construct(): Maker { let n = 0;\n")
				source.WriteString(strings.Repeat("n++;\n", 1500))
				source.WriteString("return new M1(); }\nfunction read(): Factory { return construct().make; }\n")
			}
			source.WriteString("const factory = read(); const box: Box<C0> = factory(new C0()); console.log(box.value.tag);\n")
			entry := writeSource(t, source.String())

			c, err := driver.EmitCWith(t.Context(), entry, lower.AllInstances)

			assert.NilError(t, err)
			for receiver := range 3 {
				name := fmt.Sprintf("pts_fn_M%d_make_C0(", receiver)
				assert.Check(t, strings.Contains(c, name), "%s must remain when the captured-receiver proof exhausts %s", name, budget)
			}
		})
	}
}
