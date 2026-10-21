package driver_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/pts/driver"
	"github.com/microsoft/typescript-go/pts/lower"
	"gotest.tools/v3/assert"
)

func TestLiteralProjectionDemandsOnlyInstantiateTheirSelectedSources(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"function", "receiver", "captured_receiver"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			for _, origin := range []string{
				"fresh_object", "const_object", "const_key", "shorthand", "fresh_array", "const_array",
				"projected_alias", "selected_return", "projected_return", "selected_parameter", "conditional",
			} {
				t.Run(origin, func(t *testing.T) {
					t.Parallel()
					entry := writeSource(t, literalProjectionFlowSource(kind, origin))

					c, err := driver.EmitCWith(t.Context(), entry, lower.AllInstances)

					assert.NilError(t, err)
					pairs := 0
					for value := range 3 {
						for demand := range 3 {
							name := literalProjectionTarget(kind, value, demand)
							present := strings.Contains(c, name)
							if present {
								pairs++
							}
							required := value == demand || origin == "conditional" && value == 1 && demand == 0
							assert.Check(t, present == required, "%s: present=%t, selected source requires=%t", name, present, required)
							if kind != "function" {
								assert.Check(t, strings.Contains(c, "return "+name) == required, "%s must be registered exactly when required", name)
							}
						}
					}
					want := 3
					if origin == "conditional" {
						want++
					}
					t.Logf("3 source values x 3 demand signatures: %d target pairs; want %d", pairs, want)
					if kind == "captured_receiver" {
						for wrapper := range 3 {
							assertCapturedReceiverWrapperSignatures(t, c, fmt.Sprintf("read%d_make", wrapper), []int{wrapper})
						}
					}
				})
			}
		})
	}
}

func literalProjectionFlowSource(kind, origin string) string {
	var source strings.Builder
	source.WriteString(literalProjectionFlowPrelude())
	for i := range 3 {
		fmt.Fprintf(&source, "class C%d { tag = %d; }\n", i, i)
		fmt.Fprintf(&source, "function source%d<T>(value: T): Box<T> { return new Box(value); }\n", i)
		fmt.Fprintf(&source, "class M%d implements Maker { make<T>(value: T): Box<T> { return new Box(value); } }\n", i)
		if kind == "receiver" {
			fmt.Fprintf(&source, "function use%d(receiver: Maker): void { const box: Box<C%d> = receiver.make(new C%d()); console.log(box.value.tag); }\n", i, i, i)
		} else {
			fmt.Fprintf(&source, "function use%d(factory: Factory): void { const box: Box<C%d> = factory(new C%d()); console.log(box.value.tag); }\n", i, i, i)
		}
		if kind == "captured_receiver" {
			fmt.Fprintf(&source, "function read%d(receiver: Maker): Factory { return receiver.make; }\n", i)
		}
	}
	valueType := "Factory"
	if kind != "function" {
		valueType = "Maker"
	}
	for i := range 3 {
		value := fmt.Sprintf("source%d", i)
		other := fmt.Sprintf("source%d", (i+1)%3)
		if kind != "function" {
			value = fmt.Sprintf("new M%d()", i)
			other = fmt.Sprintf("new M%d()", (i+1)%3)
		}
		selected := value
		if origin == "conditional" && i == 0 {
			selected = "choose() ? " + value + " : " + other
		}
		expression := ""
		switch origin {
		case "fresh_object":
			expression = fmt.Sprintf("({ value: %s, other: %s }).value", value, other)
		case "fresh_array":
			expression = fmt.Sprintf("[%s, %s][0]", value, other)
		case "const_array":
			fmt.Fprintf(&source, "const held%d: %s[] = [%s, %s];\n", i, valueType, value, other)
			expression = fmt.Sprintf("held%d[0]", i)
		case "shorthand":
			fmt.Fprintf(&source, "const selected%d: %s = %s; const held%d = { selected%d };\n", i, valueType, value, i, i)
			expression = fmt.Sprintf("held%d.selected%d", i, i)
		case "selected_return":
			fmt.Fprintf(&source, "function select%d(): %s { return %s; } const held%d = { value: select%d() };\n", i, valueType, value, i, i)
			expression = fmt.Sprintf("held%d.value", i)
		case "projected_return":
			fmt.Fprintf(&source, "function select%d(): %s { const held = { value: %s, other: %s }; return held.value; }\n", i, valueType, value, other)
			expression = fmt.Sprintf("select%d()", i)
		case "selected_parameter":
			fmt.Fprintf(&source, "function select%d(value: %s): %s { const held = { value }; return held.value; }\n", i, valueType, valueType)
			expression = fmt.Sprintf("select%d(%s)", i, value)
		default:
			fmt.Fprintf(&source, "const held%d: { value: %s; other: %s } = { value: %s, other: %s };\n", i, valueType, valueType, selected, other)
			expression = fmt.Sprintf("held%d.value", i)
			if origin == "const_key" {
				expression = fmt.Sprintf("held%d[\"value\"]", i)
			}
			if origin == "projected_alias" {
				fmt.Fprintf(&source, "const selected%d = %s; const again%d = selected%d;\n", i, expression, i, i)
				expression = fmt.Sprintf("again%d", i)
			}
		}
		if kind == "captured_receiver" {
			expression = fmt.Sprintf("read%d(%s)", i, expression)
		}
		fmt.Fprintf(&source, "use%d(%s);\n", i, expression)
	}
	return source.String()
}

