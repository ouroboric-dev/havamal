// Command havamal checks and maintains the skill library.
package main

import (
	"os"

	"github.com/ouroboric-dev/havamal/cli"
)

func main() { os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr)) }
