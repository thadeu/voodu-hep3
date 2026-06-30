// Command voodu-hep3 is the voodu plugin for the `hep3` resource kind —
// the reader over the SIP capture data clowk-hep3 writes. The controller
// invokes it as a single binary, dispatching on argv[1]:
//
//	expand  — (internal) emit the reader deployment (local image) on apply
//	serve   — run the reader server (the container the plugin deploys)
//	help    — operator overview
//
// The reader is a plain deployment once expanded, so its lifecycle
// (start/stop/restart/logs/delete) is the generic `vd` — no plugin command.
package main

import (
	"fmt"
	"log"
	"os"
)

// version is set via -ldflags at release time.
var version = "dev"

func main() {
	if len(os.Args) < 2 {
		emitErr("usage: voodu-hep3 <expand|serve|help|--version>")
		os.Exit(1)
	}

	switch os.Args[1] {
	case "--version", "-v", "version":
		fmt.Println(version)

	case "expand":
		if err := cmdExpand(); err != nil {
			emitErr(err.Error())
			os.Exit(1)
		}

	case "serve":
		// Long-running REST API server (the reader). Run as a container
		// by the plugin; errors go to stderr (not the JSON envelope).
		if err := cmdServe(); err != nil {
			log.Fatalf("voodu-hep3 serve: %v", err)
		}

	case "help":
		printPluginOverview()

	default:
		emitErr(fmt.Sprintf("unknown subcommand %q (want expand|serve|help)", os.Args[1]))
		os.Exit(1)
	}
}
