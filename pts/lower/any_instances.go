package lower

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/pts/lib"
)

// tsc's C<any, …>, for a generic class C of the program, is the type `x instanceof C`
// narrows to where x's declared type doesn't name C (D-218), and the one a place gets
// where such a value flows on (`const m = x`, `[x]`, `ident(x)`, an inferred return, the
// element `find` gives with an inferred predicate) or where the program writes it
// (`b: Box<any>`). In TypeScript it holds any of C's instances; in PTS each instance is a
// class of its own (D-050), so such a place is a dynamic value, as an `unknown` narrowed
// to C is: its members are found at run time, and an instance of C goes in as it is (H10b).
// Code that needs the class itself (`new C<any>()`, a superclass, `this`, `super`) still
// gets C's instance for any (classOf).

// isAnyInstance reports whether t is tsc's C<any, …> for a generic class C of the program,
// not of the library (`ParallelArray<any>`, whose instance for any the library's own code
// uses as a class).
func (l *lowerer) isAnyInstance(t *checker.Type) bool {
	if !l.isPrototypeInstance(t) {
		return false
	}
	file := ast.GetSourceFileOfNode(l.genericClasses[l.checker.GetMergedSymbol(t.Symbol())].node).FileName()
	return !lib.IsLibPath(file) && !lib.IsModulePath(file)
}

// holdsAnyInstance reports whether node gives a value that tsc types C<any, …>
// (isAnyInstance), or an intersection irType takes as one (`S & C<any, …>`, which
// `instanceof` narrows a type parameter's value to), held as a dynamic value: not `super`,
// whose class the subclass extends, nor a reference whose instanceof narrowing implies C's
// type arguments, which typeAt gives as their instance (D-218).
func (l *lowerer) holdsAnyInstance(node *ast.Node) bool {
	node = ast.SkipParentheses(node)
	if node.Kind == ast.KindSuperKeyword {
		return false
	}
	t := l.typeAt(node)
	if t.Flags()&checker.TypeFlagsIntersection != 0 {
		if member := l.coveringMember(t); member != nil {
			t = member
		}
	}
	return l.isAnyInstance(t)
}
