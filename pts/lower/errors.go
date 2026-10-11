package lower

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/pts/ir"
)

// nativeErrors are the native error classes (D-068), Error first.
var nativeErrors = []string{"Error", "TypeError", "RangeError", "ReferenceError", "SyntaxError", "EvalError", "URIError"}

// declareErrorClasses makes the builtin classes of Error and its native subclasses, the
// classes of the lib's declarations of them, which the runtime defines (D-068). Error's
// fields are the lib's properties name, message and cause, and a private one for the
// stack, which the lib's stack property reads and writes through the runtime's getter
// and setter; its toString is the runtime's too.
func (l *lowerer) declareErrorClasses() {
	var base *ir.Class
	for _, name := range nativeErrors {
		symbol := l.checker.GetGlobalSymbol(name, ast.SymbolFlagsValue, nil)
		class := &ir.Class{Name: name, Parent: base, Builtin: true}
		if base == nil {
			base = class
			l.declareErrorMembers(class, symbol)
		}
		class.Constructor = l.errorConstructor(class)
		l.classes[symbol] = class
		l.errorClasses = append(l.errorClasses, class)
	}
}

func (l *lowerer) declareErrorMembers(class *ir.Class, symbol *ast.Symbol) {
	errorType := l.checker.GetDeclaredTypeOfSymbol(symbol)
	for _, member := range []struct {
		name string
		t    ir.Type
	}{{"name", ir.String}, {"message", ir.String}, {"cause", ir.Value}} {
		field := &ir.Field{Name: member.name, Type: member.t, Class: class, Optional: member.name == "cause"}
		class.Fields = append(class.Fields, field)
		l.members.fields[l.checker.GetPropertyOfType(errorType, member.name).ValueDeclaration] = field
	}
	class.Fields = append(class.Fields, &ir.Field{Name: "#stack", Type: ir.Value, Class: class})
	stackType := l.unionOf([]ir.Type{ir.String, ir.Undefined})
	getter := l.externMethod(class, "get_stack", "pts_error_get_stack", stackType)
	setter := l.externMethod(class, "set_stack", "pts_error_set_stack", ir.Void, &ir.Local{Name: "stack", Type: stackType})
	toString := l.externMethod(class, "toString", "pts_error_to_string", ir.String)
	class.Methods = []*ir.Method{
		{Name: "stack", Kind: ir.Getter, Class: class, Func: getter},
		{Name: "stack", Kind: ir.Setter, Class: class, Func: setter},
		{Name: "toString", Kind: ir.Plain, Class: class, Func: toString},
	}
	l.members.abstractFields[l.checker.GetPropertyOfType(errorType, "stack").ValueDeclaration] = property{getter: class.Methods[0], setter: class.Methods[1]}
}

// externMethod makes the function of a method of a builtin class that the runtime function extern implements.
func (l *lowerer) externMethod(class *ir.Class, name string, extern string, result ir.Type, params ...*ir.Local) *ir.Func {
	this := &ir.Local{Name: "this", Type: class}
	return &ir.Func{Name: class.Name + "." + name, Params: append([]*ir.Local{this}, params...), Result: result, Extern: extern}
}

// errorConstructor is the constructor of a native error class, the runtime's
// pts_error_init, which takes the lib's (message?: string, options?: ErrorOptions) with a
// Given flag for each (D-055).
func (l *lowerer) errorConstructor(class *ir.Class) *ir.Func {
	symbol := l.checker.GetGlobalSymbol("ErrorOptions", ast.SymbolFlagsType, nil)
	l.libObjectTypes[symbol] = true
	options := l.irType(l.checker.GetDeclaredTypeOfSymbol(symbol), nil)
	return l.externMethod(class, "constructor", "pts_error_init", ir.Void,
		&ir.Local{Name: "message", Type: ir.String}, &ir.Local{Name: "message_given", Type: ir.Given},
		&ir.Local{Name: "options", Type: options}, &ir.Local{Name: "options_given", Type: ir.Given})
}
