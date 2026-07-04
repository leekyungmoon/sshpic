package iterm2

import "os"

type Check struct {
	Name   string
	Status string
	Detail string
	Fatal  bool
}

func DoctorChecks() []Check {
	term := os.Getenv("TERM_PROGRAM")
	if term == "iTerm.app" {
		return []Check{{Name: "terminal", Status: "ok", Detail: "TERM_PROGRAM=iTerm.app"}}
	}
	return []Check{{Name: "terminal", Status: "warn", Detail: "iTerm2 is the only implemented direct-paste target in v0.1; current TERM_PROGRAM=" + term}}
}
