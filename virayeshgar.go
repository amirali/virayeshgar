package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/mattn/go-runewidth"
	"golang.org/x/sys/unix"
)

var (
	outfile, _ = os.Create("./virayeshgar.keys.log")
	l          = log.New(outfile, "", 0)
)

var version = "dev"

const tabstop = 8

var (
	stdinfd  = int(os.Stdin.Fd())
	stdoutfd = int(os.Stdout.Fd())
)

var ErrQuitEditor = errors.New("quit editor")
var ErrUnknownMode = errors.New("unknown mode")
var ErrUnknownCommand = errors.New("unknown command")
var ErrUnkownMotion = errors.New("unknown motion")

type Mode struct {
	name          string
	statusMessage string
}

var (
	NormalMode  Mode = Mode{name: "normal", statusMessage: "-- NORMAL --"}
	InsertMode       = Mode{name: "insert", statusMessage: "-- INSERT --"}
	CommandMode      = Mode{name: "command"}
)

type action int

const (
	Cut action = iota
	Paste
	Edit
)

type UndoNode struct {
	undoType   action
	beforeRows []*Row
	afterRows  []*Row
	// NOTE: considering one line changes for now
	fromIdx int
	toIdx   int
}

type Editor struct {
	cx int
	cy int
	rx int

	rowOffset int
	colOffset int

	screenRows int
	screenCols int

	rows []*Row

	dirty int

	quitCounter int

	filename string

	statusmsg     string
	statusmsgTime time.Time

	syntax *EditorSyntax

	origTermios *unix.Termios

	mode           Mode
	command        string
	motionRegister []key
	yankRegister   string

	undoPath []*UndoNode
}

func enableRawMode() (*unix.Termios, error) {
	t, err := unix.IoctlGetTermios(stdinfd, ioctlReadTermios)
	if err != nil {
		return nil, err
	}
	raw := *t
	raw.Iflag &^= unix.BRKINT | unix.INPCK | unix.ISTRIP | unix.IXON
	raw.Cflag |= unix.CS8
	raw.Lflag &^= unix.ECHO | unix.ICANON | unix.ISIG | unix.IEXTEN
	raw.Cc[unix.VMIN] = 0
	raw.Cc[unix.VTIME] = 1
	if err := unix.IoctlSetTermios(stdinfd, ioctlWriteTermios, &raw); err != nil {
		return nil, err
	}
	return t, nil
}

func (e *Editor) Init() error {
	termios, err := enableRawMode()

	if err != nil {
		return err
	}

	e.origTermios = termios
	e.mode = NormalMode
	e.undoPath = make([]*UndoNode, 0)

	ws, err := unix.IoctlGetWinsize(stdoutfd, unix.TIOCGWINSZ)
	if err != nil || ws.Col == 0 {
		if _, err = os.Stdout.Write([]byte("\x1b[999C\x1b[999B")); err != nil {
			return err
		}
		if row, col, err := getCursorPosition(); err == nil {
			e.screenRows = row
			e.screenCols = col
			return nil
		}
		return err
	}
	e.screenRows = int(ws.Row) - 2
	e.screenCols = int(ws.Col)
	return nil
}

func (e *Editor) Close() error {
	if e.origTermios == nil {
		return fmt.Errorf("raw mode is not enabled")
	}
	// restore original termios.
	return unix.IoctlSetTermios(stdinfd, ioctlWriteTermios, e.origTermios)
}

type key int32

// normal mode
const (
	navKeyH key = 104
	navKeyJ key = 106
	navKeyK key = 107
	navKeyL key = 108

	escKey key = 27

	modeKeyI        key = 105
	modeKeySmallA   key = 97
	modeKeyCapitalA key = 65
	modeKeySmallO   key = 111
	modeKeyCapitalO key = 79
	modeKeyCol      key = 58
	modeKeySearch   key = 47
	modeKeyU        key = 117
)

// motion
const (
	motionKeyD        key = 100
	motionKeyY        key = 121
	motionKeySmallP   key = 112
	motionKeyCapitalP key = 80
)

// insert mode
const (
	keyEnter     key = 10
	keyBackspace key = 127

	keyArrowLeft key = iota + 1000
	keyArrowRight
	keyArrowUp
	keyArrowDown
	keyDelete
	keyPageUp
	keyPageDown
	keyHome
	keyEnd
)

const (
	hlNormal uint8 = iota
	hlComment
	hlMlComment
	hlKeyword1
	hlKeyword2
	hlString
	hlNumber
	hlMatch
)

const (
	HL_HIGHLIGHT_NUMBERS = 1 << iota
	HL_HIGHLIGHT_STRINGS
)

type EditorSyntax struct {
	filetype  string
	filematch []string
	keywords  []string
	// single line comment section
	scs string
	// multi line comment start pattern
	mcs string
	// multi line comment end pattern
	mce string
	// Bit field that contains flags for whether to highlight numbers and
	// whether to highlight strings.
	flags int
}

