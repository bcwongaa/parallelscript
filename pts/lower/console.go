package lower

import (
	"math"
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/pts/ir"
)

// consoleMethods are the builtins of console's methods: info, debug and dirxml are log,
// warn is error and groupCollapsed is group, as in Node (D-088).
var consoleMethods = map[string]*ir.Builtin{
	"log": ir.ConsoleLog, "info": ir.ConsoleLog, "debug": ir.ConsoleLog, "dirxml": ir.ConsoleLog,
	"error": ir.ConsoleError, "warn": ir.ConsoleError,
	"group": ir.ConsoleGroup, "groupCollapsed": ir.ConsoleGroup, "groupEnd": ir.ConsoleGroupEnd,
	"assert": ir.ConsoleAssert, "trace": ir.ConsoleTrace,
	"count": ir.ConsoleCount, "countReset": ir.ConsoleCountReset,
	"time": ir.ConsoleTime, "timeEnd": ir.ConsoleTimeEnd, "timeLog": ir.ConsoleTimeLog,
	"dir": ir.ConsoleDir,
}

// consoleMethodName is the name of the method of console that callee names, or "".
func (l *lowerer) consoleMethodName(callee *ast.Node) string {
	name, ok := strings.CutPrefix(l.libMemberName(callee), "Console.")
	if !ok {
		return ""
	}
	return name
}

// consoleCall lowers a call of a console method: the arguments boxed, as console.log's;
// console.dir's options, an object literal, become its depth and showHidden.
func (l *lowerer) consoleCall(node *ast.Node, name string) ir.Expr {
	call := node.AsCallExpression()
	builtin, ok := consoleMethods[name]
	switch {
	case !ok:
		l.notSupportedYet(node, "`console."+name+"` is")
		return nil
	case builtin == ir.ConsoleGroupEnd:
		return &ir.CallBuiltin{Builtin: builtin}
	case builtin == ir.ConsoleDir:
		return l.consoleDir(call)
	}
	args := l.consoleArguments(call)
	if args == nil {
		return nil
	}
	return &ir.CallBuiltin{Builtin: builtin, Args: args}
}

// consoleArguments lowers the arguments of a console method, each boxed; a spread argument
// passes an array's elements (SpreadArg) or a tuple's. It is nil if one failed.
func (l *lowerer) consoleArguments(call *ast.CallExpression) []ir.Expr {
	args := make([]ir.Expr, 0, len(call.Arguments.Nodes))
	for _, argument := range call.Arguments.Nodes {
		if argument.Kind == ast.KindSpreadElement {
			values := l.argumentValues(argument)
			if values == nil {
				return nil
			}
			for _, value := range values {
				if spread, ok := value.(*spreadValue); ok {
					args = append(args, &ir.SpreadArg{X: spread.x})
				} else {
					args = append(args, box(value))
				}
			}
			continue
		}
		arg := l.valueExpression(argument)
		if arg == nil {
			return nil
		}
		args = append(args, box(arg))
	}
	return args
}

// consoleDir lowers console.dir(value, options): Node's inspect of the value (strings
// quoted), with the options' depth (null for no limit) and showHidden.
func (l *lowerer) consoleDir(call *ast.CallExpression) ir.Expr {
	args := call.Arguments.Nodes
	if l.hasSpreadArgument(call) {
		return nil
	}
	value := ir.Expr(&ir.UndefinedConst{})
	if len(args) > 0 {
		if value = l.valueExpression(args[0]); value == nil {
			return nil
		}
	}
	depth, showHidden := ir.Expr(&ir.NumberConst{Value: 2}), ir.Expr(&ir.BoolConst{Value: false})
	if len(args) > 1 && !isUndefined(args[1]) {
		var ok bool
		if depth, showHidden, ok = l.consoleDirOptions(args[1], depth, showHidden); !ok {
			return nil
		}
	}
	return &ir.CallBuiltin{Builtin: ir.ConsoleDir, Args: []ir.Expr{box(value), depth, showHidden}}
}

// consoleDirOptions lowers console.dir's options, an object literal of depth and
// showHidden, onto their defaults.
func (l *lowerer) consoleDirOptions(options *ast.Node, depth, showHidden ir.Expr) (ir.Expr, ir.Expr, bool) {
	options = ast.SkipParentheses(options)
	if options.Kind != ast.KindObjectLiteralExpression {
		l.notSupportedYet(options, "options of `console.dir` other than an object literal are")
		return nil, nil, false
	}
	for _, property := range options.AsObjectLiteralExpression().Properties.Nodes {
		if property.Kind != ast.KindPropertyAssignment || property.Name().Kind != ast.KindIdentifier {
			l.notSupportedYet(property, "this option of `console.dir` is")
			return nil, nil, false
		}
		initializer := property.Initializer()
		switch property.Name().Text() {
		case "depth":
			if ast.SkipParentheses(initializer).Kind == ast.KindNullKeyword {
				depth = &ir.NumberConst{Value: math.Inf(1)}
			} else if depth = l.expression(initializer); depth != nil {
				depth = l.convert(depth, ir.Number, initializer)
			}
		case "showHidden":
			if showHidden = l.expression(initializer); showHidden != nil {
				showHidden = l.convert(showHidden, ir.Bool, initializer)
			}
		default:
			l.notSupportedYet(property, "the option `"+property.Name().Text()+"` of `console.dir` is")
			return nil, nil, false
		}
		if depth == nil || showHidden == nil {
			return nil, nil, false
		}
	}
	return depth, showHidden, true
}
