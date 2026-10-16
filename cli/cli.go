// Package cli is the havamal command line.
package cli

import (
	"context"
	"flag"
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
		fmt.Fprintln(errOut, "usage: havamal check [dir] | vendor ...")
		return ExitRefused
	}
	switch args[0] {
	case "check":
		return check(args[1:], out, errOut)
	case "vendor":
		return vendor(args[1:], out, errOut)
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

// vendor copies one skill from an upstream commit into the library in the current directory (or
// -dir) and locks it. The result is a diff for a person to review.
func vendor(args []string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("vendor", flag.ContinueOnError)
	fs.SetOutput(errOut)
	var o havamal.VendorOptions
	fs.StringVar(&o.Dir, "dir", ".", "the library: holds skills/ and skills.lock")
	fs.StringVar(&o.Name, "name", "", "the skill's name (its folder's name)")
	fs.StringVar(&o.Repo, "repo", "", "the upstream's public https URL")
	fs.StringVar(&o.Commit, "commit", "", "the full commit hash")
	fs.StringVar(&o.Path, "path", "", "the skill's folder in the upstream")
	fs.StringVar(&o.Licence, "licence", "", "the skill's SPDX licence id")
	fs.BoolVar(&o.Update, "update", false, "replace a skill that is already vendored")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		return ExitRefused
	}
	if err := havamal.Vendor(context.Background(), o); err != nil {
		fmt.Fprintf(errOut, "havamal: %v\n", err)
		return ExitFailed
	}
	fmt.Fprintf(out, "vendored %s at %.12s; review the diff, then open a pull request\n", o.Name, o.Commit)
	return ExitDone
}
