// Command ghs manages GitHub repository settings from a settings file.
package main

import (
	"os"

	"github.com/kota65535/ghs/cmd"
)

func main() {
	os.Exit(cmd.Execute())
}
