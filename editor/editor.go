package editor

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/mattn/go-runewidth"
	"golang.org/x/sys/unix"

	actions "github.com/amirali/virayeshgar/editor/actions"
	keys "github.com/amirali/virayeshgar/editor/keys"
	modes "github.com/amirali/virayeshgar/editor/modes"
	"github.com/amirali/virayeshgar/editor/syntax"
	"github.com/amirali/virayeshgar/tools"
)

var version = "0.0.0dev"

const tabstop = 8

var ErrQuitEditor = errors.New("quit editor")
var ErrUnknownMode = errors.New("unknown mode")
var ErrUnknownCommand = errors.New("unknown command")
var ErrUnkownMotion = errors.New("unknown motion")

var (
	stdinfd  = int(os.Stdin.Fd())
	stdoutfd = int(os.Stdout.Fd())
)

type Editor struct {
	cx int
	cy int
	rx int

	rowOffset int
	colOffset int

	screenRows int
	screenCols int

	Rows []*Row

	dirty int

	quitCounter int

	filename string

	statusmsg     string
	statusmsgTime time.Time

	syntax *syntax.EditorSyntax

	origTermios *unix.Termios

	mode           modes.Mode
	command        string
	motionRegister []keys.Key
	yankRegister   string

	// TODO: Implement this as an actual tree
	undoPath []*UndoNode

	logger *log.Logger
}

