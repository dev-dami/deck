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

func main() {
	dirs := os.Args[1:]
	go func() {
		if err := ui.Run(dirs); err != nil {
			log.Println(err)
		}
		os.Exit(0)
	}()
	app.Main()
}
