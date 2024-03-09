package syntax

var syntaxGo = &EditorSyntax{
	Filetype:  "go",
	Filematch: []string{".go"},
	Keywords: []string{
		"break", "default", "func", "interface", "select", "case", "defer",
		"go", "map", "struct", "chan", "else", "goto", "package", "switch",
		"const", "fallthrough", "if", "range", "type", "continue", "for",
		"import", "return", "var",

		"append|", "bool|", "byte|", "cap|", "close|", "complex|",
		"complex64|", "complex128|", "error|", "uint16|", "copy|", "false|",
		"float32|", "float64|", "imag|", "int|", "int8|", "int16|",
		"uint32|", "int32|", "int64|", "iota|", "len|", "make|", "new|",
		"nil|", "panic|", "uint64|", "print|", "println|", "real|",
		"recover|", "rune|", "string|", "true|", "uint|", "uint8|",
		"uintptr|", "any|",
	},
	Scs:     "//",
	Mcs:     "/*",
	Mce:     "*/",
	Flags:   HL_HIGHLIGHT_NUMBERS | HL_HIGHLIGHT_STRINGS,
	Tabstop: 4,
}
