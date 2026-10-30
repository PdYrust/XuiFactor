package main

import (
	"os"
	"path/filepath"
	"syscall"

	"github.com/PdYrust/XuiFactor/internal/cli"
)

func main() {
	if filepath.Base(os.Args[0]) == cli.CommandName && cli.IsPrimeFactorCall(os.Args[1:]) {
		execSystemFactor(os.Args[1:])
	}
	app := cli.New(os.Stdout, os.Stderr)
	os.Exit(app.Run(os.Args[1:]))
}

// The factor command is installed ahead of coreutils factor in PATH, so
// numeric calls keep their usual meaning and go to the system command.
func execSystemFactor(args []string) {
	self, _ := os.Executable()
	self, _ = filepath.EvalSymlinks(self)
	for _, path := range []string{"/usr/bin/factor", "/bin/factor"} {
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil || resolved == self {
			continue
		}
		_ = syscall.Exec(path, append([]string{"factor"}, args...), os.Environ())
	}
}
