package havamal

import (
	"embed"
	"io/fs"
)

// The pinned library: the skills folder and its lock, embedded, so the module's version pins them.
//
//go:embed all:skills skills.lock
var embedded embed.FS

// Embedded opens the library this version of the module ships, and refuses it if it has drifted.
func Embedded() (*Library, error) { return Open(fs.FS(embedded)) }
