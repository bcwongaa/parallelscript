package lower

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
)

// Extraction must preserve the binding's declared contract. A narrowed apparent
// type at a read can hide the conversion that its alias initializer would make.
func (l *lowerer) containerReadType(read *ast.Node, steps *int, depth int) *checker.Type {
	var path []string
	for {
		if !containerStep(steps, &depth) {
			return nil
		}
		if read.Kind == ast.KindParenthesizedExpression {
			read = read.Expression()
			continue
		}
		if !ast.IsAccessExpression(read) {
			break
		}
		key, ok := literalProjectionKey(read)
		if !ok {
			return nil
		}
		path = append(path, key)
		read = read.Expression()
	}
	_, symbol := l.containerBinding(read)
	if symbol == nil {
		return nil
	}
	contract := l.checker.GetTypeOfSymbol(symbol)
	for i := len(path) - 1; i >= 0; i-- {
		if !containerStep(steps, &depth) || contract == nil || contract.Flags()&checker.TypeFlagsObject == 0 {
			return nil
		}
		if l.checker.IsArrayType(contract) {
			contract = l.checker.GetElementTypeOfArrayType(contract)
		} else {
			property := l.checker.GetPropertyOfType(contract, path[i])
			if property == nil {
				return nil
			}
			contract = l.checker.GetTypeOfSymbol(property)
		}
	}
	return contract
}
