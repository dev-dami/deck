// Command deck-desktop is the Deck desktop app: a native window over the
// deck CLI, which it runs as `deck serve --rpc` for each open project.
//
//	deck-desktop [project dir...]
package main

import (
	"log"
	"os"

	"gioui.org/app"

	"github.com/dev-dami/deck/desktop/ui"
)

// version is set at build time with -ldflags "-X main.version=…".
var version = "dev"

func main() {
	dirs := os.Args[1:]
	go func() {
		if err := ui.Run(version, dirs); err != nil {
			log.Println(err)
		}
		os.Exit(0)
	}()
	app.Main()
}
