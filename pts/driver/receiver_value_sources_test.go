package driver_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/pts/driver"
	"github.com/microsoft/typescript-go/pts/lower"
	"gotest.tools/v3/assert"
)

func TestConstructedReceiverDemandsOnlyInstantiateTheirProvenMethods(t *testing.T) {
	t.Parallel()
	for _, origin := range []string{
		"direct", "const_aliases", "closed_parameters", "conditional", "logical", "comma",
		"returned", "returned_aliases", "merged_returns", "shared_callers",
	} {
		t.Run(origin, func(t *testing.T) {
			t.Parallel()
			entry := writeSource(t, constructedReceiverFlowSource(origin))

			c, err := driver.EmitCWith(t.Context(), entry, lower.AllInstances)

			assert.NilError(t, err)
			assertConstructedReceiverPairs(t, c, origin)
		})
	}
}

func TestConstructedReceiverCallFormsPreserveRequiredMethodPairs(t *testing.T) {
	t.Parallel()
	for _, form := range []string{"element", "optional_receiver", "optional_call", "dispatched"} {
		t.Run(form, func(t *testing.T) {
			t.Parallel()
			source := constructedReceiverFlowSource("direct")
			switch form {
			case "element":
				source = strings.ReplaceAll(source, "receiver.make(", "receiver[\"make\"](")
			case "optional_receiver":
				source = strings.ReplaceAll(source, "receiver.make(", "receiver?.make(")
			case "optional_call":
				source = strings.ReplaceAll(source, "receiver.make(", "receiver.make?.(")
			case "dispatched":
				source = strings.ReplaceAll(source, "interface Maker { make<T>(value: T): Box<T>; }", "class Maker { make<T>(value: T): Box<T> { return new Box(value); } }")
				source = strings.ReplaceAll(source, "implements Maker", "extends Maker")
			}
			entry := writeSource(t, source)

			c, err := driver.EmitCWith(t.Context(), entry, lower.AllInstances)

			assert.NilError(t, err)
			assertConstructedReceiverPairs(t, c, "direct")
		})
	}
}

func constructedReceiverFlowSource(origin string) string {
	var source strings.Builder
	source.WriteString(`class Box<T> { constructor(public value: T) {} }
interface Maker { make<T>(value: T): Box<T>; }
function choose(): boolean { return true; }
`)
	for i := range 3 {
		fmt.Fprintf(&source, "class C%d { tag = %d; }\n", i, i)
		fmt.Fprintf(&source, "class M%d implements Maker { make<T>(value: T): Box<T> { return new Box(value); } }\n", i)
		fmt.Fprintf(&source, "function use%d(receiver: Maker): void { const box: Box<C%d> = receiver.make(new C%d()); console.log(box.value.tag); }\n", i, i, i)
		if origin == "closed_parameters" {
			fmt.Fprintf(&source, "function relay%d(receiver: Maker): void { use%d(receiver); }\n", i, i)
		}
		if origin == "returned" || origin == "returned_aliases" || origin == "merged_returns" {
			if origin == "merged_returns" && i == 0 {
				source.WriteString("function construct0(): Maker { if (choose()) return new M0(); return new M1(); }\n")
			} else {
				fmt.Fprintf(&source, "function construct%d(): Maker { return new M%d(); }\n", i, i)
			}
		}
	}
	source.WriteString("function main(): void {\n")
	for i := range 3 {
		fmt.Fprintf(&source, "const receiver%d: Maker = new M%d();\n", i, i)
	}
	if origin == "conditional" || origin == "logical" {
		source.WriteString("const flag = choose(); const maybe: Maker | undefined = flag ? receiver0 : undefined;\n")
	}
	for i := range 3 {
		expression := fmt.Sprintf("new M%d()", i)
		switch origin {
		case "const_aliases":
			fmt.Fprintf(&source, "const alias%d = receiver%d; const again%d = alias%d;\n", i, i, i, i)
			expression = fmt.Sprintf("again%d", i)
		case "conditional":
			if i == 0 {
				expression = "flag ? receiver0 : receiver1"
			}
		case "logical":
			if i == 0 {
				expression = "maybe || receiver1"
			}
		case "comma":
			expression = fmt.Sprintf("(console.log(\"comma\"), receiver%d)", i)
		case "returned", "merged_returns":
			expression = fmt.Sprintf("construct%d()", i)
		case "returned_aliases":
			fmt.Fprintf(&source, "const constructAlias%d = construct%d;\n", i, i)
			expression = fmt.Sprintf("constructAlias%d()", i)
		}
		if origin == "closed_parameters" {
			fmt.Fprintf(&source, "relay%d(%s);\n", i, expression)
		} else {
			fmt.Fprintf(&source, "use%d(%s);\n", i, expression)
		}
	}
	if origin == "shared_callers" {
		source.WriteString("use0(new M1());\n")
	}
	source.WriteString("}\nmain();\n")
	return source.String()
}

