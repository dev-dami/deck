// Command fakeshim is a test shim. TestMain builds it.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		os.Exit(2)
	}
	switch os.Args[1] {
	case "args":
		in, _ := io.ReadAll(os.Stdin)
		wd, _ := os.Getwd()
		argv, _ := json.Marshal(os.Args[2:])
		env, _ := json.Marshal(os.Environ())
		fmt.Printf("argv=%s\ncwd=%s\nenv=%s\nstdin=%q\n", argv, wd, env, in)
	case "sleep":
		time.Sleep(time.Hour)
	case "spam":
		line := []byte(strings.Repeat("x", 1023) + "\n")
		for {
			if _, err := os.Stdout.Write(line); err != nil {
				os.Exit(1)
			}
		}
	case "child":
		// Start a child that sleeps, record its pid, then sleep too.
		self, _ := os.Executable()
		c := exec.Command(self, "sleep")
		if err := c.Start(); err != nil {
			os.Exit(3)
		}
		_ = os.WriteFile("child.pid", []byte(strconv.Itoa(c.Process.Pid)), 0o600)
		time.Sleep(time.Hour)
	case "exit":
		n, _ := strconv.Atoi(os.Args[2])
		os.Exit(n)
	}
}
