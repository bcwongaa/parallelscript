package lower

import (
	"fmt"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/pts/ir"
)

// Values of type `any` are dynamic values, ir.Value (D-070): their properties and
// elements are found at run time (DynamicGet, DynamicSet), operators apply JS's
// conversions to them, and converting one to a typed location checks it.

// isAny reports whether tsc types node as any.
func (l *lowerer) isAny(node *ast.Node) bool {
	return l.typeAt(node).Flags()&checker.TypeFlagsAny != 0
}

// isDynamicSource reports whether tsc types node as any or unknown: a value nothing but
// the program's logic vouches for, which a conversion checks.
func (l *lowerer) isDynamicSource(node *ast.Node) bool {
	return node != nil && l.typeAt(node).Flags()&(checker.TypeFlagsAny|checker.TypeFlagsUnknown) != 0
}

// isCheckable reports whether a dynamic value converts to t with a check the runtime
// makes (cgen's valueTest): a primitive, an integer type, a class, or a union of them.
// Interfaces, arrays and the rest wait for M3.3b's runtime type checks.
func isCheckable(t ir.Type) bool {
	switch {
	case t == ir.Number || t == ir.String || t == ir.Bool || t == ir.Null || t == ir.Undefined || ir.IsNumeric(t):
		return true
	case isClass(t):
		return !t.(*ir.Class).Shape // a shape's objects are checked by M3.3b's descriptors
	case isUnion(t):
		for _, member := range t.(*ir.Union).Members {
			if !isCheckable(member) {
				return false
			}
		}
		return true
	}
	return false
}

// dynamicKey is the key of `o.name` or `o[k]` as a dynamic value.
func (l *lowerer) dynamicKey(node *ast.Node) ir.Expr {
	if node.Kind == ast.KindPropertyAccessExpression {
		return box(&ir.StringConst{Value: node.AsPropertyAccessExpression().Name().Text()})
	}
	key := l.valueExpression(node.AsElementAccessExpression().ArgumentExpression)
	if key == nil {
		return nil
	}
	return box(key)
}

// dynamicRead lowers `o.name` or `o[k]` for an o of type any: the property found at run time.
func (l *lowerer) dynamicRead(node *ast.Node) ir.Expr {
	object := l.expression(node.Expression())
	key := l.dynamicKey(node)
	if object == nil || key == nil {
		return nil
	}
	return &ir.CallBuiltin{Builtin: ir.DynamicGet, Args: []ir.Expr{box(object), key}}
}

// dynamicPlace is the place `o.name` or `o[k]` names for an o of type any.
func (l *lowerer) dynamicPlace(node *ast.Node) (place, bool) {
	object := l.expression(node.Expression())
	key := l.dynamicKey(node)
	if object == nil || key == nil {
		return place{}, false
	}
	return place{object: box(object), key: key, dynamic: true}, true
}

// dynamicOperation lowers a binary operator with an operand of type any, as JS applies
// it: + adds numbers or concatenates (ValueAdd) where tsc doesn't type the result as a
// string; the other arithmetic, bitwise and shift operators convert both operands to
// numbers (ValueToNumber); the comparisons follow JS's rules (ValueLess, ValueLessEqual,
// with the operands swapped for > and >=, evaluated in order); === and !== compare
// dynamic values. ok is false for the operators it leaves to operation.
func (l *lowerer) dynamicOperation(node *ast.Node, op ir.BinaryOp, x ir.Expr, y ir.Expr) (result ir.Expr, ok bool) {
	switch op {
	case ir.Eq, ir.Ne:
		return unionEquality(op, x, y), true
	case ir.Add:
		return &ir.CallBuiltin{Builtin: ir.ValueAdd, Args: []ir.Expr{box(x), box(y)}}, true
	case ir.Lt:
		return &ir.CallBuiltin{Builtin: ir.ValueLess, Args: []ir.Expr{box(x), box(y)}}, true
	case ir.Le:
		return &ir.CallBuiltin{Builtin: ir.ValueLessEqual, Args: []ir.Expr{box(x), box(y)}}, true
	case ir.Gt, ir.Ge:
		left := l.newLocal("left", ir.Value)
		builtin := ir.ValueLess
		if op == ir.Ge {
			builtin = ir.ValueLessEqual
		}
		return &ir.Bind{Local: left, Value: box(x), In: &ir.CallBuiltin{Builtin: builtin, Args: []ir.Expr{box(y), &ir.LocalRef{Local: left}}}}, true
	case ir.Concat:
		return nil, false
	}
	return &ir.Binary{Op: op, X: toNumber(x), Y: toNumber(y)}, true
}

// toNumber is JS's ToNumber of x: x itself for a number, a number for another numeric type.
func toNumber(x ir.Expr) ir.Expr {
	switch {
	case x.Type() == ir.Number:
		return x
	case ir.IsNumeric(x.Type()):
		return &ir.Convert{X: x, To: ir.Number}
	}
	return &ir.CallBuiltin{Builtin: ir.ValueToNumber, Args: []ir.Expr{box(x)}}
}

// dynamicIncrement lowers ++ and -- on a place of type any: the place gets ToNumber of
// its value plus or minus one, and the expression is the new number, or if postfix, the old.
func (l *lowerer) dynamicIncrement(target place, op ir.BinaryOp, postfix bool) ir.Expr {
	return l.readAndStore(target, func(target place) ir.Expr {
		old := l.newLocal("old", ir.Number)
		next := &ir.Binary{Op: op, X: &ir.LocalRef{Local: old}, Y: &ir.NumberConst{Value: 1}}
		var result ir.Expr = next
		if postfix {
			result = &ir.LocalRef{Local: old}
		}
		return &ir.Bind{Local: old, Value: toNumber(target.read()), In: &ir.Sequence{X: target.store(box(next), false), Y: result}}
	})
}

// dynamicConversion converts value, a dynamic value from an expression tsc types as any or
// unknown, to target with a check, or reports (at node) that it can't yet.
func (l *lowerer) dynamicConversion(value ir.Expr, target ir.Type, node *ast.Node) ir.Expr {
	if !isCheckable(target) {
		l.notSupportedYet(node, fmt.Sprintf("converting %s to %s is", l.checker.TypeToString(l.typeAt(node)), target))
		return nil
	}
	return &ir.FromUnion{X: value, To: target}
}

// dynamicWithDefault applies a pattern's default to part, a dynamic value (an any's
// property): the default if it is undefined, else part, converted to the name's type.
func (l *lowerer) dynamicWithDefault(part ir.Expr, initializer *ast.Node, name *ast.Node) ir.Expr {
	t := ir.Type(ir.Value)
	if name.Kind == ast.KindIdentifier {
		if t = l.bindingType(l.checker.GetSymbolAtLocation(name), name); t == nil {
			return nil
		}
	}
	fallback := l.expression(initializer)
	if fallback == nil {
		return nil
	}
	value := l.newLocal("property", ir.Value)
	ref := &ir.LocalRef{Local: value}
	fallback, present := l.convert(fallback, t, initializer), l.convert(ref, t, name)
	if fallback == nil || present == nil {
		return nil
	}
	return &ir.Bind{Local: value, Value: part, In: &ir.Conditional{Cond: &ir.IsNullish{X: ref, Only: ir.Undefined}, Then: fallback, Else: present}}
}
