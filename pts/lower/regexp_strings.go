package lower

import (
	"fmt"
	"math"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/pts/ir"
)

// The String methods that take a RegExp (D-134) are runtime functions over a boxed
// pattern, which the runtime tells apart (a RegExp, or a string it uses as one or makes a
// RegExp of, as JS does): match, matchAll and search always; split, replace and
// replaceAll where the pattern may be a RegExp (a string pattern keeps D-082's lowering).
// A replacement callback runs in a generated helper, called with each match's arguments.

// regexpStringCall lowers s.match(p), s.matchAll(p), s.search(p), and s.split(p, limit),
// s.replace(p, r) and s.replaceAll(p, r) for a p that may be a RegExp; handled is false
// for any other call.
func (l *lowerer) regexpStringCall(call *ast.CallExpression, name string) (x ir.Expr, handled bool) {
	args := call.Arguments.Nodes
	switch name {
	case "match", "matchAll", "search":
	case "split", "replace", "replaceAll":
		if len(args) == 0 || !l.mayBeRegExp(args[0]) {
			return nil, false
		}
	default:
		return nil, false
	}
	if l.hasSpreadArgument(call) {
		return nil, true
	}
	receiver := ast.SkipParentheses(call.Expression).Expression()
	operands := l.expressions(append([]*ast.Node{receiver}, args...))
	if operands == nil {
		return nil, true
	}
	subject := l.convert(operands[0], ir.String, receiver)
	if subject == nil {
		return nil, true
	}
	pattern := box(&ir.UndefinedConst{})
	if len(operands) > 1 {
		pattern = box(operands[1])
	}
	switch name {
	case "match", "matchAll":
		result := l.irType(l.typeAt(call.AsNode()), call.AsNode()) // RegExpMatchArray | null, held as a dynamic value; an iterator
		if result == nil {
			return nil, true
		}
		return l.callExtern("pts_string_"+snakeCase(name), result, subject, pattern), true
	case "search":
		return l.callExtern("pts_string_search", ir.Number, subject, pattern), true
	case "split":
		limit := box(&ir.UndefinedConst{})
		if len(operands) > 2 {
			limit = box(operands[2])
		}
		return l.callExtern("pts_string_split_value", ir.StringArray, subject, pattern, limit), true
	}
	if len(operands) != 3 {
		l.notSupportedYet(call.AsNode(), fmt.Sprintf("`%s` with these arguments is", name))
		return nil, true
	}
	return l.regexpReplace(call, name == "replaceAll", subject, operands[1], operands[2]), true
}

// mayBeRegExp reports whether the value of node may be a RegExp: a RegExp, a union with
// one, or a dynamic value.
func (l *lowerer) mayBeRegExp(node *ast.Node) bool {
	switch t := l.irType(l.typeAt(node), node).(type) {
	case *ir.Class:
		return t == l.regexpClass
	case *ir.Union:
		return t.Has(l.regexpClass)
	}
	return l.irType(l.typeAt(node), node) == ir.Value
}

// regexpReplace lowers s.replace(pattern, replacement) and replaceAll for a pattern that
// may be a RegExp: a replacement string's references are substituted by the runtime; a
// callback, for a RegExp pattern, is called by a helper on each match.
func (l *lowerer) regexpReplace(call *ast.CallExpression, all bool, subject, pattern, replacement ir.Expr) ir.Expr {
	node := call.Arguments.Nodes[1]
	if ft, ok := replacement.Type().(*ir.FuncType); ok {
		if pattern.Type() != l.regexpClass {
			l.notSupportedYet(node, fmt.Sprintf("a replacement callback with a pattern of type %s is", pattern.Type()))
			return nil
		}
		helper := l.regexpReplaceHelper(all, ft, node)
		if helper == nil {
			return nil
		}
		return &ir.Call{Func: helper, Args: []ir.Expr{subject, pattern, replacement}}
	}
	text := l.toString(replacement, node)
	if text == nil {
		return nil
	}
	return l.callExtern("pts_string_replace_value", ir.String, subject, box(pattern), text, &ir.BoolConst{Value: all})
}

