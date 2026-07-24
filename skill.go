package muxray

import _ "embed"

// Skill is the contents of skills/muxray/SKILL.md, the drop-in agent skill
// definition (frontmatter + instructions) that tells an agent when and how to
// drive muxray. It is embedded so `muxray skill` works from the shipped binary
// alone — an agent that has muxray on PATH can discover and install the skill
// without the repo. Like Usage, it cannot drift from the committed file because
// it IS the committed file.
//
//go:embed skills/muxray/SKILL.md
var Skill string
