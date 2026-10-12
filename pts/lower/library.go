package lower

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/pts/ir"
	"github.com/microsoft/typescript-go/pts/lib"
)

// The pts: modules are sources in ParallelScript, compiled with the programs that import
// them (D-116). They reach the runtime through intrinsics: functions a module declares
// without a body (`declare function pts_process_cwd(): string;`), each the runtime's C
// function of that name, which a call calls directly with arguments of the declared types.

// globalModuleBindings maps each global that is a pts: module's default export
// (lib.GlobalModules) to the module-scope binding the module exports, for the modules
// among modules (the front end puts them there if the program uses the global).
func (l *lowerer) globalModuleBindings(modules []*ast.SourceFile) map[*ast.Symbol]*ast.Symbol {
	bindings := map[*ast.Symbol]*ast.Symbol{}
	for name, specifier := range lib.GlobalModules {
		global := l.checker.GetGlobalSymbol(name, ast.SymbolFlagsValue, nil)
		for _, module := range modules {
			if module.FileName() == lib.ModulePath(specifier) && global != nil {
				bindings[global] = module.Locals[name]
			}
		}
	}
	return bindings
}

// intrinsicCall lowers a call of an intrinsic; handled is false for other calls.
func (l *lowerer) intrinsicCall(call *ast.CallExpression) (x ir.Expr, handled bool) {
	callee := ast.SkipParentheses(call.Expression)
	if callee.Kind != ast.KindIdentifier {
		return nil, false
	}
	symbol := l.checker.GetSymbolAtLocation(callee)
	declaration := intrinsicDeclaration(symbol)
	if declaration == nil {
		return nil, false
	}
	fn, ok := l.intrinsics[symbol]
	if !ok {
		fn = l.declareIntrinsic(declaration)
		l.intrinsics[symbol] = fn
	}
	if fn == nil {
		return nil, true // its declaration was reported
	}
	args := l.arguments(call.Arguments.Nodes, fn.Params)
	if args == nil {
		return nil, true
	}
	return &ir.Call{Func: fn, Args: args}, true
}

// intrinsicDeclaration is the declaration of symbol if it is an intrinsic, a function a
// pts: module declares without a body; nil otherwise.
func intrinsicDeclaration(symbol *ast.Symbol) *ast.Node {
	if symbol == nil || symbol.Flags&ast.SymbolFlagsFunction == 0 || len(symbol.Declarations) != 1 {
		return nil
	}
	declaration := symbol.Declarations[0]
	if declaration.Kind != ast.KindFunctionDeclaration || declaration.Body() != nil || !lib.IsModulePath(ast.GetSourceFileOfNode(declaration).FileName()) {
		return nil
	}
	return declaration
}

// declareIntrinsic makes the extern function of an intrinsic's declaration, or reports a
// type it can't lower and returns nil.
func (l *lowerer) declareIntrinsic(declaration *ast.Node) *ir.Func {
	name := declaration.Name().Text()
	fn := &ir.Func{Name: name, Extern: name, Result: l.resultType(declaration)}
	for _, param := range declaration.Parameters() {
		t := l.parameterType(param)
		if t == nil {
			return nil
		}
		fn.Params = append(fn.Params, &ir.Local{Name: param.Name().Text(), Type: t})
	}
	if fn.Result == nil {
		return nil
	}
	return fn
}
