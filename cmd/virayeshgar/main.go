package main

import (
	"errors"
	"flag"
	"io"
	"log"
	"os"
	"runtime/debug"

	editormod "github.com/amirali/virayeshgar/editor"
)

func main() {
	debugFlag := flag.Bool("debug", false, "flag to enable debug logging")
	flag.Parse()

	var outfile io.Writer
	if *debugFlag {
		outfile, _ = os.Create("./virayeshgar.keys.log")
	} else {

		outfile, _ = os.Open(os.DevNull)
	}
	logger := log.New(outfile, "", 0)

	defer func() {
		if r := recover(); r != nil {
			logger.Printf("---- panic stack ----\npanic: %#v\n%s\n---------------------", r, string(debug.Stack()))
		}
	}()

	var editor editormod.Editor

	if err := editor.Init(logger); err != nil {
		editormod.Die(err)
	}
	defer editor.Close()

	if len(os.Args) > 1 {
		err := editor.OpenFile(os.Args[1])
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			editormod.Die(err)
		}
	}

	if len(editor.Rows) == 0 {
		editor.Rows = append(editor.Rows, &editormod.Row{})
	}

	editor.SetStatusMessage("-- NORMAL --")

	for {
		editor.Render()
		if err := editor.ProcessKey(); err != nil {
			if err == editormod.ErrQuitEditor {
				break
			}
			editormod.Die(err)
		}
	}
}
