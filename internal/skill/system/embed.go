package system

import "embed"

const skillsRoot = "embed/system-skills"

//go:embed embed/system-skills
var skillsFS embed.FS
