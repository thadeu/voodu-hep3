package main

import "fmt"

// printPluginOverview prints the plugin's operator-facing overview as
// plain text — `vd hep3 -h` reaches us as "help".
func printPluginOverview() {
	fmt.Print(pluginOverview)
}

const pluginOverview = `voodu-hep3 — SIP capture READER (HEP3)

Two independent pieces share one external Postgres (you create it and
pass DATABASE_URL to both):

  collector  clowk-hep3  — receives HEP3, writes SIP to Postgres.
             A PLAIN deployment with the public image; no plugin needed:

               deployment "voip" "collector" {
                 image    = "ghcr.io/thadeu/clowk-hep3:latest"
                 ports    = ["0.0.0.0:9060:9060/udp"]
                 env_from = ["voip/collector"]   # DATABASE_URL, retention, ...
                 resources { limits { cpu = "1", memory = "256Mi" } }
               }

  reader     voodu-hep3 (this plugin) — the read-only REST API the webui
             consumes through the controller's PAT proxy:

               hep3 "voip" "api" {
                 resources { limits { cpu = "0.5", memory = "128Mi" } }
               }

             expand emits a deployment running a LOCAL image (this binary
             + a runtime Dockerfile, built by the install hook) — no
             public registry. Put DATABASE_URL in its bucket:

               vd config voip/api set DATABASE_URL=postgres://...
               vd apply -f hep3-api.voodu

Lifecycle:

  The reader is a plain deployment once applied, so manage it with the
  generic voodu commands — no plugin command:

    vd get                       vd logs <scope>/<name>
    vd restart <scope>/<name>    vd stop|start <scope>/<name>
    vd delete <scope>/<name>
`