// regexpReplaceHelper returns the helper that replaces a RegExp's matches in a string (each
// one with g) by what a callback of type ft returns for them, making it the first time.
// As @@replace does, it finds every match before it calls the callback.
func (l *lowerer) regexpReplaceHelper(all bool, ft *ir.FuncType, node *ast.Node) *ir.Func {
	key := helperKey{method: "RegExp.replace", callback: ft}
	if all {
		key.method = "RegExp.replaceAll"
	}
	if helper, ok := l.helpers[key]; ok {
		return helper
	}
	h := &helperBuilder{l: l, key: key, node: node}
	h.fn = &ir.Func{Name: key.method, Module: l.function.Module, Pos: l.pos(node), Result: ir.String}
	l.inFunction(h.fn, func() {
		subject, regexp := l.newLocal("string", ir.String), l.newLocal("regexp", l.regexpClass)
		h.callback = l.newLocal("callback", ft)
		h.fn.Params = []*ir.Local{subject, regexp, h.callback}
		h.fn.Body = &ir.Block{At: h.fn.Pos, Stmts: h.replaceMatches(&ir.LocalRef{Local: subject}, &ir.LocalRef{Local: regexp}, all)}
	})
	if h.failed {
		l.helpers[key] = nil
		return nil
	}
	l.helpers[key] = h.fn
	return h.fn
}

// replaceMatches is a RegExp replace helper's body: the matches, the callback's result for
// each, then the string with each match replaced by its result.
func (h *helperBuilder) replaceMatches(subject, regexp ir.Expr, all bool) []ir.Stmt {
	l := h.l
	matches, replacements, i := l.newLocal("matches", ir.ValueArray), l.newLocal("replacements", ir.StringArray), l.newLocal("i", ir.Number)
	ref := func(local *ir.Local) ir.Expr { return &ir.LocalRef{Local: local} }
	args := l.callExtern("pts_regexp_replacer_arguments", ir.ValueArray, &ir.ArrayCall{Op: ir.ArrayElement, Args: []ir.Expr{ref(matches), ref(i)}})
	replaced := h.replacerCall(args)
	return []ir.Stmt{
		h.declare(matches, l.callExtern("pts_regexp_replace_matches", ir.ValueArray, subject, regexp, &ir.BoolConst{Value: all})),
		h.declare(replacements, &ir.NewArray{Array: ir.StringArray}),
		h.declare(i, &ir.NumberConst{Value: 0}),
		&ir.Loop{
			At: h.fn.Pos, Label: &ir.Label{},
			Cond:   &ir.Binary{Op: ir.Lt, X: ref(i), Y: &ir.ArrayCall{Op: ir.ArrayLength, Args: []ir.Expr{ref(matches)}}},
			Update: &ir.Assign{Local: i, Value: &ir.Binary{Op: ir.Add, X: ref(i), Y: &ir.NumberConst{Value: 1}}},
			Body:   &ir.Block{At: h.fn.Pos, Stmts: []ir.Stmt{h.stmt(&ir.ArrayCall{Op: ir.ArrayPush, Args: []ir.Expr{ref(replacements), replaced}})}},
		},
		h.ret(l.callExtern("pts_regexp_replace_with", ir.String, subject, ref(matches), ref(replacements))),
	}
}

// replacerCall is ToString of the callback's result for a match whose arguments are args,
// an any[] as long as the match makes it (match, groups, index, input and maybe the groups
// object): each parameter takes its element (undefined past the end), converted to its
// type, and a rest parameter the elements from its position on.
func (h *helperBuilder) replacerCall(args ir.Expr) ir.Expr {
	l, ft := h.l, h.key.callback
	converted := make([]ir.Expr, 0, len(ft.Params))
	for i, position := 0, 0; i < len(ft.Params); i, position = i+1, position+1 {
		param := ft.Params[i]
		var arg ir.Expr
		switch rest, isRest := param.(*ir.Rest); {
		case isRest:
			arg = l.convert(&ir.ArrayCall{Op: ir.ArraySlice, Args: []ir.Expr{args, &ir.NumberConst{Value: float64(position)}, &ir.NumberConst{Value: math.Inf(1)}}}, rest.Array, h.node)
		case hasDefault(ft.Params, i):
			l.notSupportedYet(h.node, "a replacement callback with default parameter values is")
		default:
			arg = l.convert(&ir.ArrayCall{Op: ir.ArrayElement, Args: []ir.Expr{args, &ir.NumberConst{Value: float64(position)}}}, param, h.node)
		}
		if arg == nil {
			h.failed = true
			return &ir.StringConst{}
		}
		converted = append(converted, arg)
	}
	result := &ir.CallValue{Callee: &ir.LocalRef{Local: h.callback}, Args: converted}
	if result.Type() == ir.Void {
		l.notSupportedYet(h.node, "a replacement callback that returns nothing is")
		h.failed = true
		return &ir.StringConst{}
	}
	text := l.toString(result, h.node)
	if text == nil {
		h.failed = true
		return &ir.StringConst{}
	}
	return text
}
