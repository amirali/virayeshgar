package syntax

const (
	HlNormal uint8 = iota
	HlComment
	HlMlComment
	HlKeyword1
	HlKeyword2
	HlString
	HlNumber
	HlMatch
)

const (
	HL_HIGHLIGHT_NUMBERS = 1 << iota
	HL_HIGHLIGHT_STRINGS
)

type EditorSyntax struct {
	Filetype  string
	Filematch []string
	Keywords  []string
	// single line comment section
	Scs string
	// multi line comment start pattern
	Mcs string
	// multi line comment end pattern
	Mce string
	// Bit field that contains Flags for whether to highlight numbers and
	// whether to highlight strings.
	Flags int
	// \t representation based of file syntax
	Tabstop int
}

var HLDB = []*EditorSyntax{
	syntaxGo, syntaxLua, syntaxPython,
}

func SyntaxToColor(hl uint8) string {
	switch hl {
	case HlComment, HlMlComment:
		return "0;90"
	case HlKeyword1:
		return "0;94"
	case HlKeyword2:
		return "0;96"
	case HlString:
		return "0;36"
	case HlNumber:
		return "0;33"
	case HlMatch:
		return "1;92"
	default:
		return "0;37"
	}
}
