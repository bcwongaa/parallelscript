package lower

import (
	"fmt"
	"math"
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/pts/ir"
)

// The String methods and functions of M3.5a whose calls aren't one runtime function with
// fixed parameters, as the builtins table's are (D-082): concat takes any number of
// strings, replace and replaceAll a replacement string or a callback, localeCompare
// locales and options, and String.fromCharCode and String.fromCodePoint their arguments
// as an array.

// stringLibraryCall lowers a call of one of those methods or functions; handled is
// false if the call isn't one.
func (l *lowerer) stringLibraryCall(call *ast.CallExpression) (x ir.Expr, handled bool) {
	callee := ast.SkipParentheses(call.Expression)
	if builtin, component, ok := l.uriFunction(callee); ok {
		return l.uriCall(call, builtin, component), true
	}
	switch member := l.libMemberName(callee); member {
	case "String.concat":
		return l.stringConcat(call), true
	case "String.replace", "String.replaceAll":
		return l.stringReplace(call, member == "String.replaceAll"), true
	case "String.localeCompare":
		return l.localeCompare(call), true
	case "StringConstructor.fromCharCode", "StringConstructor.fromCodePoint":
		builtin := ir.StringFromCharCode
		if member == "StringConstructor.fromCodePoint" {
			builtin = ir.StringFromCodePoint
		}
		return l.stringFromCodes(call, builtin), true
	}
	return nil, false
}

// uriFunction reports whether callee names encodeURI, encodeURIComponent, decodeURI or
// decodeURIComponent, and which builtin it is, for components or not.
func (l *lowerer) uriFunction(callee *ast.Node) (builtin *ir.Builtin, component bool, ok bool) {
	if callee.Kind != ast.KindIdentifier || !l.isDeclaredInDefaultLib(l.target(callee)) {
		return nil, false, false
	}
	switch callee.Text() {
	case "encodeURI":
		return ir.URIEncode, false, true
	case "encodeURIComponent":
		return ir.URIEncode, true, true
	case "decodeURI":
		return ir.URIDecode, false, true
	case "decodeURIComponent":
		return ir.URIDecode, true, true
	}
	return nil, false, false
}

// uriCall lowers a call of a URI function: of its argument as a string (ToString).
func (l *lowerer) uriCall(call *ast.CallExpression, builtin *ir.Builtin, component bool) ir.Expr {
	if l.hasSpreadArgument(call) {
		return nil
	}
	var text ir.Expr = &ir.StringConst{Value: "undefined"}
	if len(call.Arguments.Nodes) > 0 {
		argument := call.Arguments.Nodes[0]
		if text = l.expression(argument); text == nil {
			return nil
		}
		if text = l.toString(text, argument); text == nil {
			return nil
		}
	}
	return &ir.CallBuiltin{Builtin: builtin, Args: []ir.Expr{text, &ir.BoolConst{Value: component}}}
}

// stringConcat lowers `s.concat(a, b, …)`: s + a + b + …, a spread string array joined.
func (l *lowerer) stringConcat(call *ast.CallExpression) ir.Expr {
	result := l.expression(ast.SkipParentheses(call.Expression).Expression())
	if result == nil {
		return nil
	}
	for _, argument := range call.Arguments.Nodes {
		values := l.argumentValues(argument)
		if values == nil {
			return nil
		}
		for _, value := range values {
			piece := ir.Expr(nil)
			switch s, spread := value.(*spreadValue); {
			case spread && s.x.Type() == ir.StringArray:
				piece = &ir.ArrayCall{Op: ir.ArrayJoin, Args: []ir.Expr{s.x, &ir.StringConst{}}}
			case spread:
				l.notSupportedYet(argument, fmt.Sprintf("spreading a %s into `concat` is", s.x.Type()))
				return nil
			default:
				piece = l.toString(value, argument)
			}
			if piece == nil {
				return nil
			}
			result = &ir.Binary{Op: ir.Concat, X: result, Y: piece}
		}
	}
	return result
}

