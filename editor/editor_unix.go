//go:build darwin
// +build darwin

package editor

import "golang.org/x/sys/unix"

const ioctlReadTermios = unix.TIOCGETA
const ioctlWriteTermios = unix.TIOCSETA

const NullBufferPath = "/dev/null"
