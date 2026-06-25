package main

import (
	_ "embed"
	"strings"

	"github.com/martinmares/oci-patch/cmd"
)

//go:embed VERSION
var versionFile string

func main() {
	cmd.SetVersion(strings.TrimSpace(versionFile))
	cmd.Execute()
}
