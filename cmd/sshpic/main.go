package main

import (
	"os"

	"github.com/leekyungmoon/sshpic/internal/app"
)

var (
	version = "0.1.0"
	commit  = "dev"
	date    = "unknown"
)

func main() {
	os.Exit(app.Run(os.Args[1:], app.BuildInfo{Version: version, Commit: commit, Date: date}, os.Stdout, os.Stderr))
}
