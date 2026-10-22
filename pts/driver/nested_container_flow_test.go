package driver_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/pts/driver"
	"github.com/microsoft/typescript-go/pts/lower"
	"gotest.tools/v3/assert"
)

func TestNestedContainerDemandsOnlyInstantiateSelectedTerminals(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"function", "receiver", "captured_receiver", "wrapper"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			for _, origin := range []string{"fresh_object", "fresh_array", "object_tree", "array_tree", "mixed_tree", "root_alias", "root_fanout", "selected_return", "terminal_union"} {
				t.Run(origin, func(t *testing.T) {
					t.Parallel()
					entry := writeSource(t, nestedContainerFlowSource(kind, origin))

					c, err := driver.EmitCWith(t.Context(), entry, lower.AllInstances)

					assert.NilError(t, err)
					pairs := 0
					for value := range 3 {
						for demand := range 3 {
							required := value == demand || origin == "terminal_union" && value == 1 && demand == 0
							name := literalProjectionTarget(kind, value, demand)
							present := strings.Contains(c, name)
							if present {
								pairs++
							}
							assert.Check(t, present == required, "%s: present=%t, selected nested terminal requires=%t", name, present, required)
							if kind != "function" {
								assert.Check(t, strings.Contains(c, "return "+name) == required, "%s must be registered exactly when required", name)
							}
						}
					}
					want := 3
					if origin == "terminal_union" {
						want++
					}
					t.Logf("3 nested-held sources x 3 signatures: %d target pairs; want %d", pairs, want)
					if kind == "captured_receiver" || kind == "wrapper" {
						for wrapper := range 3 {
							required := []int{wrapper}
							if kind == "wrapper" && origin == "terminal_union" && wrapper == 1 {
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

func nestedContainerFlowSource(kind, origin string) string {
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
		fmt.Fprintf(&source, "const selected%d: %s = %s; const alternate%d: %s = %s;\n", i, valueType, value, i, valueType, other)
		selected := fmt.Sprintf("selected%d", i)
		alternate := fmt.Sprintf("alternate%d", i)
		if origin == "terminal_union" && i == 0 {
			selected = "choose() ? " + selected + " : " + alternate
		}
		expression := fmt.Sprintf("root%d.branch.value", i)
		switch origin {
		case "fresh_object":
			expression = fmt.Sprintf("({ branch: { value: %s, other: %s } }).branch.value", selected, alternate)
		case "fresh_array":
			expression = fmt.Sprintf("[[%s, %s]][0][0]", selected, alternate)
		case "array_tree":
			fmt.Fprintf(&source, "const root%d = [[%s, %s]];\n", i, selected, alternate)
			expression = fmt.Sprintf("root%d[0][0]", i)
		case "mixed_tree":
			fmt.Fprintf(&source, "const root%d = { branch: [{ row: { value: %s, other: %s } }] };\n", i, selected, alternate)
			expression = fmt.Sprintf("root%d.branch[0].row.value", i)
		case "root_alias":
			fmt.Fprintf(&source, "const root%d = { branch: { value: %s, other: %s } }; const alias%d = root%d; const other%d = alias%d;\n", i, selected, alternate, i, i, i, i)
			expression = fmt.Sprintf("other%d.branch.value", i)
		case "root_fanout":
			fmt.Fprintf(&source, "const root%d = { branch: { value: %s }, side: [%s] }; const alias%d = root%d; const sibling%d = root%d; const other%d = alias%d; const ignored%d = sibling%d.side[0];\n", i, selected, alternate, i, i, i, i, i, i, i, i)
			expression = fmt.Sprintf("other%d.branch.value", i)
		case "selected_return":
			fmt.Fprintf(&source, "function select%d(): %s { const root = { branch: { value: %s, other: %s } }; const alias = root; return alias.branch.value; }\n", i, valueType, selected, alternate)
			expression = fmt.Sprintf("select%d()", i)
		default:
			fmt.Fprintf(&source, "const root%d = { branch: { value: %s, other: %s } };\n", i, selected, alternate)
		}
		if kind == "captured_receiver" {
			expression = fmt.Sprintf("read%d(%s)", i, expression)
		}
		fmt.Fprintf(&source, "use%d(%s);\n", i, expression)
	}
	return source.String()
}

// Preserve the complete former unknown fixture: a closed extraction no longer
// justifies retaining the six source-impossible targets.
func TestNestedContainerExtractedAliasKeepsOnlyReachableTargets(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"function", "receiver", "captured_receiver", "wrapper"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			entry := writeSource(t, nestedContainerUnknownFlowSource(kind, "const extracted = other.branch;"))

			c, err := driver.EmitCWith(t.Context(), entry, lower.AllInstances)

			assert.NilError(t, err)
			for value := range 3 {
				for demand := range 3 {
					name := literalProjectionTarget(kind, value, demand)
					required := value == 0
					assert.Check(t, strings.Contains(c, name) == required, "%s: the unchanged extracted-alias fixture proves only source zero", name)
					if kind != "function" {
						assert.Check(t, strings.Contains(c, "return "+name) == required, "%s registration follows the target", name)
					}
				}
			}
			if kind == "captured_receiver" {
				for wrapper := range 3 {
					assertCapturedReceiverWrapperSignatures(t, c, fmt.Sprintf("read%d_make", wrapper), []int{wrapper})
				}
			}
		})
	}
}

func TestNestedContainerUnsafeUsesKeepEveryCompatibleTarget(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"function", "receiver", "captured_receiver", "wrapper"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			for _, tt := range []struct {
				name string
				use  string
			}{
				{"leaf_write", "other.branch.value = second;"},
				{"sibling_write", "other.side.value = second;"},
				{"intermediate_replacement", "other.branch = other.side;"},
				{"captured_write", "function change(): void { other.branch.value = second; } change();"},
				{"passed_inner", "function replace(value: { value: Value; other: Value }): void { value.value = second; } replace(other.branch);"},
				{"returned_inner", "function expose(): { value: Value; other: Value } { return other.branch; } expose().value = second;"},
				{"stored_inner", "const stored = [other.branch];"},
				{"dynamic_inner", "const dynamic: any = other.branch;"},
				{"asserted_inner", "const asserted = other.branch as { value: Value; other: Value };"},
				{"dynamic_inner_key", "let key: \"value\" = \"value\"; const ignored = other.branch[key];"},
			} {
				t.Run(tt.name, func(t *testing.T) {
					t.Parallel()
					entry := writeSource(t, nestedContainerUnknownFlowSource(kind, tt.use))

					c, err := driver.EmitCWith(t.Context(), entry, lower.AllInstances)

					assert.NilError(t, err)
					for value := range 3 {
						for demand := range 3 {
							name := literalProjectionTarget(kind, value, demand)
							assert.Check(t, strings.Contains(c, name), "%s remains when any nested use invalidates the whole root", name)
							if kind != "function" {
								assert.Check(t, strings.Contains(c, "return "+name), "%s remains registered", name)
							}
						}
					}
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

func nestedContainerUnknownFlowSource(kind, unsafeUse string) string {
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
	source.WriteString("const root = { branch: { value: first, other: first }, side: { value: first, other: first }, plain: first }; const alias = root; const other = alias;\n")
	source.WriteString(unsafeUse + "\n")
	for i, terminal := range []string{"root.branch.value", "alias.side.value", "other.plain"} {
		if kind == "captured_receiver" {
			terminal = fmt.Sprintf("read%d(%s)", i, terminal)
		}
		fmt.Fprintf(&source, "use%d(%s);\n", i, terminal)
	}
	return source.String()
}