func TestLiteralProjectionUncertainOriginsKeepCompatibleTargets(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"function", "receiver", "captured_receiver"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			for _, tt := range []struct {
				name string
				body string
			}{
				{"property_write", "const held = { value: first }; held.value = second; USE(held.value);"},
				{"element_write", "const held: Value[] = [first]; held[0] = second; USE(held[0]);"},
				{"captured_write", "const held = { value: first }; function change(): void { held.value = second; } change(); USE(held.value);"},
				{"alias_write", "const held = { value: first }; const alias = held; alias.value = second; USE(held.value);"},
				{"passed_container", "function replace(held: { value: Value }): void { held.value = second; } const held = { value: first }; replace(held); USE(held.value);"},
				{"returned_container", "const held = { value: first }; function expose(): { value: Value } { return held; } expose().value = second; USE(held.value);"},
				{"stored_container", "const held = { value: first }; const stored = [held]; stored[0].value = second; USE(held.value);"},
				{"getter", "const held = { get value(): Value { return second; } }; USE(held.value);"},
				{"unselected_getter", "const held = { value: first, get other(): Value { return second; } }; USE(held.value);"},
				{"any_storage", "const held: any = { value: second }; const checked: Value = held.value; USE(checked);"},
				{"unknown_array", "const held: unknown[] = [second]; const checked = held[0] as Value; USE(checked);"},
				{"array_method", "const held: Value[] = [first, second]; held.reverse(); USE(held[0]);"},
				{"dynamic_key", "const held: Value[] = [first, second]; let key = 1; USE(held[key]);"},
			} {
				t.Run(tt.name, func(t *testing.T) {
					t.Parallel()
					var source strings.Builder
					source.WriteString(literalProjectionFlowPrelude())
					source.WriteString("class C0 { tag = 0; }\n")
					for i := range 3 {
						fmt.Fprintf(&source, "function source%d<T>(value: T): Box<T> { return new Box(value); }\n", i)
						fmt.Fprintf(&source, "class M%d implements Maker { make<T>(value: T): Box<T> { return new Box(value); } }\n", i)
					}
					use := "use"
					if kind == "function" {
						source.WriteString("type Value = Factory; const first: Value = source0; const second: Value = source1; const keep: any[] = [source0, source1, source2];\n")
					} else {
						source.WriteString("type Value = Maker; const first: Value = new M0(); const second: Value = new M1();\n")
					}
					if kind == "receiver" {
						source.WriteString("function use(receiver: Maker): void { const box: Box<C0> = receiver.make(new C0()); console.log(box.value.tag); }\n")
					} else {
						source.WriteString("function use(factory: Factory): void { const box: Box<C0> = factory(new C0()); console.log(box.value.tag); }\n")
					}
					if kind == "captured_receiver" {
						source.WriteString("function read(receiver: Maker): Factory { return receiver.make; }\nfunction apply(receiver: Maker): void { use(read(receiver)); }\n")
						use = "apply"
					}
					source.WriteString(strings.ReplaceAll(tt.body, "USE", use))
					entry := writeSource(t, source.String())

					c, err := driver.EmitCWith(t.Context(), entry, lower.AllInstances)

					assert.NilError(t, err)
					for value := range 3 {
						name := literalProjectionTarget(kind, value, 0)
						assert.Check(t, strings.Contains(c, name), "%s remains for an uncertain container origin", name)
					}
				})
			}
		})
	}
}

func literalProjectionFlowPrelude() string {
	return `class Box<T> { constructor(public value: T) {} }
interface Maker { make<T>(value: T): Box<T>; }
type Factory = <T>(value: T) => Box<T>;
function choose(): boolean { return true; }
`
}

func literalProjectionTarget(kind string, value, demand int) string {
	if kind == "function" {
		return fmt.Sprintf("pts_fn_source%d_C%d(", value, demand)
	}
	return fmt.Sprintf("pts_fn_M%d_make_C%d(", value, demand)
}