// stringReplace lowers `s.replace(pattern, replacement)` and replaceAll with a string
// pattern: a replacement string goes to the runtime, a callback to a generated helper.
func (l *lowerer) stringReplace(call *ast.CallExpression, all bool) ir.Expr {
	name := "replace"
	if all {
		name = "replaceAll"
	}
	args := call.Arguments.Nodes
	if len(args) != 2 || l.hasSpreadArgument(call) {
		l.notSupportedYet(call.AsNode(), fmt.Sprintf("`%s` with these arguments is", name))
		return nil
	}
	operands := l.expressions([]*ast.Node{ast.SkipParentheses(call.Expression).Expression(), args[0], args[1]})
	if operands == nil {
		return nil
	}
	if operands[1].Type() != ir.String {
		l.notSupportedYet(args[0], fmt.Sprintf("`%s` with a pattern of type %s is", name, operands[1].Type()))
		return nil
	}
	switch replacement := operands[2].Type().(type) {
	case *ir.FuncType:
		helper := l.replaceHelper(name, replacement, args[1])
		if helper == nil {
			return nil
		}
		return &ir.Call{Func: helper, Args: operands}
	default:
		if replacement != ir.String {
			l.notSupportedYet(args[1], fmt.Sprintf("`%s` with a replacement of type %s is", name, replacement))
			return nil
		}
	}
	builtin := ir.StringReplace
	if all {
		builtin = ir.StringReplaceAll
	}
	return &ir.CallBuiltin{Builtin: builtin, Args: operands}
}

// expressions lowers nodes in order, or returns nil if one fails (reported).
func (l *lowerer) expressions(nodes []*ast.Node) []ir.Expr {
	xs := make([]ir.Expr, len(nodes))
	for i, node := range nodes {
		if xs[i] = l.expression(node); xs[i] == nil {
			return nil
		}
	}
	return xs
}

// replaceHelper returns the helper that replaces the first (or, for replaceAll, every)
// occurrence of a string pattern by what a callback of type ft returns for it, called with
// as many of (match, position, string) as it takes; it makes the helper the first time.
func (l *lowerer) replaceHelper(name string, ft *ir.FuncType, node *ast.Node) *ir.Func {
	key := helperKey{method: "String." + name, callback: ft}
	if helper, ok := l.helpers[key]; ok {
		return helper
	}
	h := &helperBuilder{l: l, key: key, node: node}
	h.fn = &ir.Func{Name: key.method, Module: l.function.Module, Pos: l.pos(node), Result: ir.String}
	l.inFunction(h.fn, func() {
		subject, pattern := l.newLocal("string", ir.String), l.newLocal("pattern", ir.String)
		h.callback = l.newLocal("callback", ft)
		h.fn.Params = []*ir.Local{subject, pattern, h.callback}
		r := replacing{h: h, subject: &ir.LocalRef{Local: subject}, pattern: &ir.LocalRef{Local: pattern}}
		body := r.first()
		if name == "replaceAll" {
			body = r.all()
		}
		h.fn.Body = &ir.Block{At: h.fn.Pos, Stmts: body}
	})
	if h.failed {
		l.helpers[key] = nil
		return nil
	}
	l.helpers[key] = h.fn
	return h.fn
}

// replacing builds the body of a replace helper.
type replacing struct {
	h                *helperBuilder
	subject, pattern ir.Expr
}

// replacement is ToString of what the callback returns for the match at position.
func (r replacing) replacement(position ir.Expr) ir.Expr {
	value := r.h.callWith([]ir.Expr{r.pattern, position, r.subject})
	if value.Type() == ir.Void {
		r.h.l.notSupportedYet(r.h.node, "a replacement callback that returns nothing is")
		r.h.failed = true
		return &ir.StringConst{}
	}
	if s := r.h.l.toString(value, r.h.node); s != nil {
		return s
	}
	r.h.failed = true
	return &ir.StringConst{}
}

func (r replacing) slice(from, to ir.Expr) ir.Expr {
	return &ir.CallBuiltin{Builtin: ir.StringSlice, Args: []ir.Expr{r.subject, from, to}}
}

func (r replacing) indexOf(from ir.Expr) ir.Expr {
	return &ir.CallBuiltin{Builtin: ir.StringIndexOf, Args: []ir.Expr{r.subject, r.pattern, from}}
}

func concatAll(pieces ...ir.Expr) ir.Expr {
	result := pieces[0]
	for _, piece := range pieces[1:] {
		result = concat(result, piece)
	}
	return result
}

// first is replace's body: `position = indexOf(pattern); if none, the string; else the
// text before, the callback's replacement and the text after`.
func (r replacing) first() []ir.Stmt {
	h := r.h
	position := h.l.newLocal("position", ir.Number)
	at := &ir.LocalRef{Local: position}
	after := &ir.Binary{Op: ir.Add, X: at, Y: &ir.CallBuiltin{Builtin: ir.StringLength, Args: []ir.Expr{r.pattern}}}
	return []ir.Stmt{
		h.declare(position, r.indexOf(&ir.NumberConst{Value: 0})),
		h.when(&ir.Binary{Op: ir.Lt, X: at, Y: &ir.NumberConst{Value: 0}}, h.ret(r.subject)),
		h.ret(concatAll(r.slice(&ir.NumberConst{Value: 0}, at), r.replacement(at), r.slice(after, &ir.NumberConst{Value: math.Inf(1)}))),
	}
}

