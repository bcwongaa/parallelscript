package lower

import (
	"fmt"
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/pts/ir"
)

// RegExp (D-133) is a builtin class the runtime defines (pts_regexp, over D-132's compiled
// code). lastIndex is its one field, read and written as any field is; its other
// properties, its methods and its constructor's forms are runtime functions that calls
// reach directly. The arrays exec makes (RegExpExecArray, and String.match's
// RegExpMatchArray) are string[]s to the program, whose index, input and groups the
// runtime keeps after the array.

// regexpFlags are the flag properties of RegExp and the PTS_REGEX_* bit each reports.
var regexpFlags = map[string]float64{
	"hasIndices": 1 << 0, "global": 1 << 1, "ignoreCase": 1 << 2, "multiline": 1 << 3, "dotAll": 1 << 4, "unicode": 1 << 5, "sticky": 1 << 6,
}

// declareRegExpClass makes the builtin class of the lib's RegExp, with its field lastIndex.
func (l *lowerer) declareRegExpClass() {
	symbol := l.checker.GetGlobalSymbol("RegExp", ast.SymbolFlagsValue, nil)
	class := &ir.Class{Name: "RegExp", Builtin: true}
	lastIndex := &ir.Field{Name: "lastIndex", Type: ir.Number, Class: class}
	class.Fields = []*ir.Field{lastIndex}
	l.members.fields[l.checker.GetPropertyOfType(l.checker.GetDeclaredTypeOfSymbol(symbol), "lastIndex").ValueDeclaration] = lastIndex
	l.classes[symbol] = class
	l.builtinClasses = append(l.builtinClasses, class)
	l.regexpClass = class
}

// regexLiteral lowers /pattern/flags: a new RegExp each time it runs, over code compiled once.
func (l *lowerer) regexLiteral(node *ast.Node) ir.Expr {
	text := node.Text()
	end := strings.LastIndexByte(text, '/')
	return &ir.RegExpLiteral{Pattern: text[1:end], Flags: text[end+1:], Class: l.regexpClass}
}

// isRegExpConstructor reports whether a callee is the global RegExp.
func (l *lowerer) isRegExpConstructor(callee *ast.Node) bool {
	return callee.Kind == ast.KindIdentifier && callee.Text() == "RegExp" && l.isDeclaredInDefaultLib(l.target(callee))
}

// regexpConstruction lowers `new RegExp(pattern, flags)`, or `RegExp(pattern, flags)` for
// the runtime function pts_regexp_call: the runtime tells a RegExp pattern from a string.
func (l *lowerer) regexpConstruction(node *ast.Node, extern string) ir.Expr {
	args := node.Arguments()
	if l.hasSpread(args) {
		l.notSupportedYet(node, "spread arguments of `RegExp` are")
		return nil
	}
	values := l.expressions(args)
	if values == nil && len(args) > 0 {
		return nil
	}
	operands := []ir.Expr{box(&ir.UndefinedConst{}), box(&ir.UndefinedConst{})}
	for i, value := range values {
		operands[i] = box(value) // tsc allows two arguments
	}
	return l.callExtern(extern, l.regexpClass, operands...)
}

// regexpCall lowers a call of RegExp, of RegExp.escape or of a method of a RegExp;
// handled is false for any other call.
func (l *lowerer) regexpCall(call *ast.CallExpression) (x ir.Expr, handled bool) {
	callee := ast.SkipParentheses(call.Expression)
	if l.isRegExpConstructor(callee) {
		return l.regexpConstruction(call.AsNode(), "pts_regexp_call"), true
	}
	if callee.Kind != ast.KindPropertyAccessExpression {
		return nil, false
	}
	switch member := l.libMemberName(callee); member {
	case "RegExpConstructor.escape":
		text := l.stringArgument(call)
		if text == nil {
			return nil, true
		}
		return l.callExtern("pts_regexp_escape", ir.String, text), true
	case "RegExp.exec", "RegExp.test", "RegExp.toString":
		return l.regexpMethodCall(call, callee, strings.TrimPrefix(member, "RegExp.")), true
	case "RegExp.compile":
		l.notSupportedYet(callee, "the deprecated `compile` of RegExp is")
		return nil, true
	}
	return nil, false
}

// regexpMethodCall lowers re.exec(s), re.test(s) and re.toString().
func (l *lowerer) regexpMethodCall(call *ast.CallExpression, callee *ast.Node, name string) ir.Expr {
	regexp := l.regexpReceiver(callee, name)
	if regexp == nil {
		return nil
	}
	if name == "toString" {
		return l.callExtern("pts_regexp_to_string", ir.String, regexp)
	}
	subject := l.stringArgument(call)
	if subject == nil {
		return nil
	}
	if name == "test" {
		return l.callExtern("pts_regexp_test", ir.Bool, regexp, subject)
	}
	result := l.irType(l.typeAt(call.AsNode()), call.AsNode()) // RegExpExecArray | null: a union, held as a dynamic value
	if result == nil {
		return nil
	}
	return l.callExtern("pts_regexp_exec", result, regexp, subject)
}

// regexpReceiver lowers the RegExp whose member a property access reads, or reports it.
func (l *lowerer) regexpReceiver(access *ast.Node, name string) ir.Expr {
	regexp := l.expression(access.Expression())
	if regexp == nil {
		return nil
	}
	if regexp.Type() != l.regexpClass {
		l.notSupportedYet(access, fmt.Sprintf("`%s` of a %s is", name, regexp.Type()))
		return nil
	}
	return regexp
}

