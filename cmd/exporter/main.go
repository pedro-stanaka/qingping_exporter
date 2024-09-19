package main

import (
	"fmt"
	"os"

	"gopkg.in/alecthomas/kingpin.v2"
)

func main() {
	app := kingpin.New("qingping_exporter", "A simple CLI application.")
	kingpin.Version("1.0.0")
	kingpin.HelpFlag.Short('h')

	kingpin.MustParse(app.Parse(os.Args[1:]))

	// Print hello world message
	fmt.Println("Hello, World!")
}
