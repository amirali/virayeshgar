package tools

import (
	"fmt"
	"os"
	"strings"
	"unicode"
)

func InsertToSlice[T any](s []T, value T, at int) []T {
	if at >= 0 && at <= len(s) {
		newSlice := make([]T, len(s)+1)
		copy(newSlice[:at], s[:at])
		newSlice[at] = value
		copy(newSlice[at+1:], s[at:])
		s = newSlice
	}

	return s
}

func RemoveFromSlice[T any](s []T, at int) []T {
	if at >= 0 && at <= len(s) {
		newSlice := make([]T, len(s)-1)
		copy(newSlice[:at], s[:at])
		copy(newSlice[at:], s[at+1:])
		s = newSlice
	}

	return s
}

// utf8Slice slice the given string by utf8 character.
func Utf8Slice(s string, start, end int) string {
	return string([]rune(s)[start:end])
}

func GetCursorPosition() (row, col int, err error) {
	if _, err = os.Stdout.Write([]byte("\x1b[6n")); err != nil {
		return
	}
	if _, err = fmt.Fscanf(os.Stdin, "\x1b[%d;%d", &row, &col); err != nil {
		return
	}
	return
}

func IsSeparator(r rune) bool {
	return unicode.IsSpace(r) || strings.IndexRune(",.()+-/*=~%<>[]{}:;", r) != -1
}
