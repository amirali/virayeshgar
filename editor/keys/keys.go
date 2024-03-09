package keys

type Key int32

// normal mode
const (
	NavKeyH          Key = 104
	NavKeyJ          Key = 106
	NavKeyK          Key = 107
	NavKeyL          Key = 108
	NavKeyLeftCurly  Key = 123
	NavKeyRightCurly Key = 125
	NavKeyGg         Key = 103
	NavKeyCapitalG   Key = 71

	EscKey Key = 27

	ModeKeyI        Key = 105
	ModeKeySmallA   Key = 97
	ModeKeyCapitalA Key = 65
	ModeKeySmallO   Key = 111
	ModeKeyCapitalO Key = 79
	ModeKeyCol      Key = 58
	ModeKeySearch   Key = 47
	ModeKeyU        Key = 117
	ModeKeyX        Key = 120
)

// motion
const (
	MotionKeyD        Key = 100
	MotionKeyY        Key = 121
	MotionKeySmallP   Key = 112
	MotionKeyCapitalP Key = 80
)

// insert mode
const (
	KeyEnter     Key = 10
	KeyBackspace Key = 127

	KeyArrowLeft Key = iota + 1000
	KeyArrowRight
	KeyArrowUp
	KeyArrowDown
	KeyDelete
	KeyPageUp
	KeyPageDown
	KeyHome
	KeyEnd
)

// ctrl returns a byte resulting from pressing the given ASCII character with the ctrl-key.
func Ctrl(char byte) byte {
	return char & 0x1f
}

func KeySequenceEqual(a, b []Key) bool {
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

func IsArrowKey(k Key) bool {
	return k == KeyArrowUp || k == KeyArrowRight ||
		k == KeyArrowDown || k == KeyArrowLeft
}
