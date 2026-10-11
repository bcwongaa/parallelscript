package lower

import (
	"fmt"
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/pts/ir"
)

// Date (D-087) is a builtin class the runtime defines (pts_date: a time value). Its
// constructor's forms, its statics and its methods are runtime functions that calls
// reach directly; the methods' fields and formats are numbers the runtime switches on.

// dateFields are the fields Date's getters read and its setters write from, as
// pts_date_get and pts_date_set number them (the UTC ones are the local ones plus 8).
var dateFields = map[string]float64{
	"Time": 0, "TimezoneOffset": 1, "FullYear": 2, "Month": 3, "Date": 4, "Day": 5, "Hours": 6, "Minutes": 7, "Seconds": 8, "Milliseconds": 9,
}

const dateUTCFields = 8

// dateFormats are the string methods of Date and the formats pts_date_format numbers.
var dateFormats = map[string]float64{
	"toString": 0, "toDateString": 1, "toTimeString": 2, "toISOString": 3, "toUTCString": 4, "toGMTString": 4,
}

// dateFunctions are the runtime functions behind Date, made once per program.
type dateFunctions struct {
	newNow, newTime, newString, newValue, newComponents *ir.Func
	now, utc, parse                                     *ir.Func
	get, setTime, set, format, toJSON                   *ir.Func
}

// declareDateClass makes the builtin class of the lib's Date, which the runtime defines,
// and the functions that implement it.
func (l *lowerer) declareDateClass() {
	symbol := l.checker.GetGlobalSymbol("Date", ast.SymbolFlagsValue, nil)
	class := &ir.Class{Name: "Date", Builtin: true}
	l.classes[symbol] = class
	l.builtinClasses = append(l.builtinClasses, class)
	l.dateClass = class
	extern := func(name, c string, result ir.Type, params ...ir.Type) *ir.Func {
		fn := &ir.Func{Name: "Date." + name, Result: result, Extern: c}
		for i, t := range params {
			fn.Params = append(fn.Params, &ir.Local{Name: fmt.Sprintf("p%d", i), Type: t})
		}
		return fn
	}
	n := ir.Number
	l.dates = &dateFunctions{
		newNow:        extern("new", "pts_date_new_now", class),
		newTime:       extern("new", "pts_date_new_time", class, n),
		newString:     extern("new", "pts_date_new_string", class, ir.String),
		newValue:      extern("new", "pts_date_new_value", class, ir.Value),
		newComponents: extern("new", "pts_date_new_components", class, n, n, n, n, n, n, n, n),
		now:           extern("now", "pts_date_now", n),
		utc:           extern("UTC", "pts_date_utc", n, n, n, n, n, n, n, n, n),
		parse:         extern("parse", "pts_date_parse", n, ir.String),
		get:           extern("get", "pts_date_get", n, class, n),
		setTime:       extern("setTime", "pts_date_set_time", n, class, n),
		set:           extern("set", "pts_date_set", n, class, n, n, n, n, n, n),
		format:        extern("format", "pts_date_format", ir.String, class, n),
		toJSON:        extern("toJSON", "pts_date_to_json", ir.Value, class),
	}
}

// isDateConstructor reports whether a callee is the global Date.
func (l *lowerer) isDateConstructor(callee *ast.Node) bool {
	return callee.Kind == ast.KindIdentifier && callee.Text() == "Date" && l.isDeclaredInDefaultLib(l.target(callee))
}

// newDate lowers `new Date(…)`: now; a time value, a string to parse or another date;
// or local components, year and month first.
func (l *lowerer) newDate(node *ast.Node) ir.Expr {
	args := node.Arguments()
	if l.hasSpread(args) {
		l.notSupportedYet(node, "spread arguments of `new Date` are")
		return nil
	}
	values := l.expressions(args)
	if values == nil && len(args) > 0 {
		return nil
	}
	if len(values) == 0 {
		return &ir.Call{Func: l.dates.newNow}
	}
	if len(values) == 1 {
		switch t := values[0].Type(); {
		case ir.IsNumeric(t):
			return &ir.Call{Func: l.dates.newTime, Args: []ir.Expr{toNumber(values[0])}}
		case t == ir.String:
			return &ir.Call{Func: l.dates.newString, Args: values}
		case t == l.dateClass, t == ir.Value, isUnion(t):
			return &ir.Call{Func: l.dates.newValue, Args: []ir.Expr{box(values[0])}}
		}
		l.notSupportedYet(args[0], fmt.Sprintf("`new Date` of a %s is", values[0].Type()))
		return nil
	}
	components := l.dateComponents(values, args)
	if components == nil {
		return nil
	}
	return &ir.Call{Func: l.dates.newComponents, Args: components}
}

