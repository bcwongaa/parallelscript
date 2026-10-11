package cgen

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/microsoft/typescript-go/internal/stringutil"
)

// reservedNames can't name a local: C keywords, and lowercase macros that the headers pts.h includes may define.
var reservedNames = map[string]bool{
	"auto": true, "break": true, "case": true, "char": true, "const": true, "continue": true, "default": true,
	"do": true, "double": true, "else": true, "enum": true, "extern": true, "float": true, "for": true, "goto": true,
	"if": true, "inline": true, "int": true, "long": true, "register": true, "restrict": true, "return": true,
	"short": true, "signed": true, "sizeof": true, "static": true, "struct": true, "switch": true, "typedef": true,
	"union": true, "unsigned": true, "void": true, "volatile": true, "while": true, "asm": true, "typeof": true,
	"bool": true, "true": true, "false": true, "errno": true, "stdin": true, "stdout": true, "stderr": true,
	"main": true, "linux": true, "unix": true, "alignas": true, "alignof": true, "noreturn": true,
	"static_assert": true, "thread_local": true, "offsetof": true, "environ": true,
}

// cIdentifier turns a TypeScript identifier into a C identifier that can't collide with
// C keywords, runtime names (pts_, PTS_, GC_), reserved names (leading _) or macros
// (all-caps names). Names that are fine in C are kept; others get a trailing underscore,
// with characters C doesn't allow spelled out ($ as _S, others as _uXXXX).
func cIdentifier(name string) string {
	var c strings.Builder
	clean := true
	for _, r := range name {
		switch {
		case r < utf8.RuneSelf && (r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)):
			c.WriteRune(r)
		case r == '$':
			c.WriteString("_S")
			clean = false
		default:
			fmt.Fprintf(&c, "_u%04X", r)
			clean = false
		}
	}
	result := c.String()
	if !clean || isReserved(result) {
		result += "_"
	}
	return result
}

func isReserved(name string) bool {
	return reservedNames[name] || strings.HasPrefix(name, "_") || strings.HasPrefix(name, "pts_") ||
		strings.HasPrefix(name, "PTS_") || strings.HasPrefix(name, "GC_") || strings.ToUpper(name) == name
}

// cString writes a JS string value (see ir.StringConst) as a C string literal with the same bytes.
// Printable characters stay readable; everything else is an octal escape.
func cString(value string) string {
	var c strings.Builder
	c.WriteByte('"')
	for i := 0; i < len(value); {
		r, size := stringutil.DecodeJSStringRune(value[i:])
		switch {
		case r == '"' || r == '\\':
			c.WriteByte('\\')
			c.WriteRune(r)
		case r == '\n':
			c.WriteString(`\n`)
		case r == '\t':
			c.WriteString(`\t`)
		case r == '?' && i > 0 && value[i-1] == '?':
			c.WriteString(`\?`) // "??x" would be a trigraph in strict C11
		case r >= ' ' && r < utf8.RuneSelf && r != 0x7f,
			r >= utf8.RuneSelf && r != utf8.RuneError && !stringutil.IsSurrogate(r) && unicode.IsPrint(r):
			c.WriteString(value[i : i+size])
		default:
			for _, b := range []byte(value[i : i+size]) {
				fmt.Fprintf(&c, `\%03o`, b)
			}
		}
		i += size
	}
	c.WriteByte('"')
	return c.String()
}

func isASCII(value string) bool {
	for i := range len(value) {
		if value[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// utf16Length is the JS .length of a string value: code points above U+FFFF count twice.
func utf16Length(value string) int {
	length := 0
	for i := 0; i < len(value); {
		r, size := stringutil.DecodeJSStringRune(value[i:])
		length++
		if r > 0xFFFF {
			length++
		}
		i += size
	}
	return length
}
