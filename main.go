// Package mirror copies a source directory tree into a destination,
// skipping files whose size and mtime already match.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	tea "charm.land/bubbletea/v2"
)

const version = "0.1.0"

func main() {
	var showVersion bool
	var (
		dstPath string
		srcPath string
	)

	flag.BoolVar(&showVersion, "version", false, "print version and exit")
	flag.BoolVar(&showVersion, "v", false, "print version and exit (shorthand)")
	flag.StringVar(&dstPath, "dst", "", "destination directory")
	flag.StringVar(&srcPath, "src", "", "source directory")
	flag.Parse()

	if showVersion {
		fmt.Println(version)
		return
	}

	if len(srcPath) == 0 {
		homePath, err := os.UserHomeDir()
		if err != nil {
			log.Fatalf("%v", err)
		}

		srcPath = homePath
	}

	srcInfo, err := os.Stat(srcPath)
	if err != nil {
		log.Fatalf("%v", err)
	}

	if !srcInfo.IsDir() {
		log.Println("Source path is not a directory.")
		return
	}

	dstInfo, err := os.Stat(dstPath)
	if err != nil {
		log.Fatalf("%v", err)
	}

	if !dstInfo.IsDir() {
		log.Println("Destination path is not a directory.")
		return
	}

	m := newModel(srcPath, dstPath)
	p := tea.NewProgram(m)

	if _, err := p.Run(); err != nil {
		log.Fatalf("Unexpected error: %v", err)
	}
}
