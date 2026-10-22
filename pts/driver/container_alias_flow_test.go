package driver_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/pts/driver"
	"github.com/microsoft/typescript-go/pts/lower"
	"gotest.tools/v3/assert"
)

func TestContainerAliasDemandsOnlyInstantiateTheirSelectedSources(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"function", "receiver", "captured_receiver", "wrapper"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			for _, origin := range []string{"object_chain", "array_chain", "parenthesized", "fanout", "local_selected_return", "selected_parameter", "conditional"} {
				t.Run(origin, func(t *testing.T) {
					t.Parallel()
					entry := writeSource(t, containerAliasFlowSource(kind, origin))

					c, err := driver.EmitCWith(t.Context(), entry, lower.AllInstances)

					assert.NilError(t, err)
					pairs := 0
					for value := range 3 {
						for demand := range 3 {
							name := literalProjectionTarget(kind, value, demand)
							present := strings.Contains(c, name)
							required := value == demand || origin == "conditional" && value == 1 && demand == 0
							if present {
								pairs++
							}
							assert.Check(t, present == required, "%s: present=%t, actual alias source requires=%t", name, present, required)
							if kind != "function" {
								assert.Check(t, strings.Contains(c, "return "+name) == required, "%s must be registered exactly when required", name)
							}
						}
					}
					want := 3
					if origin == "conditional" {
						want++
					}
					t.Logf("3 alias-held source values x 3 signatures: %d target pairs; want %d", pairs, want)
					if kind == "captured_receiver" || kind == "wrapper" {
						for wrapper := range 3 {
							required := []int{wrapper}
							if kind == "wrapper" && origin == "conditional" && wrapper == 1 {
								required = []int{0, 1}
							}
							assertCapturedReceiverWrapperSignatures(t, c, fmt.Sprintf("read%d_make", wrapper), required)
						}
					}
				})
			}
		})
	}
}

func containerAliasFlowSource(kind, origin string) string {
	var source strings.Builder
	source.WriteString(literalProjectionFlowPrelude())
	valueType := "Factory"
	if kind == "receiver" || kind == "captured_receiver" {
		valueType = "Maker"
	}
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
		} else if kind == "wrapper" {
			fmt.Fprintf(&source, "function read%d(): Factory { const receiver: Maker = new M%d(); return receiver.make; }\n", i, i)
		}
	}
	for i := range 3 {
		value := fmt.Sprintf("source%d", i)
		other := fmt.Sprintf("source%d", (i+1)%3)
		if valueType == "Maker" {
			value = fmt.Sprintf("new M%d()", i)
			other = fmt.Sprintf("new M%d()", (i+1)%3)
		} else if kind == "wrapper" {
			value = fmt.Sprintf("read%d()", i)
			other = fmt.Sprintf("read%d()", (i+1)%3)
		}
		selected := value
		if origin == "conditional" && i == 0 {
			selected = "choose() ? " + value + " : " + other
		}
		expression := fmt.Sprintf("other%d.value", i)
		switch origin {
		case "array_chain":
			fmt.Fprintf(&source, "const root%d: %s[] = [%s, %s]; const alias%d = root%d; const other%d = alias%d;\n", i, valueType, value, other, i, i, i, i)
			expression = fmt.Sprintf("other%d[0]", i)
		case "parenthesized":
			fmt.Fprintf(&source, "const root%d = ({ value: %s, other: %s }); const alias%d = (root%d); const other%d = ((alias%d));\n", i, value, other, i, i, i, i)
		case "fanout":
			fmt.Fprintf(&source, "const root%d = { value: %s, other: %s }; const alias%d = root%d; const sibling%d = root%d; const other%d = alias%d; const ignored%d = sibling%d.other;\n", i, value, other, i, i, i, i, i, i, i, i)
		case "local_selected_return":
			fmt.Fprintf(&source, "function select%d(): %s { const root = { value: %s, other: %s }; const alias = root; const other = alias; return other.value; }\n", i, valueType, value, other)
			expression = fmt.Sprintf("select%d()", i)
		case "selected_parameter":
			fmt.Fprintf(&source, "function select%d(value: %s): %s { const root = { value }; const alias = root; const other = alias; return other.value; }\n", i, valueType, valueType)
			expression = fmt.Sprintf("select%d(%s)", i, value)
		default:
			fmt.Fprintf(&source, "const root%d: { value: %s; other: %s } = { value: %s, other: %s }; const alias%d = root%d; const other%d = alias%d;\n", i, valueType, valueType, selected, other, i, i, i, i)
		}
		if kind == "captured_receiver" {
			expression = fmt.Sprintf("read%d(%s)", i, expression)
		}
		fmt.Fprintf(&source, "use%d(%s);\n", i, expression)
	}
	return source.String()
}