// stringArgument lowers a call's one argument, a string; nil if the call has none.
func (l *lowerer) stringArgument(call *ast.CallExpression) ir.Expr {
	if l.hasSpreadArgument(call) {
		return nil
	}
	args := call.Arguments.Nodes
	if len(args) == 0 {
		l.notSupportedYet(call.AsNode(), "leaving out the string argument is")
		return nil
	}
	value := l.expression(args[0])
	if value == nil {
		return nil
	}
	return l.toString(value, args[0])
}

// regexpProperty lowers a property of a RegExp other than lastIndex (a field), of a match
// array (index, input, groups) or of RegExp itself (the legacy statics, left out);
// handled is false for any other property.
func (l *lowerer) regexpProperty(node *ast.Node) (x ir.Expr, handled bool) {
	owner, name, ok := strings.Cut(l.libMemberName(node), ".")
	if !ok {
		return nil, false
	}
	switch owner {
	case "RegExp":
		if name == "lastIndex" {
			return nil, false
		}
		return l.regexpGetter(node, name), true
	case "RegExpExecArray", "RegExpMatchArray":
		return l.matchArrayProperty(node, name), true
	case "RegExpConstructor":
		l.notSupportedYet(node, "the legacy statics of RegExp (`RegExp."+name+"`) are")
		return nil, true
	}
	return nil, false
}

// regexpGetter lowers re.source, re.flags and the flag properties.
func (l *lowerer) regexpGetter(node *ast.Node, name string) ir.Expr {
	regexp := l.regexpReceiver(node, name)
	if regexp == nil {
		return nil
	}
	switch name {
	case "source", "flags":
		return l.callExtern("pts_regexp_"+name, ir.String, regexp)
	case "unicodeSets":
		return discard(regexp, &ir.BoolConst{Value: false}) // the v flag isn't supported (D-132)
	}
	if flag, ok := regexpFlags[name]; ok {
		return l.callExtern("pts_regexp_has_flag", ir.Bool, regexp, &ir.NumberConst{Value: flag})
	}
	l.notSupportedYet(node, "the RegExp property `"+name+"` is")
	return nil
}

// matchArrayProperty lowers m.index, m.input and m.groups of a match array, which the
// runtime reads as dynamic values (undefined for an array exec didn't make).
func (l *lowerer) matchArrayProperty(node *ast.Node, name string) ir.Expr {
	if name == "indices" {
		l.notSupportedYet(node, "the `indices` of a match (the d flag) are")
		return nil
	}
	array := l.expression(node.Expression())
	if array == nil {
		return nil
	}
	t := l.irType(l.typeAt(node), node)
	if t == nil {
		return nil
	}
	if _, ok := t.(*ir.Union); ok || t == ir.Value {
		return l.callExtern("pts_match_"+name, t, array) // groups' `Record<string, string> | undefined`, held as a dynamic value
	}
	return l.convert(l.callExtern("pts_match_"+name, ir.Value, array), t, node)
}

// isMatchElement reports whether node is m[i] of a match array, for an i that may be a
// group's (not 0, the whole match, which is always there).
func (l *lowerer) isMatchElement(node *ast.Node) bool {
	if node.Kind != ast.KindElementAccessExpression || !l.isMatchArrayType(l.typeAt(node.Expression())) {
		return false
	}
	index := ast.SkipParentheses(node.AsElementAccessExpression().ArgumentExpression)
	return index.Kind != ast.KindNumericLiteral || index.Text() != "0"
}

// matchElement lowers m[i] of a match array, which is undefined for a group that took no
// part though tsc types it a string: a `string | undefined` where the context tests it
// for undefined (`m[2] || "px"`, `m[2] === undefined`) or makes a string of it (a
// template), as a loose local is (D-090); elsewhere a string, checked.
func (l *lowerer) matchElement(node *ast.Node) ir.Expr {
	x := l.valueExpression(node)
	if x == nil {
		return nil
	}
	if l.isTestedOperand(node) || l.contextTakesUndefined(node) || node.Parent.Kind == ast.KindTemplateSpan {
		return l.convert(x, l.withUndefined(ir.String), node)
	}
	return l.checkedConversion(x, ir.String, node)
}

// isMatchArrayType reports whether t is the lib's RegExpExecArray or RegExpMatchArray,
// whose values are string[]s (D-133).
func (l *lowerer) isMatchArrayType(t *checker.Type) bool {
	symbol := t.Symbol()
	if t.Flags()&checker.TypeFlagsObject == 0 || symbol == nil || (symbol.Name != "RegExpExecArray" && symbol.Name != "RegExpMatchArray") {
		return false
	}
	return len(symbol.Declarations) > 0 && l.program.IsSourceFileDefaultLibrary(ast.GetSourceFileOfNode(symbol.Declarations[0]).Path())
}

// isMatchArrayOrEmpty reports whether t is a union of match array types and the empty
// tuple, which `re.exec(s) ?? []` makes: a string[].
func (l *lowerer) isMatchArrayOrEmpty(t *checker.Type) bool {
	if t.Flags()&checker.TypeFlagsUnion == 0 {
		return false
	}
	for _, member := range t.Types() {
		if !l.isMatchArrayType(member) && !(checker.IsTupleType(member) && len(member.TargetTupleType().ElementFlags()) == 0) {
			return false
		}
	}
	return true
}

// isArrayType reports whether t is an array type: tsc's, or a match array's (D-133).
func (l *lowerer) isArrayType(t *checker.Type) bool {
	return l.checker.IsArrayType(t) || l.isMatchArrayType(t) || l.isMatchArrayOrEmpty(t)
}
