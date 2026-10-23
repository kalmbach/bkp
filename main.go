// Package main concurrent copies a source directory tree into a destination,
// skipping files whose size and mtime already match.
package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"os"

	tea "charm.land/bubbletea/v2"

	"github.com/kalmbach/bkp/internal/mirror"
)

const version = "0.1.1"

func main() {
	var showVersion bool
	var (
		input   string
		output  string
		workers int
	)

	log.SetFlags(0)

	flag.StringVar(&input, "i", "", "input directory")
	flag.StringVar(&output, "o", "", "output directory")
	flag.IntVar(&workers, "n", 4, "number of concurrent workers")
	flag.BoolVar(&showVersion, "v", false, "print version and exit (shorthand)")
	flag.BoolVar(&showVersion, "version", false, "print version and exit")
	flag.Parse()

	if flag.NFlag() == 0 {
		flag.Usage()
		os.Exit(2)
	}

	if showVersion {
		fmt.Println(version)
		return
	}

	if input == "" {
		log.Fatal("-i: a non empty path is required")
	}

	if err := mirror.CheckPath(input); err != nil {
		log.Fatalf("-i: %v", err)
	}

	if output == "" {
		log.Fatal("-o: a non empty path is required")
	}

	if err := mirror.CheckPath(output); err != nil {
		log.Fatalf("-o: %v", err)
	}

	if workers < 1 {
		log.Fatal("-n: minimum number of concurrent workers is 1 (one)")
	}

	canonicalInput, err := mirror.CanonicalPath(input)
	if err != nil {
		log.Fatalf("-i: %v", err)
	}

	canonicalOutput, err := mirror.CanonicalPath(output)
	if err != nil {
		log.Fatalf("-o: %v", err)
	}

	err = mirror.ContainmentCheck(canonicalInput, canonicalOutput)
	if errors.Is(err, mirror.ErrSameDir) {
		log.Fatal("-o: output directory is the same as input directory")
	}
	if errors.Is(err, mirror.ErrDstContainedBySrc) {
		log.Fatal("-o: output directory is inside the input directory")
	}
	if errors.Is(err, mirror.ErrSrcContainedByDst) {
		log.Fatal("-i: input directory is inside the output directory")
	}
	if err != nil {
		log.Fatal(err)
	}

	m := newModel(canonicalInput, canonicalOutput, workers)
	p := tea.NewProgram(m)

	if _, err := p.Run(); err != nil {
		log.Fatalf("unexpected error: %v", err)
	}
}
