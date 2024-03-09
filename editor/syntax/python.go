package syntax

var syntaxPython = &EditorSyntax{
	Filetype:  "python",
	Filematch: []string{".py"},
	Keywords: []string{
		"as", "assert", "break", "class", "continue", "def", "del",
		"elif", "else", "except", "finally", "for", "from", "global",
		"if", "import", "in", "lambda", "nonlocal", "pass", "raise",
		"return", "try", "while", "with", "yield",

		"and|", "False|", "is|", "None|", "not|", "or|", "True|",
		"int|", "float|", "bool|", "str|", "abs|", "all|", "any|",
		"ascii|", "bin|", "bytearray|", "bytes|", "callable|", "chr|",
		"classmethod|", "compile|", "complex|", "delattr|", "dict|",
		"dir|", "divmod|", "enumerate|", "eval|", "exec|", "filter|",
		"format|", "frozenset|", "getattr|", "globals|", "hasattr|",
		"hash|", "help|", "hex|", "id|", "input|", "isinstance|",
		"issubclass|", "iter|", "len|", "list|", "locals|", "map|",
		"max|", "memoryview|", "min|", "next|", "object|", "oct|",
		"open|", "ord|", "pow|", "print|", "property|", "range|",
		"repr|", "reversed|", "round|", "set|", "setattr|", "slice|",
		"sorted|", "staticmethod|", "sum|", "super|", "tuple|", "type|",
		"vars|", "zip|",
	},
	Scs:     "#",
	Mcs:     "",
	Mce:     "",
	Flags:   HL_HIGHLIGHT_NUMBERS | HL_HIGHLIGHT_STRINGS,
	Tabstop: 4,
}