var HLDB = []*EditorSyntax{
	// FIXME: Update list
	{
		filetype:  "go",
		filematch: []string{".go"},
		keywords: []string{
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
			"uintptr|",
		},
		scs:   "//",
		mcs:   "/*",
		mce:   "*/",
		flags: HL_HIGHLIGHT_NUMBERS | HL_HIGHLIGHT_STRINGS,
	},
	{
		filetype:  "lua",
		filematch: []string{".lua"},
		keywords: []string{
			"end", "in", "repeat", "break", "local", "return", "do", "for",
			"then", "else", "function", "elseif", "if", "until", "while",

			"and|", "false|", "nil|", "not|", "true|", "or|",
		},
		scs:   "--",
		mcs:   "--[[",
		mce:   "--]]",
		flags: HL_HIGHLIGHT_NUMBERS | HL_HIGHLIGHT_STRINGS,
	},
	{
		filetype:  "python",
		filematch: []string{".py"},
		keywords: []string{
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
		scs:   "#",
		mcs:   "",
		mce:   "",
		flags: HL_HIGHLIGHT_NUMBERS | HL_HIGHLIGHT_STRINGS,
	},
}

type Row struct {
	// Index within the file.
	idx int
	// Raw character data for the row as an array of runes.
	chars []rune
	// Actual chracters to draw on the screen.
	render string
	// Syntax highlight value for each rune in the render string.
	hl []uint8
	// Indicates whether this row has unclosed multiline comment.
	hasUnclosedComment bool
}

// ctrl returns a byte resulting from pressing the given ASCII character with the ctrl-key.
func ctrl(char byte) byte {
	return char & 0x1f
}

func die(err error) {
	os.Stdout.WriteString("\x1b[2J") // clear the screen
	os.Stdout.WriteString("\x1b[H")  // reposition the cursor
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	os.Exit(1)
}

// readKey reads a key press input from stdin.
func readKey() (key, error) {
	buf := make([]byte, 4)
	for {
		n, err := os.Stdin.Read(buf)
		if err != nil && err != io.EOF {
			return 0, err
		}
		if n > 0 {
			buf = bytes.TrimRightFunc(buf, func(r rune) bool { return r == 0 })
			switch {
			case bytes.Equal(buf, []byte("\x1b[A")):
				return keyArrowUp, nil
			case bytes.Equal(buf, []byte("\x1b[B")):
				return keyArrowDown, nil
			case bytes.Equal(buf, []byte("\x1b[C")):
				return keyArrowRight, nil
			case bytes.Equal(buf, []byte("\x1b[D")):
				return keyArrowLeft, nil
			case bytes.Equal(buf, []byte("\x1b[1~")), bytes.Equal(buf, []byte("\x1b[7~")),
				bytes.Equal(buf, []byte("\x1b[H")), bytes.Equal(buf, []byte("\x1bOH")):
				return keyHome, nil
			case bytes.Equal(buf, []byte("\x1b[4~")), bytes.Equal(buf, []byte("\x1b[8~")),
				bytes.Equal(buf, []byte("\x1b[F")), bytes.Equal(buf, []byte("\x1bOF")):
				return keyEnd, nil
			case bytes.Equal(buf, []byte("\x1b[3~")):
				return keyDelete, nil
			case bytes.Equal(buf, []byte("\x1b[5~")):
				return keyPageUp, nil
			case bytes.Equal(buf, []byte("\x1b[6~")):
				return keyPageDown, nil

			default:
				return key(buf[0]), nil
			}
		}
	}
}

func (e *Editor) MoveCursor(k key) {
	switch k {
	case navKeyK, keyArrowUp:
		if e.cy != 0 {
			e.cy--
		}
	case navKeyJ, keyArrowDown:
		if e.cy < len(e.rows) {
			e.cy++
		}
	case navKeyH, keyArrowLeft:
		if e.cx != 0 {
			e.cx--
		} else if e.cy > 0 {
			e.cy--
			e.cx = len(e.rows[e.cy].chars)
		}
	case navKeyL, keyArrowRight:
		linelen := -1
		if e.cy < len(e.rows) {
			linelen = len(e.rows[e.cy].chars)
		}
		if linelen >= 0 && e.cx < linelen {
			e.cx++
		} else if linelen >= 0 && e.cx == linelen {
			e.cy++
			e.cx = 0
		}
	}

	// If the cursor ends up past the end of the line it's on
	// put the cursor at the end of the line.
	var linelen int
	if e.cy < len(e.rows) {
		linelen = len(e.rows[e.cy].chars)
	}
	if e.cx > linelen {
		e.cx = linelen
	}
}

func (e *Editor) ProcessKeyInsertMode() error {
	k, err := readKey()
	if err != nil {
		return err
	}
	switch k {
	case keyEnter:
		e.InsertNewline()

	case keyBackspace:
		e.DeleteChar()

	case keyDelete:
		if e.cy == len(e.rows)-1 && e.cx == len(e.rows[e.cy].chars) {
			// cursor is on the last row and one past the last character,
			// no more character to delete to the right.
			break
		}
		e.MoveCursor(keyArrowRight)
		e.DeleteChar()

	// case keyArrowLeft, keyArrowDown, keyArrowUp, keyArrowRight:
	// 	e.MoveCursor(k)

	case escKey:
		e.SetMode(NormalMode)

	default:
		e.InsertChar(rune(k))
	}
	// Reset quitCounter to zero if user pressed any key other than Ctrl-Q.
	e.quitCounter = 0
	return nil
}

func keySequenceEqual(a, b []key) bool {
	if len(a) != len(b) {
		return false
	}
	for i, v := range a {
		if v != b[i] {
			return false
		}
	}
	return true
}

func (e *Editor) SetMode(mode Mode) {
	e.mode = mode
	e.SetStatusMessage(mode.statusMessage)

	switch mode {
	case InsertMode:
		// FIXME: WTF?
		newRow := *e.rows[e.cy]
		e.undoPath = append(e.undoPath, &UndoNode{undoType: Edit, fromIdx: e.cy, toIdx: e.cy, beforeRows: []*Row{&newRow}})
	case NormalMode:
		// node := e.undoPath[len(e.undoPath)-1]
		// node.toIdx = e.cy
	}
}

func (e *Editor) ExecuteMotion() error {
	if len(e.motionRegister) > 3 {
		e.motionRegister = []key{}
		return ErrUnkownMotion
	}
	switch {
	case keySequenceEqual(e.motionRegister, []key{motionKeyD, motionKeyD}):
		e.CutRow()
		e.motionRegister = []key{}
	case keySequenceEqual(e.motionRegister, []key{motionKeySmallP}):
		e.PasteRow(e.cy + 1)
		e.motionRegister = []key{}
	case keySequenceEqual(e.motionRegister, []key{motionKeyCapitalP}):
		e.PasteRow(e.cy)
		e.motionRegister = []key{}
	case keySequenceEqual(e.motionRegister, []key{motionKeyY, motionKeyY}):
		e.YankRow()

		e.motionRegister = []key{}
	}
	return nil
}

func insertToSlice[T any](s []T, value T, at int) []T {
	if at >= 0 && at <= len(s) {
		newSlice := make([]T, len(s)+1)
		copy(newSlice[:at], s[:at])
		newSlice[at] = value
		copy(newSlice[at+1:], s[at:])
		s = newSlice
	}

	return s
}

func removeFromSlice[T any](s []T, at int) []T {
	if at >= 0 && at <= len(s) {
		newSlice := make([]T, len(s)-1)
		copy(newSlice[:at], s[:at])
		copy(newSlice[at:], s[at+1:])
		s = newSlice
	}

	return s
}

func (e *Editor) Undo() {
	undoLength := len(e.undoPath)
	if undoLength == 0 {
		e.SetStatusMessage("oldest version")
		return
	}

	lastUndoNode := e.undoPath[undoLength-1]
	e.undoPath = e.undoPath[:undoLength-1]

	switch lastUndoNode.undoType {
	case Cut:
		for i := 0; i < lastUndoNode.toIdx-lastUndoNode.fromIdx+1; i++ {
			e.rows = insertToSlice(e.rows, lastUndoNode.beforeRows[i], lastUndoNode.fromIdx+i)
		}
	case Paste:
		for i := 0; i < lastUndoNode.toIdx-lastUndoNode.fromIdx+1; i++ {
			e.rows = removeFromSlice(e.rows, lastUndoNode.fromIdx+i)
		}
	case Edit:
		l.Printf("before rows: %#v", lastUndoNode.beforeRows[0])
		for i := 0; i < lastUndoNode.toIdx-lastUndoNode.fromIdx+1; i++ {
			l.Println(lastUndoNode.fromIdx+i, i)
			e.rows[lastUndoNode.fromIdx+i] = lastUndoNode.beforeRows[i]
		}
	}
}

func (e *Editor) ProcessKeyNormalMode() error {
	k, err := readKey()
	if err != nil {
		return err
	}
	e.SetStatusMessage("-- NORMAL --")
	l.Printf("%#v\n", k)
	switch k {

	// case navKeyH, navKeyJ, navKeyK, navKeyL, keyArrowLeft, keyArrowDown, keyArrowUp, keyArrowRight:
	case navKeyH, navKeyJ, navKeyK, navKeyL:
		e.MoveCursor(k)

	case modeKeyI:
		e.SetMode(InsertMode)
	case modeKeyCol:
		e.mode = CommandMode
		// FIXME: Get rid of :
		e.command = ":"
		e.SetStatusMessage(e.command)

	case modeKeySmallA:
		e.MoveCursor(navKeyL)
		e.SetMode(InsertMode)

	case modeKeyCapitalA:
		if e.cy < len(e.rows) {
			e.cx = len(e.rows[e.cy].chars)
		}
		e.SetMode(InsertMode)
	case modeKeySmallO:
		if e.cy < len(e.rows) {
			e.cx = len(e.rows[e.cy].chars)
		}
		e.InsertNewline()
		e.SetMode(InsertMode)
	case modeKeyCapitalO:
		// FIXME: Line break upper line when width is less than the line above
		if e.cy < len(e.rows) {
			e.cx = len(e.rows[e.cy].chars)
		}
		e.MoveCursor(navKeyK)
		e.InsertNewline()
		e.SetMode(InsertMode)
	case modeKeySearch:
		err := e.Find()
		if err != nil {
			if err == ErrPromptCanceled {
				e.SetStatusMessage("")
			} else {
				return err
			}
		}
	case modeKeyU:
		e.Undo()
	case escKey:
		e.motionRegister = []key{}
	default:
		e.motionRegister = append(e.motionRegister, k)
		err = e.ExecuteMotion()
		if err != nil {
			e.SetStatusMessage(err.Error())
		}
	}

	e.quitCounter = 0
	return nil
}

func (e *Editor) ProcessKeyCommandMode() error {
	k, err := readKey()
	if err != nil {
		return err
	}
	l.Printf("%#v\n", k)
	switch k {
	case escKey:
		e.mode = NormalMode
		e.command = ""
		e.SetStatusMessage("-- NORMAL --")

	case keyBackspace:
		e.command = e.command[:len(e.command)-1]
		e.SetStatusMessage(e.command)

	case keyEnter:
		// FIXME: Turn back to normal mode after executing command
		return e.ExecuteCommand()

	default:
		e.command += string(rune(k))
		e.SetStatusMessage(e.command)
	}

	e.quitCounter = 0
	return nil
}

func (e *Editor) ProcessKey() error {
	switch e.mode {
	case NormalMode:
		return e.ProcessKeyNormalMode()

	case InsertMode:
		return e.ProcessKeyInsertMode()

	case CommandMode:
		return e.ProcessKeyCommandMode()

	default:
		return ErrUnknownMode
	}
}

func (e *Editor) ExecuteCommand() error {
	var err error

	commandParts := strings.Split(e.command, " ")

	switch commandParts[0] {
	case ":w":
		n, err := e.Save(commandParts[1:]...)
		if err != nil {
			if err == ErrPromptCanceled {
				e.SetStatusMessage("Save aborted")
			} else {
				e.SetStatusMessage("Can't save! I/O error: %s", err.Error())
			}
		} else {
			e.SetStatusMessage("%d bytes written to disk", n)
		}

	case ":q":
		if e.dirty > 0 {
			e.SetStatusMessage("ERROR!!! File has unsaved changes")
			e.command = ""
			return nil
		}
		os.Stdout.WriteString("\x1b[2J") // clear the screen
		os.Stdout.WriteString("\x1b[H")  // reposition the cursor
		return ErrQuitEditor

	case ":q!":
		os.Stdout.WriteString("\x1b[2J") // clear the screen
		os.Stdout.WriteString("\x1b[H")  // reposition the cursor
		return ErrQuitEditor

	case ":wq":
		n, err := e.Save()
		if err != nil {
			if err == ErrPromptCanceled {
				e.SetStatusMessage("Save aborted")
			} else {
				e.SetStatusMessage("Can't save! I/O error: %s", err.Error())
			}
		} else {
			e.SetStatusMessage("%d bytes written to disk", n)
		}
		os.Stdout.WriteString("\x1b[2J") // clear the screen
		os.Stdout.WriteString("\x1b[H")  // reposition the cursor
		return ErrQuitEditor

	case ":syntax":
		for _, syntax := range HLDB {
			if syntax.filetype == commandParts[1] {
				e.syntax = syntax
				for _, row := range e.rows {
					e.updateHighlight(row)
				}
			}
		}

	default:
		e.SetStatusMessage(ErrUnknownCommand.Error())
	}

	e.command = ""

	return err
}

func (e *Editor) drawRows(b *strings.Builder) {
	for y := 0; y < e.screenRows; y++ {
		filerow := y + e.rowOffset
		if filerow >= len(e.rows) {
			if len(e.rows) == 0 && y == e.screenRows/3 {
				welcomeMsg := fmt.Sprintf("Virayeshgar v%s", version)
				if runewidth.StringWidth(welcomeMsg) > e.screenCols {
					welcomeMsg = utf8Slice(welcomeMsg, 0, e.screenCols)
				}
				padding := (e.screenCols - runewidth.StringWidth(welcomeMsg)) / 2
				if padding > 0 {
					b.Write([]byte("~"))
					padding--
				}
				for ; padding > 0; padding-- {
					b.Write([]byte(" "))
				}
				b.WriteString(welcomeMsg)
			} else {
				b.Write([]byte("~"))
			}

		} else {
			var (
				line string
				hl   []uint8
			)
			if runewidth.StringWidth(e.rows[filerow].render) > e.colOffset {
				line = utf8Slice(
					e.rows[filerow].render,
					e.colOffset,
					utf8.RuneCountInString(e.rows[filerow].render))
				hl = e.rows[filerow].hl[e.colOffset:]
			}
			if runewidth.StringWidth(line) > e.screenCols {
				line = runewidth.Truncate(line, e.screenCols, "")
				hl = hl[:utf8.RuneCountInString(line)]
			}
			currentColor := ""          // keep track of color to detect color change
			b.WriteString("\x1b[0;90m") // use inverted colors
			maxLength := len(fmt.Sprint(len(e.rows)))
			b.WriteString(fmt.Sprintf("%*d ", maxLength, e.rows[filerow].idx+1))
			b.WriteString("\x1b[m") // reset all formatting
			for i, r := range []rune(line) {
				if unicode.IsControl(r) {
					// deal with non-printable characters (e.g. Ctrl-A)
					sym := '?'
					if r < 26 {
						sym = '@' + r
					}
					b.WriteString("\x1b[7m") // use inverted colors
					b.WriteRune(sym)
					b.WriteString("\x1b[m") // reset all formatting
					if currentColor != "" {
						// restore the current color
						b.WriteString(fmt.Sprintf("\x1b[%sm", currentColor))
					}
				} else if hl[i] == hlNormal {
					if currentColor != "" {
						currentColor = ""
						b.WriteString("\x1b[39m")
					}
					b.WriteRune(r)
				} else {
					color := syntaxToColor(hl[i])
					if color != currentColor {
						currentColor = color
						b.WriteString(fmt.Sprintf("\x1b[%sm", color))
					}
					b.WriteRune(r)
				}
			}
			b.WriteString("\x1b[39m") // reset to normal color
		}
		b.Write([]byte("\x1b[K")) // clear the line
		b.Write([]byte("\r\n"))
	}
}

func (e *Editor) drawStatusBar(b *strings.Builder) {
	b.Write([]byte("\x1b[7m"))      // switch to inverted colors
	defer b.Write([]byte("\x1b[m")) // switch back to normal formatting
	filename := e.filename
	if utf8.RuneCountInString(filename) == 0 {
		filename = "[No Name]"
	}
	dirtyStatus := ""
	if e.dirty > 0 {
		dirtyStatus = "(modified)"
	}
	lmsg := fmt.Sprintf("%.20s - %d lines - %s", filename, len(e.rows), dirtyStatus)
	if runewidth.StringWidth(lmsg) > e.screenCols {
		lmsg = runewidth.Truncate(lmsg, e.screenCols, "...")
	}
	b.WriteString(lmsg)
	filetype := "no filetype"
	if e.syntax != nil {
		filetype = e.syntax.filetype
	}
	row, col, _ := getCursorPosition()

	motionString := ""
	for _, motion := range e.motionRegister {
		motionString += string(rune(motion))
	}

	rmsg := fmt.Sprintf("%s %s | %d:%d", motionString, filetype, row, col)
	l := runewidth.StringWidth(lmsg)
	for l < e.screenCols {
		if e.screenCols-l == runewidth.StringWidth(rmsg) {
			b.WriteString(rmsg)
			break
		}
		b.Write([]byte(" "))
		l++
	}
	b.Write([]byte("\r\n"))
}

// utf8Slice slice the given string by utf8 character.
func utf8Slice(s string, start, end int) string {
	return string([]rune(s)[start:end])
}

func (e *Editor) drawMessageBar(b *strings.Builder) {
	b.Write([]byte("\x1b[K"))
	msg := e.statusmsg
	if runewidth.StringWidth(msg) > e.screenCols {
		msg = runewidth.Truncate(msg, e.screenCols, "...")
	}
	// show the message if it's less than 5s old.
	if time.Since(e.statusmsgTime) < 5*time.Second {
		b.WriteString(msg)
	}
}

func rowCxToRx(row *Row, cx int) int {
	rx := 0
	idx := cx
	if cx > len(row.chars) {
		idx = len(row.chars) - 1
	}
	for _, r := range row.chars[:idx] {
		if r == '\t' {
			rx += (tabstop) - (rx % tabstop)
		} else {
			rx += runewidth.RuneWidth(r)
		}
	}
	return rx
}

func rowRxToCx(row *Row, rx int) int {
	curRx := 0
	for i, r := range row.chars {
		if r == '\t' {
			curRx += (tabstop) - (curRx % tabstop)
		} else {
			curRx += runewidth.RuneWidth(r)
		}

		if curRx > rx {
			return i
		}
	}
	panic("unreachable")
}

func (e *Editor) scroll() {
	e.rx = 0
	if e.cy < len(e.rows) {
		e.rx = rowCxToRx(e.rows[e.cy], e.cx)
	}
	// scroll up if the cursor is above the visible window.
	if e.cy < e.rowOffset {
		e.rowOffset = e.cy
	}
	// scroll down if the cursor is below the visible window.
	if e.cy >= e.rowOffset+e.screenRows {
		e.rowOffset = e.cy - e.screenRows + 1
	}
	// scroll left if the cursor is left of the visible window.
	if e.rx < e.colOffset {
		e.colOffset = e.rx
	}
	// scroll right if the cursor is right of the visible window.
	if e.rx >= e.colOffset+e.screenCols {
		e.colOffset = e.rx - e.screenCols + 1
	}
}

// Render refreshes the screen.
func (e *Editor) Render() {
	e.scroll()

	var b strings.Builder

	b.Write([]byte("\x1b[?25l")) // hide the cursor
	b.Write([]byte("\x1b[H"))    // reposition the cursor at the top left.

	e.drawRows(&b)
	e.drawStatusBar(&b)
	e.drawMessageBar(&b)

	// position the cursor
	b.WriteString(fmt.Sprintf("\x1b[%d;%dH", (e.cy-e.rowOffset)+1, (e.rx-e.colOffset)+1+len(fmt.Sprint(len(e.rows)))+1))
	// show the cursor
	b.Write([]byte("\x1b[?25h"))
	os.Stdout.WriteString(b.String())
}

func (e *Editor) SetStatusMessage(format string, a ...interface{}) {
	e.statusmsg = fmt.Sprintf(format, a...)
	e.statusmsgTime = time.Now()
}

func getCursorPosition() (row, col int, err error) {
	if _, err = os.Stdout.Write([]byte("\x1b[6n")); err != nil {
		return
	}
	if _, err = fmt.Fscanf(os.Stdin, "\x1b[%d;%d", &row, &col); err != nil {
		return
	}
	return
}

func (e *Editor) rowsToString() string {
	var b strings.Builder
	for _, row := range e.rows {
		b.WriteString(string(row.chars))
		b.WriteRune('\n')
	}
	return b.String()
}

var ErrPromptCanceled = fmt.Errorf("user canceled the input prompt")

// Prompt shows the given prompt in the command bar and get user input
// until to user presses the Enter key to confirm the input or until the user
// presses the Escape key to cancel the input. Returns the user input and nil
// if the user enters the input. Returns an empty string and ErrPromptCancel
// if the user cancels the input.
// It takes an optional callback function, which takes the query string and
// the last key pressed.
func (e *Editor) Prompt(prompt string, cb func(query string, k key)) (string, error) {
	var b strings.Builder
	for {
		e.SetStatusMessage(prompt, b.String())
		e.Render()

		k, err := readKey()
		if err != nil {
			return "", err
		}
		if k == keyDelete || k == keyBackspace || k == key(ctrl('h')) {
			if b.Len() > 0 {
				bytes := []byte(b.String())
				_, size := utf8.DecodeLastRune(bytes)
				b.Reset()
				b.WriteString(string(bytes[:len(bytes)-size]))
			}
		} else if k == key('\x1b') {
			e.SetStatusMessage("")
			if cb != nil {
				cb(b.String(), k)
			}
			return "", ErrPromptCanceled
		} else if k == keyEnter {
			if b.Len() > 0 {
				e.SetStatusMessage("")
				if cb != nil {
					cb(b.String(), k)
				}
				return b.String(), nil
			}
		} else if !unicode.IsControl(rune(k)) && !isArrowKey(k) && unicode.IsPrint(rune(k)) {
			b.WriteRune(rune(k))
		}

		if cb != nil {
			cb(b.String(), k)
		}
	}
}

func isArrowKey(k key) bool {
	return k == keyArrowUp || k == keyArrowRight ||
		k == keyArrowDown || k == keyArrowLeft
}

func (e *Editor) Save(opts ...string) (int, error) {
	if len(opts) > 0 {
		e.filename = opts[0]
	}
	if len(e.filename) == 0 {
		fname, err := e.Prompt("Save as: %s (ESC to cancel)", nil)
		if err != nil {
			return 0, err
		}
		e.filename = fname
		e.selectSyntaxHighlight()
	}

	f, err := os.OpenFile(e.filename, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	n, err := f.WriteString(e.rowsToString())
	if err != nil {
		return 0, err
	}
	e.dirty = 0
	return n, nil
}

// OpenFile opens a file with the given filename.
// If a file does not exist, it returns os.ErrNotExist.
func (e *Editor) OpenFile(filename string) error {
	e.filename = filename
	e.selectSyntaxHighlight()
	f, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := s.Bytes()
		// strip off newline or cariage return
		bytes.TrimRightFunc(line, func(r rune) bool { return r == '\n' || r == '\r' })
		e.InsertRow(len(e.rows), string(line))
	}
	if err := s.Err(); err != nil {
		return err
	}
	e.dirty = 0
	return nil
}

func (e *Editor) InsertRow(at int, chars string) {
	if at < 0 || at > len(e.rows) {
		return
	}
	row := &Row{chars: []rune(chars)}
	row.idx = at
	if at > 0 {
		row.hasUnclosedComment = e.rows[at-1].hasUnclosedComment
	}
	e.updateRow(row)

	e.rows = append(e.rows, &Row{}) // grow the buffer
	copy(e.rows[at+1:], e.rows[at:])
	for i := at + 1; i < len(e.rows); i++ {
		e.rows[i].idx++
	}
	e.rows[at] = row
}

func (e *Editor) CutRow() {
	e.dirty++

	row := e.rows[e.cy]
	e.yankRegister = row.render

	e.undoPath = append(e.undoPath, &UndoNode{undoType: Cut, fromIdx: e.cy, toIdx: e.cy, beforeRows: []*Row{row}, afterRows: []*Row{{}}})

	copy(e.rows[e.cy:], e.rows[e.cy+1:])
	e.rows = e.rows[:len(e.rows)-1]
	for i := e.cy; i < len(e.rows); i++ {
		e.rows[i].idx--
	}
}

func (e *Editor) YankRow() {
	e.dirty++
	row := e.rows[e.cy]
	e.yankRegister = row.render
}

func (e *Editor) PasteRow(at int) {
	e.dirty++
	e.undoPath = append(e.undoPath, &UndoNode{undoType: Paste, fromIdx: at, toIdx: at, afterRows: []*Row{e.rows[at]}, beforeRows: []*Row{{}}})

	e.InsertRow(at, e.yankRegister)
	e.yankRegister = ""
}

func (e *Editor) InsertNewline() {
	e.dirty += 1

	if e.cx == 0 {
		e.InsertRow(e.cy, "")
	} else {
		row := e.rows[e.cy]
		e.InsertRow(e.cy+1, string(row.chars[e.cx:]))
		// reassignment needed since the call to InsertRow
		// invalidates the pointer.
		row = e.rows[e.cy]
		row.chars = row.chars[:e.cx]
		e.updateRow(row)
	}
	e.cy++
	e.cx = 0
}

func (e *Editor) updateRow(row *Row) {
	var b strings.Builder
	col := 0
	for _, r := range row.chars {
		if r == '\t' {
			// each tab must advance the cursor forward at least one column
			b.WriteRune(' ')
			col++
			// append spaces until we get to a tab stop
			for col%tabstop != 0 {
				b.WriteRune(' ')
				col++
			}
		} else {
			b.WriteRune(r)
		}
	}
	row.render = b.String()
	e.updateHighlight(row)
}

func isSeparator(r rune) bool {
	return unicode.IsSpace(r) || strings.IndexRune(",.()+-/*=~%<>[]{}:;", r) != -1
}

func (e *Editor) updateHighlight(row *Row) {
	row.hl = make([]uint8, utf8.RuneCountInString(row.render))
	for i := range row.hl {
		row.hl[i] = hlNormal
	}

	if e.syntax == nil {
		return
	}

	prevSep := true

	// set to the quote when inside of a string.
	// set to zero when outside of a string.
	var strQuote rune

	// indicates whether we are inside a multi-line comment.
	inComment := row.idx > 0 && e.rows[row.idx-1].hasUnclosedComment

	idx := 0
	runes := []rune(row.render)
	for idx < len(runes) {
		r := runes[idx]
		prevHl := hlNormal
		if idx > 0 {
			prevHl = row.hl[idx-1]
		}

		if e.syntax.mcs != "" && e.syntax.mce != "" && strQuote == 0 {
			if inComment {
				row.hl[idx] = hlMlComment
				if strings.HasPrefix(string(runes[idx:]), e.syntax.mce) {
					for j := 0; j < len(e.syntax.mce); j++ {
						row.hl[idx] = hlMlComment
						idx++
					}
					inComment = false
					prevSep = true
					continue
				} else {
					idx++
					continue
				}
			} else if strings.HasPrefix(string(runes[idx:]), e.syntax.mcs) {
				for j := 0; j < len(e.syntax.mcs); j++ {
					row.hl[idx] = hlMlComment
					idx++
				}
				inComment = true
				continue
			}
		}

		if e.syntax.scs != "" && strQuote == 0 && !inComment {
			if strings.HasPrefix(string(runes[idx:]), e.syntax.scs) {
				for idx < len(runes) {
					row.hl[idx] = hlComment
					idx++
				}
				break
			}
		}

		if (e.syntax.flags & HL_HIGHLIGHT_STRINGS) != 0 {
			if strQuote != 0 {
				row.hl[idx] = hlString
				//deal with escape quote when inside a string
				if r == '\\' && idx+1 < len(runes) {
					row.hl[idx+1] = hlString
					idx += 2
					continue
				}
				if r == strQuote {
					strQuote = 0
				}
				idx++
				prevSep = true
				continue
			} else {
				if r == '"' || r == '\'' {
					strQuote = r
					row.hl[idx] = hlString
					idx++
					continue
				}
			}
		}

		if (e.syntax.flags & HL_HIGHLIGHT_NUMBERS) != 0 {
			if unicode.IsDigit(r) && (prevSep || prevHl == hlNumber) ||
				r == '.' && prevHl == hlNumber {
				row.hl[idx] = hlNumber
				idx++
				prevSep = false
				continue
			}
		}

		if prevSep {
			keywordFound := false
			for _, kw := range e.syntax.keywords {
				isKeyword2 := strings.HasSuffix(kw, "|")
				if isKeyword2 {
					kw = strings.TrimSuffix(kw, "|")
				}

				end := idx + utf8.RuneCountInString(kw)
				if end <= len(runes) && kw == string(runes[idx:end]) &&
					(end == len(runes) || isSeparator(runes[end])) {
					keywordFound = true
					hl := hlKeyword1
					if isKeyword2 {
						hl = hlKeyword2
					}
					for idx < end {
						row.hl[idx] = hl
						idx++
					}
					break
				}
			}
			if keywordFound {
				prevSep = false
				continue
			}
		}

		prevSep = isSeparator(r)
		idx++
	}

	changed := row.hasUnclosedComment != inComment
	row.hasUnclosedComment = inComment
	if changed && row.idx+1 < len(e.rows) {
		e.updateHighlight(e.rows[row.idx+1])
	}
}

func syntaxToColor(hl uint8) string {
	switch hl {
	case hlComment, hlMlComment:
		return "0;90"
	case hlKeyword1:
		return "0;94"
	case hlKeyword2:
		return "0;96"
	case hlString:
		return "0;36"
	case hlNumber:
		return "0;33"
	case hlMatch:
		return "1;92"
	default:
		return "0;37"
	}
}

func (e *Editor) selectSyntaxHighlight() {
	e.syntax = nil
	if len(e.filename) == 0 {
		return
	}

	ext := filepath.Ext(e.filename)

	for _, syntax := range HLDB {
		for _, pattern := range syntax.filematch {
			isExt := strings.HasPrefix(pattern, ".")
			if (isExt && pattern == ext) ||
				(!isExt && strings.Index(e.filename, pattern) != -1) {
				e.syntax = syntax
				for _, row := range e.rows {
					e.updateHighlight(row)
				}
				return
			}
		}
	}
}

func (row *Row) insertChar(at int, c rune) {
	if at < 0 || at > len(row.chars) {
		at = len(row.chars)
	}
	row.chars = append(row.chars, 0) // make room
	copy(row.chars[at+1:], row.chars[at:])
	row.chars[at] = c
}

func (row *Row) appendChars(chars []rune) {
	row.chars = append(row.chars, chars...)
}

func (row *Row) deleteChar(at int) {
	if at < 0 || at >= len(row.chars) {
		return
	}
	row.chars = append(row.chars[:at], row.chars[at+1:]...)
}

func (e *Editor) InsertChar(c rune) {
	if e.cy == len(e.rows) {
		e.InsertRow(len(e.rows), "")
	}
	row := e.rows[e.cy]
	row.insertChar(e.cx, c)
	e.updateRow(row)
	e.cx++
	e.dirty++
}

func (e *Editor) DeleteChar() {
	if e.cy == len(e.rows) {
		return
	}
	if e.cx == 0 && e.cy == 0 {
		return
	}
	row := e.rows[e.cy]
	if e.cx > 0 {
		row.deleteChar(e.cx - 1)
		e.updateRow(row)
		e.cx--
		e.dirty++
	} else {
		prevRow := e.rows[e.cy-1]
		e.cx = len(prevRow.chars)
		prevRow.appendChars(row.chars)
		e.updateRow(prevRow)
		e.DeleteRow(e.cy)
		e.cy--
	}
}

func (e *Editor) DeleteRow(at int) {
	if at < 0 || at >= len(e.rows) {
		return
	}
	e.rows = append(e.rows[:at], e.rows[at+1:]...)
	for i := at; i < len(e.rows); i++ {
		e.rows[i].idx--
	}
	e.dirty++
}

// FIXME: Sometimes the patterm match will match the line above the rowOffset
func (e *Editor) Find() error {
	savedCx := e.cx
	savedCy := e.cy
	savedColOffset := e.colOffset
	savedRowOffset := e.rowOffset

	lastMatchRowIndex := -1 // remember the last match row
	searchDirection := 1    // 1 = forward, -1 = backward

	savedHlRowIndex := -1
	savedHl := []uint8(nil)

	onKeyPress := func(query string, k key) {
		if len(savedHl) > 0 {
			copy(e.rows[savedHlRowIndex].hl, savedHl)
			savedHl = []uint8(nil)
		}
		switch k {
		case keyEnter, key('\x1b'):
			lastMatchRowIndex = -1
			searchDirection = 1
			return
		case keyArrowRight, keyArrowDown:
			searchDirection = 1
		case keyArrowLeft, keyArrowUp:
			searchDirection = -1
		default:
			// unless an arrow key was pressed, we'll reset.
			lastMatchRowIndex = -1
			searchDirection = 1
		}

		if lastMatchRowIndex == -1 {
			searchDirection = 1
		}

		current := lastMatchRowIndex

		// search for query and set e.cy, e.cx, e.rowOffset values.
		for i := 0; i < len(e.rows); i++ {
			current += searchDirection
			switch current {
			case -1:
				current = len(e.rows) - 1
			case len(e.rows):
				current = 0
			}

			row := e.rows[current]
			rx := strings.Index(row.render, query)
			if rx != -1 {
				lastMatchRowIndex = current
				e.cy = current
				e.cx = rowRxToCx(row, rx)
				// set rowOffset to bottom so that the next scroll() will scroll
				// upwards and the matching line will be at the top of the screen
				e.rowOffset = len(e.rows)
				// highlight the matched string
				savedHlRowIndex = current
				savedHl = make([]uint8, len(row.hl))
				copy(savedHl, row.hl)
				for i := 0; i < utf8.RuneCountInString(query); i++ {
					row.hl[rx+i] = hlMatch
				}
				break
			}
		}
	}

	_, err := e.Prompt("Search: %s (ESC = cancel | Enter = confirm | Arrows = prev/next)", onKeyPress)
	// restore cursor position when the user cancels search
	if err == ErrPromptCanceled {
		e.cx = savedCx
		e.cy = savedCy
		e.colOffset = savedColOffset
		e.rowOffset = savedRowOffset
	}
	return err
}

func main() {
	var editor Editor

	if err := editor.Init(); err != nil {
		die(err)
	}
	defer editor.Close()

	if len(os.Args) > 1 {
		err := editor.OpenFile(os.Args[1])
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			die(err)
		}
	}

	editor.SetStatusMessage("-- NORMAL --")

	for {
		editor.Render()
		if err := editor.ProcessKey(); err != nil {
			if err == ErrQuitEditor {
				break
			}
			die(err)
		}
	}
}
