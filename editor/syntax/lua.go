package syntax

var syntaxLua = &EditorSyntax{
	Filetype:  "lua",
	Filematch: []string{".lua"},
	Keywords: []string{
		"end", "in", "repeat", "break", "local", "return", "do", "for",
		"then", "else", "function", "elseif", "if", "until", "while",

		"and|", "false|", "nil|", "not|", "true|", "or|",
	},
	Scs:     "--",
	Mcs:     "--[[",
	Mce:     "--]]",
	Flags:   HL_HIGHLIGHT_NUMBERS | HL_HIGHLIGHT_STRINGS,
	Tabstop: 2,
}