func assertConstructedReceiverPairs(t *testing.T, c, origin string) {
	t.Helper()
	merged := origin == "conditional" || origin == "logical" || origin == "merged_returns" || origin == "shared_callers"
	pairs := 0
	for receiver := range 3 {
		for demand := range 3 {
			name := fmt.Sprintf("pts_fn_M%d_make_C%d(", receiver, demand)
			present := strings.Contains(c, name)
			if present {
				pairs++
			}
			required := receiver == demand || merged && receiver == 1 && demand == 0
			assert.Check(t, present == required, "%s: present=%t, required by constructed receiver sources=%t", name, present, required)
		}
	}
	want := 3
	if merged {
		want++
	}
	t.Logf("3 constructed receiver classes x 3 demand signatures: %d target pairs; want %d required pairs", pairs, want)
}

func TestUnknownReceiverOriginsKeepEveryCompatibleMethod(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name   string
		prefix string
		body   string
	}{
		{"any_readback", "", "const held: any[] = [new M1()]; use(held[0]);"},
		{"unknown_readback", "", "const held: unknown[] = [new M1()]; use(held[0] as Maker);"},
		{"mixed_known_unknown", "", "const held: any[] = [new M1()]; use(choose() ? new M0() : held[0]);"},
		{"assigned_binding", "", "let chosen: Maker = new M0(); chosen = new M1(); use(chosen);"},
		{"captured_assignment", "", "let chosen: Maker = new M0(); function change(): void { chosen = new M1(); } change(); use(chosen);"},
		{"escaped_consumer", "", "const alias = use; alias(new M1());"},
		{"mutable_property_read", "", "let held = { receiver: new M1() }; use(held.receiver);"},
		{"generic_factory", "function construct<U>(marker: U): Maker { console.log(marker); return new M1(); }", "use(construct(1));"},
		{"contextual_generic_factory", "const construct: <U>(marker: U) => Maker = marker => { console.log(marker); return new M1(); };", "use(construct(1));"},
		{"generic_constructor", "class Generic<U> implements Maker { constructor(public marker: U) {} make<T>(value: T): Box<T> { return new Box(value); } }", "use(new Generic(1));"},
		{"structural_any_impostor", "", "const held: any[] = [{ make<T>(value: T): Box<T> { return new Box(value); } }]; const checked: Maker = held[0]; use(checked);"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			entry := writeSource(t, `class Box<T> { constructor(public value: T) {} }
class C0 { tag = 0; }
interface Maker { make<T>(value: T): Box<T>; }
function choose(): boolean { return false; }
class M0 implements Maker { make<T>(value: T): Box<T> { return new Box(value); } }
class M1 implements Maker { make<T>(value: T): Box<T> { return new Box(value); } }
class M2 implements Maker { make<T>(value: T): Box<T> { return new Box(value); } }
function use(receiver: Maker): void { const box: Box<C0> = receiver.make(new C0()); console.log(box.value.tag); }
`+tt.prefix+"\n"+tt.body+"\n")

			c, err := driver.EmitCWith(t.Context(), entry, lower.AllInstances)

			assert.NilError(t, err)
			for receiver := range 3 {
				name := fmt.Sprintf("pts_fn_M%d_make_C0(", receiver)
				assert.Check(t, strings.Contains(c, name), "%s must remain for an unproven receiver source", name)
			}
		})
	}
}