type UndoNode struct {
	undoType   actions.Action
	beforeRows []*Row
	afterRows  []*Row
	// NOTE: considering one line changes for now
	fromIdx int
	toIdx   int
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

func (e *Editor) Init(logger *log.Logger) error {
	termios, err := enableRawMode()

	e.logger = logger

	if err != nil {
		return err
	}

	e.origTermios = termios
	e.mode = modes.NormalMode
	e.undoPath = make([]*UndoNode, 0)

	ws, err := unix.IoctlGetWinsize(stdoutfd, unix.TIOCGWINSZ)
	if err != nil || ws.Col == 0 {
		if _, err = os.Stdout.Write([]byte("\x1b[999C\x1b[999B")); err != nil {
			return err
		}
		if row, col, err := tools.GetCursorPosition(); err == nil {
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

func Die(err error) {
	os.Stdout.WriteString("\x1b[2J") // clear the screen
	os.Stdout.WriteString("\x1b[H")  // reposition the cursor
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	os.Exit(1)
}

// readKey reads a key press input from stdin.
func readKey() (keys.Key, error) {
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
				return keys.KeyArrowUp, nil
			case bytes.Equal(buf, []byte("\x1b[B")):
				return keys.KeyArrowDown, nil
			case bytes.Equal(buf, []byte("\x1b[C")):
				return keys.KeyArrowRight, nil
			case bytes.Equal(buf, []byte("\x1b[D")):
				return keys.KeyArrowLeft, nil
			case bytes.Equal(buf, []byte("\x1b[1~")), bytes.Equal(buf, []byte("\x1b[7~")),
				bytes.Equal(buf, []byte("\x1b[H")), bytes.Equal(buf, []byte("\x1bOH")):
				return keys.KeyHome, nil
			case bytes.Equal(buf, []byte("\x1b[4~")), bytes.Equal(buf, []byte("\x1b[8~")),
				bytes.Equal(buf, []byte("\x1b[F")), bytes.Equal(buf, []byte("\x1bOF")):
				return keys.KeyEnd, nil
			case bytes.Equal(buf, []byte("\x1b[3~")):
				return keys.KeyDelete, nil
			case bytes.Equal(buf, []byte("\x1b[5~")):
				return keys.KeyPageUp, nil
			case bytes.Equal(buf, []byte("\x1b[6~")):
				return keys.KeyPageDown, nil

			default:
				return keys.Key(buf[0]), nil
			}
		}
	}
}

func (e *Editor) MoveCursor(k keys.Key) {
	switch k {
	case keys.NavKeyK, keys.KeyArrowUp, keys.NavKeyLeftCurly:
		if e.cy != 0 {
			e.cy--
		}
	case keys.NavKeyJ, keys.KeyArrowDown, keys.NavKeyRightCurly:
		if e.cy < len(e.Rows) {
			e.cy++
		}
	case keys.NavKeyH, keys.KeyArrowLeft:
		if e.cx != 0 {
			e.cx--
		} else if e.cy > 0 {
			e.cy--
			e.cx = len(e.Rows[e.cy].chars)
		}
	case keys.NavKeyL, keys.KeyArrowRight:
		linelen := -1
		if e.cy < len(e.Rows) {
			linelen = len(e.Rows[e.cy].chars)
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
	if e.cy < len(e.Rows) {
		linelen = len(e.Rows[e.cy].chars)
	}
	if e.cx > linelen {
		e.cx = linelen
	}
}

func (e *Editor) MoveCursorByRepeat(k keys.Key, repeat int) {
	for i := 0; i < repeat; i++ {
		e.MoveCursor(k)
	}
}

func (e *Editor) JumpParagraph(k keys.Key) {
	row := e.cy
	var targetSlice []*Row
	if (row == 0 && k == keys.NavKeyLeftCurly) || (row == len(e.Rows) && k == keys.NavKeyRightCurly) {
		return
	}
	e.logger.Println("     ", row)
	switch k {
	case keys.NavKeyLeftCurly:
		targetSlice = slices.Clone(e.Rows[:row])
		slices.Reverse(targetSlice)
	case keys.NavKeyRightCurly:
		targetSlice = e.Rows[row+1:]
	}
	for _, rowFinder := range targetSlice {
		e.logger.Println(rowFinder.render, row)
		e.MoveCursor(k)
		if rowFinder.render == "" {
			break
		}
	}
}

func (e *Editor) ProcessKeyInsertMode() error {
	k, err := readKey()
	if err != nil {
		return err
	}
	switch k {
	case keys.KeyEnter:
		e.InsertNewline()

	case keys.KeyBackspace:
		e.DeleteChar()

	case keys.KeyDelete:
		if e.cy == len(e.Rows)-1 && e.cx == len(e.Rows[e.cy].chars) {
			// cursor is on the last row and one past the last character,
			// no more character to delete to the right.
			break
		}
		e.MoveCursor(keys.KeyArrowRight)
		e.DeleteChar()

	// case keyArrowLeft, keyArrowDown, keyArrowUp, keyArrowRight:
	// 	e.MoveCursor(k)

	case keys.EscKey:
		e.SetMode(modes.NormalMode)

	default:
		e.InsertChar(rune(k))
	}
	// Reset quitCounter to zero if user pressed any key other than Ctrl-Q.
	e.quitCounter = 0
	return nil
}

func (e *Editor) SetMode(mode modes.Mode) {
	e.mode = mode
	e.SetStatusMessage(mode.StatusMessage)

	switch mode {
	case modes.InsertMode:
		// FIXME: WTF?
		newRow := *e.Rows[e.cy]
		e.undoPath = append(e.undoPath, &UndoNode{undoType: actions.Edit, fromIdx: e.cy, toIdx: e.cy, beforeRows: []*Row{&newRow}})
	case modes.NormalMode:
		// node := e.undoPath[len(e.undoPath)-1]
		// node.toIdx = e.cy
	}
}

func (e *Editor) ExecuteMotion() error {
	if len(e.motionRegister) > 3 {
		e.motionRegister = []keys.Key{}
		return ErrUnkownMotion
	}
	switch {
	case keys.KeySequenceEqual(e.motionRegister, []keys.Key{keys.MotionKeyD, keys.MotionKeyD}):
		e.CutRow()
		e.motionRegister = []keys.Key{}
	case keys.KeySequenceEqual(e.motionRegister, []keys.Key{keys.MotionKeySmallP}):
		e.PasteRow(e.cy + 1)
		e.motionRegister = []keys.Key{}
	case keys.KeySequenceEqual(e.motionRegister, []keys.Key{keys.MotionKeyCapitalP}):
		e.PasteRow(e.cy)
		e.motionRegister = []keys.Key{}
	case keys.KeySequenceEqual(e.motionRegister, []keys.Key{keys.MotionKeyY, keys.MotionKeyY}):
		e.YankRow()

		e.motionRegister = []keys.Key{}
	}
	return nil
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
	case actions.Cut:
		for i := 0; i < lastUndoNode.toIdx-lastUndoNode.fromIdx+1; i++ {
			e.Rows = tools.InsertToSlice(e.Rows, lastUndoNode.beforeRows[i], lastUndoNode.fromIdx+i)
		}
	case actions.Paste:
		for i := 0; i < lastUndoNode.toIdx-lastUndoNode.fromIdx+1; i++ {
			e.Rows = tools.RemoveFromSlice(e.Rows, lastUndoNode.fromIdx+i)
		}
	case actions.Edit:
		e.logger.Printf("before rows: %#v", lastUndoNode.beforeRows[0])
		for i := 0; i < lastUndoNode.toIdx-lastUndoNode.fromIdx+1; i++ {
			e.logger.Println(lastUndoNode.fromIdx+i, i)
			e.Rows[lastUndoNode.fromIdx+i] = lastUndoNode.beforeRows[i]
		}
	}
}

func (e *Editor) ProcessKeyNormalMode() error {
	k, err := readKey()
	if err != nil {
		return err
	}
	e.SetStatusMessage("-- NORMAL --")
	e.logger.Printf("%#v\n", k)
	switch k {

	// case navKeyH, navKeyJ, navKeyK, navKeyL, keyArrowLeft, keyArrowDown, keyArrowUp, keyArrowRight:
	case keys.NavKeyH, keys.NavKeyJ, keys.NavKeyK, keys.NavKeyL:
		e.MoveCursor(k)

	case keys.NavKeyLeftCurly, keys.NavKeyRightCurly:
		e.JumpParagraph(k)

	case keys.NavKeyGg:
		e.cy = 0

	case keys.NavKeyCapitalG:
		e.cy = len(e.Rows) - 1

	case keys.ModeKeyI:
		e.SetMode(modes.InsertMode)
	case keys.ModeKeyCol:
		e.mode = modes.CommandMode
		e.command = ""
		e.SetStatusMessage(e.command)

	case keys.ModeKeySmallA:
		e.MoveCursor(keys.NavKeyL)
		e.SetMode(modes.InsertMode)

	case keys.ModeKeyCapitalA:
		if e.cy < len(e.Rows) {
			e.cx = len(e.Rows[e.cy].chars)
		}
		e.SetMode(modes.InsertMode)
	case keys.ModeKeySmallO:
		if e.cy < len(e.Rows) {
			e.cx = len(e.Rows[e.cy].chars)
		}
		e.InsertNewline()
		e.SetMode(modes.InsertMode)
	case keys.ModeKeyCapitalO:
		if e.cy < len(e.Rows) {
			e.cx = 0
		}
		e.InsertNewline()
		e.MoveCursor(keys.NavKeyK)
		e.SetMode(modes.InsertMode)
	case keys.ModeKeySearch:
		err := e.Find()
		if err != nil {
			if err == ErrPromptCanceled {
				e.SetStatusMessage("")
			} else {
				return err
			}
		}
	case keys.ModeKeyU:
		e.Undo()
	case keys.ModeKeyX:
		if e.cy == len(e.Rows)-1 && e.cx == len(e.Rows[e.cy].chars) {
			// cursor is on the last row and one past the last character,
			// no more character to delete to the right.
			break
		}
		e.MoveCursor(keys.KeyArrowRight)
		e.DeleteChar()
	case keys.EscKey:
		e.motionRegister = []keys.Key{}
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
	var err error
	e.command, err = e.Prompt(":%s", nil)
	if err != nil {
		return err
	}

	if e.command != "" {
		return e.ExecuteCommand()
	}

	return nil
}

func (e *Editor) ProcessKey() error {
	switch e.mode {
	case modes.NormalMode:
		return e.ProcessKeyNormalMode()

	case modes.InsertMode:
		return e.ProcessKeyInsertMode()

	case modes.CommandMode:
		return e.ProcessKeyCommandMode()

	default:
		return ErrUnknownMode
	}
}

func (e *Editor) ExecuteCommand() error {
	var err error

	commandParts := strings.Split(e.command, " ")

	e.SetMode(modes.NormalMode)

	switch commandParts[0] {
	case "w":
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

	case "q":
		if e.dirty > 0 {
			e.SetStatusMessage("ERROR!!! File has unsaved changes")
			e.command = ""
			return nil
		}
		os.Stdout.WriteString("\x1b[2J") // clear the screen
		os.Stdout.WriteString("\x1b[H")  // reposition the cursor
		return ErrQuitEditor

	case "q!":
		os.Stdout.WriteString("\x1b[2J") // clear the screen
		os.Stdout.WriteString("\x1b[H")  // reposition the cursor
		return ErrQuitEditor

	case "wq":
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

	case "syntax":
		for _, syntax := range syntax.HLDB {
			if syntax.Filetype == commandParts[1] {
				e.syntax = syntax
				for _, row := range e.Rows {
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
		if filerow >= len(e.Rows) {
			if len(e.Rows) == 0 && y == e.screenRows/3 {
				welcomeMsg := fmt.Sprintf("Virayeshgar v%s", version)
				if runewidth.StringWidth(welcomeMsg) > e.screenCols {
					welcomeMsg = tools.Utf8Slice(welcomeMsg, 0, e.screenCols)
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
			if runewidth.StringWidth(e.Rows[filerow].render) > e.colOffset {
				line = tools.Utf8Slice(
					e.Rows[filerow].render,
					e.colOffset,
					utf8.RuneCountInString(e.Rows[filerow].render))
				hl = e.Rows[filerow].hl[e.colOffset:]
			}
			if runewidth.StringWidth(line) > e.screenCols {
				line = runewidth.Truncate(line, e.screenCols, "")
				hl = hl[:utf8.RuneCountInString(line)]
			}
			currentColor := ""          // keep track of color to detect color change
			b.WriteString("\x1b[0;90m") // use inverted colors
			maxLength := len(fmt.Sprint(len(e.Rows)))
			b.WriteString(fmt.Sprintf("%*d ", maxLength, e.Rows[filerow].idx+1))
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
				} else if hl[i] == syntax.HlNormal {
					if currentColor != "" {
						currentColor = ""
						b.WriteString("\x1b[39m")
					}
					b.WriteRune(r)
				} else {
					color := syntax.SyntaxToColor(hl[i])
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
	lmsg := fmt.Sprintf("%.20s - %d lines - %s", filename, len(e.Rows), dirtyStatus)
	if runewidth.StringWidth(lmsg) > e.screenCols {
		lmsg = runewidth.Truncate(lmsg, e.screenCols, "...")
	}
	b.WriteString(lmsg)
	filetype := "no filetype"
	if e.syntax != nil {
		filetype = e.syntax.Filetype
	}
	row, col, _ := tools.GetCursorPosition()

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

func (e Editor) rowCxToRx(row *Row, cx int) int {
	rx := 0
	idx := cx
	if cx > len(row.chars) {
		idx = len(row.chars) - 1
	}
	for _, r := range row.chars[:idx] {
		if r == '\t' {
			if e.syntax.Tabstop != 0 {
				rx += (e.syntax.Tabstop) - (rx % e.syntax.Tabstop)
			} else {
				rx += (tabstop) - (rx % tabstop)
			}
		} else {
			rx += runewidth.RuneWidth(r)
		}
	}
	return rx
}

func (e Editor) rowRxToCx(row *Row, rx int) int {
	curRx := 0
	for i, r := range row.chars {
		if r == '\t' {
			if e.syntax.Tabstop != 0 {
				curRx += (e.syntax.Tabstop) - (curRx % e.syntax.Tabstop)
			} else {
				curRx += (tabstop) - (curRx % tabstop)
			}
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
	if e.cy < len(e.Rows) {
		e.rx = e.rowCxToRx(e.Rows[e.cy], e.cx)
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
	b.WriteString(fmt.Sprintf("\x1b[%d;%dH", (e.cy-e.rowOffset)+1, (e.rx-e.colOffset)+1+len(fmt.Sprint(len(e.Rows)))+1))
	// show the cursor
	b.Write([]byte("\x1b[?25h"))
	os.Stdout.WriteString(b.String())
}

func (e *Editor) SetStatusMessage(format string, a ...interface{}) {
	e.statusmsg = fmt.Sprintf(format, a...)
	e.statusmsgTime = time.Now()
}

func (e *Editor) rowsToString() string {
	var b strings.Builder
	for _, row := range e.Rows {
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
func (e *Editor) Prompt(prompt string, cb func(query string, k keys.Key)) (string, error) {
	var b strings.Builder
	for {
		e.SetStatusMessage(prompt, b.String())
		e.Render()

		k, err := readKey()
		if err != nil {
			return "", err
		}
		if k == keys.KeyDelete || k == keys.KeyBackspace || k == keys.Key(keys.Ctrl('h')) {
			if b.Len() > 0 {
				bytes := []byte(b.String())
				_, size := utf8.DecodeLastRune(bytes)
				b.Reset()
				b.WriteString(string(bytes[:len(bytes)-size]))
			}
		} else if k == keys.Key('\x1b') {
			e.SetStatusMessage("")
			if cb != nil {
				cb(b.String(), k)
			}
			return "", ErrPromptCanceled
		} else if k == keys.KeyEnter {
			if b.Len() > 0 {
				e.SetStatusMessage("")
				if cb != nil {
					cb(b.String(), k)
				}
				return b.String(), nil
			}
		} else if !unicode.IsControl(rune(k)) && !keys.IsArrowKey(k) && unicode.IsPrint(rune(k)) {
			b.WriteRune(rune(k))
		}

		if cb != nil {
			cb(b.String(), k)
		}
	}
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
		e.InsertRow(len(e.Rows), string(line))
	}
	if err := s.Err(); err != nil {
		return err
	}
	e.dirty = 0
	return nil
}

func (e *Editor) InsertRow(at int, chars string) {
	if at < 0 || at > len(e.Rows) {
		return
	}
	row := &Row{chars: []rune(chars)}
	row.idx = at
	if at > 0 {
		row.hasUnclosedComment = e.Rows[at-1].hasUnclosedComment
	}
	e.updateRow(row)

	e.Rows = append(e.Rows, &Row{}) // grow the buffer
	copy(e.Rows[at+1:], e.Rows[at:])
	for i := at + 1; i < len(e.Rows); i++ {
		e.Rows[i].idx++
	}
	e.Rows[at] = row
}

func (e *Editor) CutRow() {
	e.dirty++

	row := e.Rows[e.cy]
	e.yankRegister = row.render

	e.undoPath = append(e.undoPath, &UndoNode{undoType: actions.Cut, fromIdx: e.cy, toIdx: e.cy, beforeRows: []*Row{row}, afterRows: []*Row{{}}})

	copy(e.Rows[e.cy:], e.Rows[e.cy+1:])
	e.Rows = e.Rows[:len(e.Rows)-1]
	for i := e.cy; i < len(e.Rows); i++ {
		e.Rows[i].idx--
	}
}

// FIXME: when cursor is at the end of the line, the yanking doesn't work
func (e *Editor) YankRow() {
	e.dirty++
	row := e.Rows[e.cy]
	e.yankRegister = row.render
}

func (e *Editor) PasteRow(at int) {
	e.dirty++
	e.undoPath = append(e.undoPath, &UndoNode{undoType: actions.Paste, fromIdx: at, toIdx: at, afterRows: []*Row{e.Rows[at]}, beforeRows: []*Row{{}}})

	e.InsertRow(at, e.yankRegister)
	e.yankRegister = ""
}

func (e *Editor) InsertNewline() {
	e.dirty += 1

	if e.cx == 0 {
		e.InsertRow(e.cy, "")
	} else {
		row := e.Rows[e.cy]
		e.InsertRow(e.cy+1, string(row.chars[e.cx:]))
		// reassignment needed since the call to InsertRow
		// invalidates the pointer.
		row = e.Rows[e.cy]
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
			var currentTabstop int
			if e.syntax.Tabstop != 0 {
				currentTabstop = e.syntax.Tabstop
			} else {
				currentTabstop = tabstop
			}
			for col%currentTabstop != 0 {
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

func (e *Editor) updateHighlight(row *Row) {
	row.hl = make([]uint8, utf8.RuneCountInString(row.render))
	for i := range row.hl {
		row.hl[i] = syntax.HlNormal
	}

	if e.syntax == nil {
		return
	}

	prevSep := true

	// set to the quote when inside of a string.
	// set to zero when outside of a string.
	var strQuote rune

	// indicates whether we are inside a multi-line comment.
	inComment := row.idx > 0 && e.Rows[row.idx-1].hasUnclosedComment

	idx := 0
	runes := []rune(row.render)
	for idx < len(runes) {
		r := runes[idx]
		prevHl := syntax.HlNormal
		if idx > 0 {
			prevHl = row.hl[idx-1]
		}

		if e.syntax.Mcs != "" && e.syntax.Mce != "" && strQuote == 0 {
			if inComment {
				row.hl[idx] = syntax.HlMlComment
				if strings.HasPrefix(string(runes[idx:]), e.syntax.Mce) {
					for j := 0; j < len(e.syntax.Mce); j++ {
						row.hl[idx] = syntax.HlMlComment
						idx++
					}
					inComment = false
					prevSep = true
					continue
				} else {
					idx++
					continue
				}
			} else if strings.HasPrefix(string(runes[idx:]), e.syntax.Mcs) {
				for j := 0; j < len(e.syntax.Mcs); j++ {
					row.hl[idx] = syntax.HlMlComment
					idx++
				}
				inComment = true
				continue
			}
		}

		if e.syntax.Scs != "" && strQuote == 0 && !inComment {
			if strings.HasPrefix(string(runes[idx:]), e.syntax.Scs) {
				for idx < len(runes) {
					row.hl[idx] = syntax.HlComment
					idx++
				}
				break
			}
		}

		if (e.syntax.Flags & syntax.HL_HIGHLIGHT_STRINGS) != 0 {
			if strQuote != 0 {
				row.hl[idx] = syntax.HlString
				//deal with escape quote when inside a string
				if r == '\\' && idx+1 < len(runes) {
					row.hl[idx+1] = syntax.HlString
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
					row.hl[idx] = syntax.HlString
					idx++
					continue
				}
			}
		}

		if (e.syntax.Flags & syntax.HL_HIGHLIGHT_NUMBERS) != 0 {
			if unicode.IsDigit(r) && (prevSep || prevHl == syntax.HlNumber) ||
				r == '.' && prevHl == syntax.HlNumber {
				row.hl[idx] = syntax.HlNumber
				idx++
				prevSep = false
				continue
			}
		}

		if prevSep {
			keywordFound := false
			for _, kw := range e.syntax.Keywords {
				isKeyword2 := strings.HasSuffix(kw, "|")
				if isKeyword2 {
					kw = strings.TrimSuffix(kw, "|")
				}

				end := idx + utf8.RuneCountInString(kw)
				if end <= len(runes) && kw == string(runes[idx:end]) &&
					(end == len(runes) || tools.IsSeparator(runes[end])) {
					keywordFound = true
					hl := syntax.HlKeyword1
					if isKeyword2 {
						hl = syntax.HlKeyword2
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

		prevSep = tools.IsSeparator(r)
		idx++
	}

	changed := row.hasUnclosedComment != inComment
	row.hasUnclosedComment = inComment
	if changed && row.idx+1 < len(e.Rows) {
		e.updateHighlight(e.Rows[row.idx+1])
	}
}

func (e *Editor) selectSyntaxHighlight() {
	e.syntax = nil
	if len(e.filename) == 0 {
		return
	}

	ext := filepath.Ext(e.filename)

	for _, syntax := range syntax.HLDB {
		for _, pattern := range syntax.Filematch {
			isExt := strings.HasPrefix(pattern, ".")
			if (isExt && pattern == ext) ||
				(!isExt && strings.Index(e.filename, pattern) != -1) {
				e.syntax = syntax
				for _, row := range e.Rows {
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
	if e.cy == len(e.Rows) {
		e.InsertRow(len(e.Rows), "")
	}
	row := e.Rows[e.cy]
	row.insertChar(e.cx, c)
	e.updateRow(row)
	e.cx++
	e.dirty++
}

func (e *Editor) DeleteChar() {
	if e.cy == len(e.Rows) {
		return
	}
	if e.cx == 0 && e.cy == 0 {
		return
	}
	row := e.Rows[e.cy]
	if e.cx > 0 {
		row.deleteChar(e.cx - 1)
		e.updateRow(row)
		e.cx--
		e.dirty++
	} else {
		prevRow := e.Rows[e.cy-1]
		e.cx = len(prevRow.chars)
		prevRow.appendChars(row.chars)
		e.updateRow(prevRow)
		e.DeleteRow(e.cy)
		e.cy--
	}
}

func (e *Editor) DeleteRow(at int) {
	if at < 0 || at >= len(e.Rows) {
		return
	}
	e.Rows = append(e.Rows[:at], e.Rows[at+1:]...)
	for i := at; i < len(e.Rows); i++ {
		e.Rows[i].idx--
	}
	e.dirty++
}

// FIXME: Sometimes the patterm match will match the line above the rowOffset
// FIXME: Crashes sometimes in big files
func (e *Editor) Find() error {
	savedCx := e.cx
	savedCy := e.cy
	savedColOffset := e.colOffset
	savedRowOffset := e.rowOffset

	lastMatchRowIndex := -1 // remember the last match row
	searchDirection := 1    // 1 = forward, -1 = backward

	savedHlRowIndex := -1
	savedHl := []uint8(nil)

	onKeyPress := func(query string, k keys.Key) {
		if len(savedHl) > 0 {
			copy(e.Rows[savedHlRowIndex].hl, savedHl)
			savedHl = []uint8(nil)
		}
		switch k {
		case keys.KeyEnter, keys.Key('\x1b'):
			lastMatchRowIndex = -1
			searchDirection = 1
			return
		case keys.KeyArrowRight, keys.KeyArrowDown:
			searchDirection = 1
		case keys.KeyArrowLeft, keys.KeyArrowUp:
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
		for i := 0; i < len(e.Rows); i++ {
			current += searchDirection
			switch current {
			case -1:
				current = len(e.Rows) - 1
			case len(e.Rows):
				current = 0
			}

			row := e.Rows[current]
			rx := strings.Index(row.render, query)
			if rx != -1 {
				lastMatchRowIndex = current
				e.cy = current
				e.cx = e.rowRxToCx(row, rx)
				// set rowOffset to bottom so that the next scroll() will scroll
				// upwards and the matching line will be at the top of the screen
				e.rowOffset = len(e.Rows)
				// highlight the matched string
				savedHlRowIndex = current
				savedHl = make([]uint8, len(row.hl))
				copy(savedHl, row.hl)
				for i := 0; i < utf8.RuneCountInString(query); i++ {
					row.hl[rx+i] = syntax.HlMatch
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