// all is replaceAll's body: each match in order, found after the end of the last (or,
// for an empty pattern, a unit further), replaced by what the callback returns for it.
func (r replacing) all() []ir.Stmt {
	h, l := r.h, r.h.l
	length, result, end, position, next := l.newLocal("length", ir.Number), l.newLocal("result", ir.String),
		l.newLocal("end", ir.Number), l.newLocal("position", ir.Number), l.newLocal("next", ir.Number)
	ref := func(local *ir.Local) ir.Expr { return &ir.LocalRef{Local: local} }
	zero := &ir.NumberConst{Value: 0}
	emptyPattern := &ir.Binary{Op: ir.Eq, X: ref(length), Y: zero}
	advance := &ir.Conditional{Cond: emptyPattern, Then: &ir.NumberConst{Value: 1}, Else: ref(length)}
	past := &ir.Binary{Op: ir.Gt, X: ref(next), Y: &ir.CallBuiltin{Builtin: ir.StringLength, Args: []ir.Expr{r.subject}}}
	body := []ir.Stmt{
		h.stmt(&ir.Assign{Local: result, Value: concatAll(ref(result), r.slice(ref(end), ref(position)), r.replacement(ref(position)))}),
		h.stmt(&ir.Assign{Local: end, Value: &ir.Binary{Op: ir.Add, X: ref(position), Y: ref(length)}}),
		h.declare(next, &ir.Binary{Op: ir.Add, X: ref(position), Y: advance}),
		h.stmt(&ir.Assign{Local: position, Value: &ir.Conditional{Cond: past, Then: &ir.NumberConst{Value: -1}, Else: r.indexOf(ref(next))}}),
	}
	return []ir.Stmt{
		h.declare(length, &ir.CallBuiltin{Builtin: ir.StringLength, Args: []ir.Expr{r.pattern}}),
		h.declare(result, &ir.StringConst{}),
		h.declare(end, zero),
		h.declare(position, r.indexOf(zero)),
		&ir.Loop{At: h.fn.Pos, Label: &ir.Label{}, Cond: &ir.Binary{Op: ir.Ge, X: ref(position), Y: zero}, Body: &ir.Block{At: h.fn.Pos, Stmts: body}},
		h.ret(concatAll(ref(result), r.slice(ref(end), &ir.NumberConst{Value: math.Inf(1)}))),
	}
}

// localeCompare lowers `s.localeCompare(t)` in the runtime's root collation (D-082),
// which an English locale (or undefined) shares; other locales and options are PTS1001.
func (l *lowerer) localeCompare(call *ast.CallExpression) ir.Expr {
	args := call.Arguments.Nodes
	if l.hasSpreadArgument(call) {
		return nil
	}
	if len(args) > 1 && !isUndefined(args[1]) && !isEnglishLocale(args[1]) {
		l.notSupportedYet(args[1], "`localeCompare` with a locale other than English is")
		return nil
	}
	if len(args) > 2 && !isUndefined(args[2]) {
		l.notSupportedYet(args[2], "`localeCompare` with options is")
		return nil
	}
	operands := l.expressions([]*ast.Node{ast.SkipParentheses(call.Expression).Expression(), args[0]})
	if operands == nil {
		return nil
	}
	that := l.convert(operands[1], ir.String, args[0])
	if that == nil {
		return nil
	}
	return &ir.CallBuiltin{Builtin: ir.StringLocaleCompare, Args: []ir.Expr{operands[0], that}}
}

// isEnglishLocale reports whether node is a string literal naming an English locale.
func isEnglishLocale(node *ast.Node) bool {
	node = ast.SkipParentheses(node)
	if node.Kind != ast.KindStringLiteral && node.Kind != ast.KindNoSubstitutionTemplateLiteral {
		return false
	}
	tag := strings.ToLower(node.Text())
	return tag == "en" || strings.HasPrefix(tag, "en-")
}

// stringFromCodes lowers String.fromCharCode(…) and String.fromCodePoint(…): the runtime
// function of an array of the arguments, spread arrays' elements in their place.
func (l *lowerer) stringFromCodes(call *ast.CallExpression, builtin *ir.Builtin) ir.Expr {
	var values []ir.Expr
	var at []*ast.Node
	for _, argument := range call.Arguments.Nodes {
		lowered := l.argumentValues(argument)
		if lowered == nil {
			return nil
		}
		for _, value := range lowered {
			values, at = append(values, value), append(at, argument)
		}
	}
	codes := l.restArray(values, at, ir.NumberArray)
	if codes == nil {
		return nil
	}
	return &ir.CallBuiltin{Builtin: builtin, Args: []ir.Expr{codes}}
}
