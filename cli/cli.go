// Package cli is the havamal command line.
package cli

import (
	"fmt"
	"io"
	"io/fs"
	"os"

	"github.com/ouroboric-dev/havamal"
)

// Exit codes: 0 done, 1 the check found a problem, 2 the command was refused.
const (
	ExitDone    = 0
	ExitFailed  = 1
	ExitRefused = 2
)

// Run runs a havamal command and returns its exit code.
func Run(args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(errOut, "usage: havamal check [dir]")
		return ExitRefused
	}
	switch args[0] {
	case "check":
		return check(args[1:], out, errOut)
	}
	fmt.Fprintf(errOut, "havamal: unknown command %q\n", args[0])
	return ExitRefused
}

// check verifies the library in dir (default: the current directory) against its lock.
func check(args []string, out, errOut io.Writer) int {
	if len(args) > 1 {
		fmt.Fprintln(errOut, "usage: havamal check [dir]")
		return ExitRefused
	}
	dir := "."
	if len(args) == 1 {
		dir = args[0]
	}
	return report(os.DirFS(dir), out, errOut)
}

// report prints the drift of the library in fsys, and returns ExitFailed when there is any.
func report(fsys fs.FS, out, errOut io.Writer) int {
	drift, err := havamal.Verify(fsys)
	if err != nil {
		fmt.Fprintf(errOut, "havamal: %v\n", err)
		return ExitRefused
	}
	for _, d := range drift {
		fmt.Fprintf(out, "DRIFT %s\n", d)
	}
	if len(drift) > 0 {
		return ExitFailed
	}
	fmt.Fprintln(out, "OK    the library is as locked")
	return ExitDone
}