// dateComponents are the arguments of the runtime's functions that take a date's
// components: their count, then seven numbers (NaN for each left out).
func (l *lowerer) dateComponents(values []ir.Expr, at []*ast.Node) []ir.Expr {
	args := []ir.Expr{&ir.NumberConst{Value: float64(len(values))}}
	for i := range 7 {
		if i >= len(values) {
			args = append(args, fromStart)
			continue
		}
		value := l.toNumberOrReport(values[i], at[i])
		if value == nil {
			return nil
		}
		args = append(args, value)
	}
	return args
}

// hasSpread reports whether one of nodes is a spread element.
func (l *lowerer) hasSpread(nodes []*ast.Node) bool {
	for _, node := range nodes {
		if isSpreadElement(node) {
			return true
		}
	}
	return false
}

// dateCall lowers a call of a static or a method of Date; handled is false for any other call.
func (l *lowerer) dateCall(call *ast.CallExpression) (x ir.Expr, handled bool) {
	callee := ast.SkipParentheses(call.Expression)
	if callee.Kind != ast.KindPropertyAccessExpression {
		if l.isDateConstructor(callee) {
			l.notSupportedYet(call.AsNode(), "calling `Date` without `new` is")
			return nil, true
		}
		return nil, false
	}
	member := l.libMemberName(callee)
	name := callee.Name().Text()
	switch {
	case member == "DateConstructor.now":
		return &ir.Call{Func: l.dates.now}, true
	case member == "DateConstructor.parse" || member == "DateConstructor.UTC":
		if l.hasSpreadArgument(call) {
			return nil, true
		}
		values := l.expressions(call.Arguments.Nodes)
		if values == nil {
			return nil, true
		}
		if name == "parse" {
			text := l.toString(values[0], call.Arguments.Nodes[0])
			if text == nil {
				return nil, true
			}
			return &ir.Call{Func: l.dates.parse, Args: []ir.Expr{text}}, true
		}
		components := l.dateComponents(values, call.Arguments.Nodes)
		if components == nil {
			return nil, true
		}
		return &ir.Call{Func: l.dates.utc, Args: components}, true
	case strings.HasPrefix(member, "Date."):
		return l.dateMethodCall(call, callee, name), true
	}
	return nil, false
}

// dateMethodCall lowers d.m(…) for a method m of Date.
func (l *lowerer) dateMethodCall(call *ast.CallExpression, callee *ast.Node, name string) ir.Expr {
	if l.hasSpreadArgument(call) {
		return nil
	}
	date := l.expression(callee.Expression())
	if date == nil {
		return nil
	}
	if date.Type() != l.dateClass {
		l.notSupportedYet(callee, fmt.Sprintf("`%s` of a %s is", name, date.Type()))
		return nil
	}
	values := l.expressions(call.Arguments.Nodes)
	if values == nil && len(call.Arguments.Nodes) > 0 {
		return nil
	}
	field := func(get string) (float64, bool) {
		if f, ok := dateFields[strings.TrimPrefix(get, "UTC")]; ok {
			if strings.HasPrefix(get, "UTC") {
				f += dateUTCFields
			}
			return f, true
		}
		return 0, false
	}
	switch {
	case name == "getTime" || name == "valueOf":
		return &ir.Call{Func: l.dates.get, Args: []ir.Expr{date, &ir.NumberConst{Value: 0}}}
	case strings.HasPrefix(name, "get"):
		if f, ok := field(strings.TrimPrefix(name, "get")); ok {
			return &ir.Call{Func: l.dates.get, Args: []ir.Expr{date, &ir.NumberConst{Value: f}}}
		}
	case name == "setTime":
		time := l.toNumberOrReport(values[0], call.Arguments.Nodes[0])
		if time == nil {
			return nil
		}
		return &ir.Call{Func: l.dates.setTime, Args: []ir.Expr{date, time}}
	case strings.HasPrefix(name, "set"):
		f, ok := field(strings.TrimPrefix(name, "set"))
		if !ok || f == dateFields["Day"] || f == dateFields["TimezoneOffset"] {
			break
		}
		args := []ir.Expr{date, &ir.NumberConst{Value: f}, &ir.NumberConst{Value: float64(len(values))}}
		for i := range 4 {
			if i >= len(values) {
				args = append(args, fromStart)
				continue
			}
			value := l.toNumberOrReport(values[i], call.Arguments.Nodes[i])
			if value == nil {
				return nil
			}
			args = append(args, value)
		}
		return &ir.Call{Func: l.dates.set, Args: args}
	case name == "toJSON":
		return &ir.Call{Func: l.dates.toJSON, Args: []ir.Expr{date}}
	}
	if format, ok := dateFormats[name]; ok {
		return &ir.Call{Func: l.dates.format, Args: []ir.Expr{date, &ir.NumberConst{Value: format}}}
	}
	l.notSupportedYet(callee, "the Date method `"+name+"` is")
	return nil
}



// dateTime is the time value of a lowered date.
func (l *lowerer) dateTime(date ir.Expr) ir.Expr {
	return &ir.Call{Func: l.dates.get, Args: []ir.Expr{date, &ir.NumberConst{Value: 0}}}
}
