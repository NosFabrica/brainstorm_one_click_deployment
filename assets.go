// Package assets embeds the deployment files the brainstorm CLI writes into
// ~/.brainstorm on first run: the compose file, relay configs, and the Vespa
// application package. Keeping these embedded means the binary is the only
// thing a user needs to install — no loose files to copy around.
package assets

import "embed"

//go:embed docker-compose.yml neofry.conf strfry.conf strfry-router.conf
//go:embed all:vespa-app
var Files embed.FS
