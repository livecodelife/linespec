// Package plugin embeds the LineSpec provenance Claude Code plugin tree so the
// distributed linespec binary can install it (prov-2026-cd1405fb / prov-2026-a4204f20).
// The plugin is also installable directly from this directory via the marketplace
// (.claude-plugin/marketplace.json) or scripts/install-plugin.
package plugin

import "embed"

// Files is the embedded plugin tree: the manifest/marketplace under .claude-plugin,
// the hook config and scripts under hooks, and the slash command under commands.
// The `all:` prefix is required to include the dot-prefixed .claude-plugin directory.
//
//go:embed all:.claude-plugin hooks commands
var Files embed.FS
