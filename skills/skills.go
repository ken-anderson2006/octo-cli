// Package skills embeds the agent-facing SKILL.md documents so the binary can
// export them via `octo-cli skills` without shipping separate files alongside it.
package skills

import (
	"embed"
	"io/fs"
)

// FS holds every markdown doc under each <skill-name>/ directory: the skill's
// own SKILL.md plus its reference files, keyed by relative path. Listing uses
// */SKILL.md; the sibling .md files are reference docs loaded on demand.
//
//go:embed */*.md
var FS embed.FS

// Manifest is the embedded skills/manifest.json: the operation-level Skill
// navigation overrides (level / advice / section), per-skill short summaries,
// and reference-file topic registrations that the MCP server merges with each
// SKILL.md's frontmatter `services:` declaration. It ships in the same binary
// as FS and the OpenAPI specs, so the three can never drift across a release.
//
//go:embed manifest.json
var Manifest []byte

//go:embed framework/*/*.md
var frameworkFiles embed.FS

// Framework returns the skills paired with the native v2 MCP transport.
func Framework() fs.FS {
	source, err := fs.Sub(frameworkFiles, "framework")
	if err != nil {
		panic(err)
	}
	return source
}