func TestContainerAliasSharedSitesJoinEveryCaller(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"function", "receiver", "captured_receiver"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
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
			}
			valueType, resultType, selected := "Factory", "Factory", "other.value"
			if kind != "function" {
				valueType = "Maker"
				if kind == "receiver" {
					resultType = "Maker"
				} else {
					selected += ".make"
				}
			}
			for _, name := range []string{"shared", "separate"} {
				fmt.Fprintf(&source, "function %s(value: %s): %s { const root = { value }; const alias = root; const other = alias; return %s; }\n", name, valueType, resultType, selected)
			}
			for i := range 3 {
				name := "shared"
				if i == 2 {
					name = "separate"
				}
				value := fmt.Sprintf("source%d", i)
				if kind != "function" {
					value = fmt.Sprintf("new M%d()", i)
				}
				fmt.Fprintf(&source, "use%d(%s(%s));\n", i, name, value)
			}
			entry := writeSource(t, source.String())

			c, err := driver.EmitCWith(t.Context(), entry, lower.AllInstances)

			assert.NilError(t, err)
			for value := range 3 {
				for demand := range 3 {
					required := value < 2 && demand < 2 || value == 2 && demand == 2
					name := literalProjectionTarget(kind, value, demand)
					assert.Check(t, strings.Contains(c, name) == required, "%s: a shared alias site represents every caller, not one runtime capture", name)
				}
			}
			if kind == "captured_receiver" {
				assertCapturedReceiverWrapperSignatures(t, c, "shared_make", []int{0, 1})
				assertCapturedReceiverWrapperSignatures(t, c, "separate_make", []int{2})
			}
		})
	}
}

func TestContainerAliasUnsafeUsesKeepEveryCompatibleTarget(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"function", "receiver", "captured_receiver", "wrapper"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			for _, tt := range []struct {
				name string
				use  string
			}{
				{"transitive_write", "other.value = second;"},
				{"sibling_unselected_write", "sibling.other = second;"},
				{"captured_write", "function change(): void { other.value = second; } change();"},
				{"passed_container", "function replace(v: { value: Value; other: Value }): void { v.value = second; } replace(other);"},
				{"returned_container", "function expose(): { value: Value; other: Value } { return other; } expose().value = second;"},
				{"stored_container", "const stored = [other];"},
				{"dynamic_alias", "const dynamic: any = other;"},
				{"mutable_alias", "let mutable = other;"},
				{"asserted_alias", "const asserted = other as { value: Value; other: Value };"},
				{"dynamic_key", "let key: \"value\" = \"value\"; const ignored = other[key];"},
			} {
				t.Run(tt.name, func(t *testing.T) {
					t.Parallel()
					entry := writeSource(t, containerAliasUnknownFlowSource(kind, tt.use))

					c, err := driver.EmitCWith(t.Context(), entry, lower.AllInstances)

					assert.NilError(t, err)
					for value := range 3 {
						for demand := range 3 {
							name := literalProjectionTarget(kind, value, demand)
							assert.Check(t, strings.Contains(c, name), "%s remains: one unsafe alias use poisons root and sibling projections", name)
						}
					}
				})
			}
		})
	}
}

func containerAliasUnknownFlowSource(kind, unsafeUse string) string {
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
		} else if kind == "wrapper" {
			fmt.Fprintf(&source, "function read%d(): Factory { const receiver: Maker = new M%d(); return receiver.make; }\n", i, i)
		}
	}
	switch kind {
	case "receiver", "captured_receiver":
		source.WriteString("type Value = Maker; const first: Value = new M0(); const second: Value = new M1();\n")
	case "wrapper":
		source.WriteString("type Value = Factory; const first: Value = read0(); const second: Value = read1(); const keep: any[] = [first, second, read2()];\n")
	default:
		source.WriteString("type Value = Factory; const first: Value = source0; const second: Value = source1; const keep: any[] = [source0, source1, source2];\n")
	}
	source.WriteString("const root = { value: first, other: first }; const alias = root; const other = alias; const sibling = root;\n")
	source.WriteString(unsafeUse + "\n")
	for i, receiver := range []string{"root.value", "alias.value", "sibling.value"} {
		if kind == "captured_receiver" {
			receiver = fmt.Sprintf("read%d(%s)", i, receiver)
		}
		fmt.Fprintf(&source, "use%d(%s);\n", i, receiver)
	}
	return source.String()
}
