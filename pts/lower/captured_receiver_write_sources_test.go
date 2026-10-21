package lower

import (
	"fmt"
	"testing"

	"github.com/microsoft/typescript-go/pts/ir"
	"gotest.tools/v3/assert"
)

func TestCapturedReceiverAssertionWritesKeepSyntheticDemandsUnknown(t *testing.T) {
	t.Parallel()
	for _, target := range []string{"(receiver as any)", "(<any>receiver)", "receiver!", "(receiver satisfies A)", "[(receiver as any)]"} {
		for _, closure := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/closure=%t", target, closure), func(t *testing.T) {
				t.Parallel()
				value := "new B()"
				if target[0] == '[' {
					value = "[new B()]"
				}
				write := target + " = " + value + ";"
				if closure {
					write = "function change(): void { " + write + " } change();"
				}
				l, probe := valueFlowProbe(t, "class A {} class B {} function read(receiver: A): void { "+write+" const probe = receiver; probe; } read(new A());")
				symbol := l.target(l.target(probe).ValueDeclaration.Initializer())
				assert.Assert(t, l.assignedAnywhere[symbol], "an asserted receiver write must be indexed")
				assert.Equal(t, l.assignedOutside[symbol], closure)
				forward := l.methodForward(&ir.Local{}, method{name: "make", result: ir.Value, call: func(ir.Expr, []ir.Expr) ir.Expr {
					return &ir.UndefinedConst{}
				}}, nil, nil, nil, probe)

				forward(&demand{want: &ir.FuncType{Result: ir.Value}}, &ir.Func{}, nil)

				assert.Equal(t, len(l.demands), 1)
				assert.Assert(t, l.demands[0].receiverSource == probe, "the synthetic demand must carry the actual captured expression")
				steps := maxValueFlowSteps
				assert.Assert(t, l.receiverSources(l.demands[0].receiverSource, &steps, 0) == nil, "mutation cannot prove a captured receiver")
			})
		}
	}
}

func TestFunctionParameterAssertionWritesKeepSourcesUnknown(t *testing.T) {
	t.Parallel()
	for _, target := range []string{"(receiver as any)", "(<any>receiver)", "receiver!", "(receiver satisfies (() => void))", "[(receiver as any)]"} {
		t.Run(target, func(t *testing.T) {
			t.Parallel()
			value := "other"
			if target[0] == '[' {
				value = "[other]"
			}
			l, probe := valueFlowProbe(t, "function first(): void {} function other(): void {} function consume(receiver: () => void): void { "+target+" = "+value+"; const probe = receiver; probe; } consume(first);")
			steps := maxValueFlowSteps

			assert.Assert(t, l.valueSources(probe, &steps, 0) == nil, "function parameters share the conservative write index")
		})
	}
}
