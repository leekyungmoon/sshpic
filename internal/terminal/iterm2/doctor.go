package iterm2

import (
	"fmt"
	"os"
)

type Check struct {
	Name   string
	Status string
	Detail string
	Fatal  bool
}

func DoctorChecks() []Check {
	checks := []Check{}
	term := os.Getenv("TERM_PROGRAM")
	if term == "iTerm.app" {
		checks = append(checks, Check{Name: "terminal", Status: "ok", Detail: "TERM_PROGRAM=iTerm.app"})
	} else {
		checks = append(checks, Check{Name: "terminal", Status: "warn", Detail: "iTerm2 is the only implemented direct-paste target in v0.1; current TERM_PROGRAM=" + term})
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		checks = append(checks, Check{Name: "iterm2_python_runtime", Status: "warn", Detail: "cannot determine home directory"})
		return checks
	}
	runtime := DetectPythonRuntime(home)
	if runtime.Ready {
		checks = append(checks, Check{Name: "iterm2_python_runtime", Status: "ok", Detail: fmt.Sprintf("version=%d path=%s", runtime.Version, runtime.Path)})
	} else {
		checks = append(checks, Check{Name: "iterm2_python_runtime", Status: "warn", Detail: "not ready; installer will use the no-Python Cmd+V fallback instead: " + runtime.Reason})
	}
	return checks
}
