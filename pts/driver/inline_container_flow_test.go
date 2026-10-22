package driver_test

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/pts/driver"
	"github.com/microsoft/typescript-go/pts/lower"
	"gotest.tools/v3/assert"
)

func TestInlineContainerDemandsOnlyInstantiateSelectedTerminals(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"function", "receiver", "captured_receiver", "wrapper"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			for _, origin := range []string{"object_child", "array_child", "array_object", "array_array", "recursive_extraction", "aliases", "selected_return", "closed_terminal_parameter", "terminal_union"} {
				t.Run(origin, func(t *testing.T) {
					t.Parallel()
					entry := writeSource(t, inlineContainerFlowSource(kind, origin, 3))

					c, err := driver.EmitCWith(t.Context(), entry, lower.AllInstances)

					assert.NilError(t, err)
					pairs := inlineContainerAssertPairs(t, c, kind, 3, func(value, demand int) bool {
						return value == demand || origin == "terminal_union" && value == 1 && demand == 0
					})
					want := 3
					if origin == "terminal_union" {
						want++
					}
					t.Logf("direct AllInstances: 3 inline sources x 3 signatures = %d pairs; want %d", pairs, want)
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

func TestInlineContainerSharedSitesJoinEveryTerminalCaller(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"function", "receiver", "captured_receiver"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			var source strings.Builder
			inventoryKind := kind
			if kind == "captured_receiver" {
				inventoryKind = "function"
			}
			source.WriteString(inlineContainerFlowInventory(inventoryKind, 3))
			valueType, resultType, selected := "Factory", "Factory", "alias.value"
			if kind != "function" {
				valueType = "Maker"
				if kind == "receiver" {
					resultType = "Maker"
				} else {
					selected += ".make"
				}
			}
			for _, name := range []string{"shared", "separate"} {
				fmt.Fprintf(&source, "function %s(value: %s): %s { const branch = ({ branch: { rows: [{ value }] } }).branch; const rows = branch.rows; const row = rows[0]; const alias = row; return %s; }\n", name, valueType, resultType, selected)
			}
			for i := range 3 {
				name, value := "shared", fmt.Sprintf("source%d", i)
				if i == 2 {
					name = "separate"
				}
				if kind != "function" {
					value = fmt.Sprintf("new M%d()", i)
				}
				fmt.Fprintf(&source, "use%d(%s(%s));\n", i, name, value)
			}
			entry := writeSource(t, source.String())

			c, err := driver.EmitCWith(t.Context(), entry, lower.AllInstances)

			assert.NilError(t, err)
			inlineContainerAssertPairs(t, c, kind, 3, func(value, demand int) bool {
				return value < 2 && demand < 2 || value == 2 && demand == 2
			})
			if kind == "captured_receiver" {
				assertCapturedReceiverWrapperSignatures(t, c, "shared_make", []int{0, 1})
				assertCapturedReceiverWrapperSignatures(t, c, "separate_make", []int{2})
			}
		})
	}
}

func TestInlineContainerUnsafeUsesKeepEveryCompatibleTarget(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"function", "receiver", "captured_receiver", "wrapper"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			for _, tt := range []struct {
				name string
				use  string
			}{
				{"unmodified", ""},
				{"late_selected_write", "other.value = second;"},
				{"late_unselected_write", "other.other = second;"},
				{"late_sibling_write", "sibling.value = second;"},
				{"late_parent_write", "alias.plain = second;"},
				{"captured_write", "function change(): void { other.value = second; } change();"},
				{"passed_child", "function replace(value: { value: Value; other: Value }): void { value.value = second; } replace(other);"},
				{"returned_sibling", "function expose(): { value: Value; other: Value } { return sibling; } expose().value = second;"},
				{"stored_child", "const stored = [other];"},
				{"dynamic_alias", "const dynamic: any = other;"},
				{"mutable_alias", "let mutable = other;"},
				{"asserted_alias", "const asserted = other as { value: Value; other: Value };"},
				{"dynamic_key", "let key: \"value\" = \"value\"; const ignored = other[key];"},
			} {
				t.Run(tt.name, func(t *testing.T) {
					t.Parallel()
					entry := writeSource(t, inlineContainerUnknownFlowSource(kind, tt.use))

					c, err := driver.EmitCWith(t.Context(), entry, lower.AllInstances)

					assert.NilError(t, err)
					inlineContainerAssertPairs(t, c, kind, 3, func(value, demand int) bool {
						return tt.use != "" || value == 0
					})
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

func TestInlineContainerGrowthKeepsOnlyReachablePairs(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"function", "receiver", "captured_receiver", "wrapper"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			for _, count := range []int{4, 8} {
				for _, escaped := range []bool{false, true} {
					t.Run(fmt.Sprintf("n%d_escaped_%t", count, escaped), func(t *testing.T) {
						t.Parallel()
						origin := "object_child"
						if escaped {
							origin = "escaped_child"
						}
						entry := writeSource(t, inlineContainerFlowSource(kind, origin, count))
						c, err := driver.EmitCWith(t.Context(), entry, lower.AllInstances)
						assert.NilError(t, err)
						pairs := inlineContainerAssertPairs(t, c, kind, count, func(value, demand int) bool {
							return escaped || value == demand
						})
						t.Logf("direct AllInstances: n=%d escaped=%t pairs=%d lines=%d bytes=%d sha256=%x", count, escaped, pairs, strings.Count(c, "\n"), len(c), sha256.Sum256([]byte(c)))
					})
				}
			}
		})
	}
}

func inlineContainerAssertPairs(t *testing.T, c, kind string, count int, required func(int, int) bool) int {
	t.Helper()
	pairs := 0
	for value := range count {
		for demand := range count {
			name := literalProjectionTarget(kind, value, demand)
			present := strings.Contains(c, name)
			if present {
				pairs++
			}
			assert.Check(t, present == required(value, demand), "%s: present=%t, actual inline source requires=%t", name, present, required(value, demand))
			if kind != "function" {
				assert.Check(t, strings.Contains(c, "return "+name) == required(value, demand), "%s registration follows the required target", name)
			}
		}
	}
	return pairs
}

func inlineContainerFlowInventory(kind string, count int) string {
	var source strings.Builder
	source.WriteString(literalProjectionFlowPrelude())
	source.WriteString("function consumeContainer(value: unknown): void {}\n")
	for i := range count {
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
	return source.String()
}

func inlineContainerFlowSource(kind, origin string, count int) string {
	var source strings.Builder
	source.WriteString(inlineContainerFlowInventory(kind, count))
	valueType := "Factory"
	if kind == "receiver" || kind == "captured_receiver" {
		valueType = "Maker"
	}
	for i := range count {
		value, other := fmt.Sprintf("source%d", i), fmt.Sprintf("source%d", (i+1)%count)
		if valueType == "Maker" {
			value, other = fmt.Sprintf("new M%d()", i), fmt.Sprintf("new M%d()", (i+1)%count)
		} else if kind == "wrapper" {
			value, other = fmt.Sprintf("read%d()", i), fmt.Sprintf("read%d()", (i+1)%count)
		}
		fmt.Fprintf(&source, "const selected%d: %s = %s; const alternate%d: %s = %s;\n", i, valueType, value, i, valueType, other)
		selected, alternate := fmt.Sprintf("selected%d", i), fmt.Sprintf("alternate%d", i)
		if origin == "terminal_union" && i == 0 {
			selected = "choose() ? " + selected + " : " + alternate
		}
		expression := fmt.Sprintf("child%d.value", i)
		switch origin {
		case "array_child":
			fmt.Fprintf(&source, "const child%d = ({ child: [%s, %s] }).child; const alias%d = child%d;\n", i, selected, alternate, i, i)
			expression = fmt.Sprintf("alias%d[0]", i)
		case "array_object":
			fmt.Fprintf(&source, "const child%d = [{ value: %s, other: %s }][0]; const alias%d = child%d;\n", i, selected, alternate, i, i)
			expression = fmt.Sprintf("alias%d.value", i)
		case "array_array":
			fmt.Fprintf(&source, "const child%d = [[%s, %s]][0];\n", i, selected, alternate)
			expression = fmt.Sprintf("child%d[0]", i)
		case "recursive_extraction":
			fmt.Fprintf(&source, "const branch%d = ({ branch: { rows: [{ value: %s, other: %s }] } }).branch; const rows%d = branch%d.rows; const child%d = rows%d[0];\n", i, selected, alternate, i, i, i, i)
		case "aliases":
			fmt.Fprintf(&source, "const child%d = ((({ child: ({ value: %s, other: %s }) }).child)); const alias%d = ((child%d)); const other%d = alias%d;\n", i, selected, alternate, i, i, i, i)
			expression = fmt.Sprintf("other%d.value", i)
		case "selected_return":
			fmt.Fprintf(&source, "function select%d(): %s { const child = ({ child: { value: %s, other: %s } }).child; const alias = child; return alias.value; }\n", i, valueType, selected, alternate)
			expression = fmt.Sprintf("select%d()", i)
		case "closed_terminal_parameter":
			fmt.Fprintf(&source, "function select%d(value: %s): %s { const child = ({ child: { value } }).child; return child.value; }\n", i, valueType, valueType)
			expression = fmt.Sprintf("select%d(%s)", i, selected)
		default:
			fmt.Fprintf(&source, "const child%d = ({ child: { value: %s, other: %s } }).child;\n", i, selected, alternate)
		}
		if origin == "escaped_child" {
			fmt.Fprintf(&source, "consumeContainer(child%d);\n", i)
		}
		if kind == "captured_receiver" {
			expression = fmt.Sprintf("read%d(%s)", i, expression)
		}
		fmt.Fprintf(&source, "use%d(%s);\n", i, expression)
	}
	return source.String()
}

func inlineContainerUnknownFlowSource(kind, lateUse string) string {
	var source strings.Builder
	source.WriteString(inlineContainerFlowInventory(kind, 3))
	switch kind {
	case "receiver", "captured_receiver":
		source.WriteString("type Value = Maker; const first: Value = new M0(); const second: Value = new M1();\n")
	case "wrapper":
		source.WriteString("type Value = Factory; const first: Value = read0(); const second: Value = read1(); const keep: any[] = [first, second, read2()];\n")
	default:
		source.WriteString("type Value = Factory; const first: Value = source0; const second: Value = source1; const keep: any[] = [source0, source1, source2];\n")
	}
	source.WriteString("const extracted = ({ child: { main: { value: first, other: first }, side: { value: first, other: first }, plain: first } }).child; const alias = extracted; const other = alias.main; const sibling = extracted.side;\n")
	for i, terminal := range []string{"extracted.main.value", "alias.side.value", "other.value"} {
		if kind == "captured_receiver" {
			terminal = fmt.Sprintf("read%d(%s)", i, terminal)
		}
		fmt.Fprintf(&source, "use%d(%s);\n", i, terminal)
	}
	source.WriteString(lateUse + "\n")
	return source.String()
}