func TestGenericReceiverContextsKeepCompatibleMethodTargets(t *testing.T) {
	t.Parallel()
	for _, context := range []string{"function", "class", "contextual_function"} {
		t.Run(context, func(t *testing.T) {
			t.Parallel()
			body := `const receiver: Maker = new M0(); const box: Box<C0> = receiver.make(new C0()); console.log(box.value.tag, marker);`
			var use string
			switch context {
			case "function":
				use = "function outer<U>(marker: U): void { " + body + " } outer(1); outer(\"s\");"
			case "class":
				use = "class Outer<U> { constructor(public marker: U) {} run(): void { const marker = this.marker; " + body + " } } new Outer(1).run(); new Outer(\"s\").run();"
			case "contextual_function":
				use = "const outer: <U>(marker: U) => void = marker => { " + body + " }; outer(1); outer(\"s\");"
			}
			entry := writeSource(t, `class Box<T> { constructor(public value: T) {} }
class C0 { tag = 0; }
interface Maker { make<T>(value: T): Box<T>; }
class M0 implements Maker { make<T>(value: T): Box<T> { return new Box(value); } }
class M1 implements Maker { make<T>(value: T): Box<T> { return new Box(value); } }
class M2 implements Maker { make<T>(value: T): Box<T> { return new Box(value); } }
`+use+"\n")

			c, err := driver.EmitCWith(t.Context(), entry, lower.AllInstances)

			assert.NilError(t, err)
			for receiver := range 3 {
				name := fmt.Sprintf("pts_fn_M%d_make_C0(", receiver)
				assert.Check(t, strings.Contains(c, name), "%s remains: shared generic contexts are outside the exact receiver proof", name)
			}
		})
	}
}

func TestConstructedReceiverInheritanceKeepsInheritedAndOverriddenImplementations(t *testing.T) {
	t.Parallel()
	entry := writeSource(t, `class Box<T> { constructor(public value: T) {} }
class C0 { tag = 0; }
class C1 { tag = 1; }
class C2 { tag = 2; }
interface Maker { make<T>(value: T): Box<T>; }
class Parent implements Maker { make<T>(value: T): Box<T> { return new Box(value); } }
class Inherited extends Parent {}
class Override extends Parent { make<T>(value: T): Box<T> { return new Box(value); } }
function use0(receiver: Maker): void { const box: Box<C0> = receiver.make(new C0()); console.log(box.value.tag); }
function use1(receiver: Maker): void { const box: Box<C1> = receiver.make(new C1()); console.log(box.value.tag); }
function use2(receiver: Maker): void { const box: Box<C2> = receiver.make(new C2()); console.log(box.value.tag); }
use0(new Inherited());
use1(new Override());
use2(new Parent());
`)

	c, err := driver.EmitCWith(t.Context(), entry, lower.AllInstances)

	assert.NilError(t, err)
	for _, required := range []string{"Parent_make_C0", "Override_make_C1", "Parent_make_C2"} {
		assert.Check(t, strings.Contains(c, "pts_fn_"+required+"("), "%s is the concrete receiver's required implementation", required)
	}
	for _, excluded := range []string{"Override_make_C0", "Override_make_C2"} {
		assert.Check(t, !strings.Contains(c, "pts_fn_"+excluded+"("), "%s belongs only to a subclass that cannot be this receiver", excluded)
	}
}
