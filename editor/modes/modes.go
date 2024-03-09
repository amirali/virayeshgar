package mode

type Mode struct {
	Name          string
	StatusMessage string
}

var (
	NormalMode  Mode = Mode{Name: "normal", StatusMessage: "-- NORMAL --"}
	InsertMode       = Mode{Name: "insert", StatusMessage: "-- INSERT --"}
	CommandMode      = Mode{Name: "command"}
)
