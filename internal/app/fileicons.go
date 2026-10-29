package app

import (
	"path/filepath"
	"strings"

	"github.com/mirzakhany/yoga/icons"
	"github.com/mirzakhany/yoga/render"
	"github.com/mirzakhany/yoga/theme"
)

// fileIcon is a tree glyph and the categorical hue it is drawn in. hue < 0
// keeps the tree's default color.
type fileIcon struct {
	icon icons.Icon
	hue  theme.Hue
}

const noHue theme.Hue = -1

var (
	codeIcon   = func(h theme.Hue) fileIcon { return fileIcon{icons.FileCode, h} }
	configIcon = fileIcon{icons.FileCog, noHue}
	dataIcon   = fileIcon{icons.Braces, theme.HueYellow}
	shellIcon  = fileIcon{icons.FileTerminal, theme.HueGreen}
	docIcon    = fileIcon{icons.FileText, theme.HueBlue}
	lockIcon   = fileIcon{icons.FileLock, noHue}
)

// fileIconNames match whole file names before extensions do; the keys are
// lower case.
var fileIconNames = map[string]fileIcon{
	"makefile": shellIcon, "gnumakefile": shellIcon, "dockerfile": {icons.Package, theme.HueBlue},
	"containerfile": {icons.Package, theme.HueBlue}, "go.mod": codeIcon(theme.HueTeal), "go.work": codeIcon(theme.HueTeal),
	"go.sum": lockIcon, "go.work.sum": lockIcon, "package-lock.json": lockIcon, "yarn.lock": lockIcon,
	"pnpm-lock.yaml": lockIcon, "cargo.lock": lockIcon, "gemfile.lock": lockIcon, "poetry.lock": lockIcon,
	"gemfile": codeIcon(theme.HueRed), "rakefile": codeIcon(theme.HueRed),
	"license": {icons.FileBadge, theme.HueYellow}, "license.md": {icons.FileBadge, theme.HueYellow},
	".gitignore": {icons.GitBranch, theme.HueOrange}, ".gitattributes": {icons.GitBranch, theme.HueOrange},
	".gitmodules": {icons.GitBranch, theme.HueOrange}, ".dockerignore": configIcon, ".editorconfig": configIcon,
	".env": {icons.FileKey, theme.HueYellow},
}

// fileIconExts are keyed by lower-case extension, dot included.
var fileIconExts = map[string]fileIcon{
	".go": codeIcon(theme.HueTeal),
	".js": codeIcon(theme.HueYellow), ".mjs": codeIcon(theme.HueYellow), ".cjs": codeIcon(theme.HueYellow), ".jsx": codeIcon(theme.HueYellow),
	".ts": codeIcon(theme.HueBlue), ".mts": codeIcon(theme.HueBlue), ".cts": codeIcon(theme.HueBlue), ".tsx": codeIcon(theme.HueBlue),
	".py": codeIcon(theme.HueBlue), ".rs": codeIcon(theme.HueOrange), ".zig": codeIcon(theme.HueOrange),
	".c": codeIcon(theme.HueBlue), ".h": codeIcon(theme.HuePurple), ".cc": codeIcon(theme.HueBlue), ".cpp": codeIcon(theme.HueBlue),
	".cxx": codeIcon(theme.HueBlue), ".hpp": codeIcon(theme.HuePurple), ".hh": codeIcon(theme.HuePurple),
	".cs": codeIcon(theme.HuePurple), ".java": codeIcon(theme.HueOrange), ".kt": codeIcon(theme.HuePurple),
	".swift": codeIcon(theme.HueOrange), ".rb": codeIcon(theme.HueRed), ".php": codeIcon(theme.HuePurple),
	".lua": codeIcon(theme.HueBlue), ".sql": {icons.Database, theme.HueOrange},
	".html": codeIcon(theme.HueOrange), ".htm": codeIcon(theme.HueOrange), ".css": codeIcon(theme.HueBlue),
	".scss": codeIcon(theme.HuePink), ".vue": codeIcon(theme.HueGreen), ".svelte": codeIcon(theme.HueOrange),
	".sh": shellIcon, ".bash": shellIcon, ".zsh": shellIcon, ".fish": shellIcon, ".ps1": shellIcon, ".mk": shellIcon,
	".json": dataIcon, ".jsonc": dataIcon, ".yaml": {icons.FileSliders, theme.HuePurple}, ".yml": {icons.FileSliders, theme.HuePurple},
	".toml": {icons.FileSliders, theme.HueOrange}, ".ini": configIcon, ".conf": configIcon, ".cfg": configIcon,
	".xml": codeIcon(theme.HueOrange), ".proto": codeIcon(theme.HueTeal), ".graphql": codeIcon(theme.HuePink),
	".md": docIcon, ".mdx": docIcon, ".txt": {icons.FileText, noHue}, ".rst": docIcon,
	".diff": {icons.FileDiff, theme.HueGreen}, ".patch": {icons.FileDiff, theme.HueGreen}, ".lock": lockIcon,
	".png": {icons.FileImage, theme.HuePurple}, ".jpg": {icons.FileImage, theme.HuePurple}, ".jpeg": {icons.FileImage, theme.HuePurple},
	".gif": {icons.FileImage, theme.HuePurple}, ".webp": {icons.FileImage, theme.HuePurple}, ".ico": {icons.FileImage, theme.HuePurple},
	".svg": {icons.FileImage, theme.HueYellow},
	".zip": {icons.FileArchive, noHue}, ".gz": {icons.FileArchive, noHue}, ".tgz": {icons.FileArchive, noHue},
	".tar": {icons.FileArchive, noHue}, ".xz": {icons.FileArchive, noHue}, ".7z": {icons.FileArchive, noHue},
	".pdf": {icons.FileText, theme.HueRed}, ".csv": {icons.FileSpreadsheet, theme.HueGreen}, ".tsv": {icons.FileSpreadsheet, theme.HueGreen},
	".mp3": {icons.FileMusic, noHue}, ".wav": {icons.FileMusic, noHue}, ".mp4": {icons.FileVideoCamera, noHue}, ".mov": {icons.FileVideoCamera, noHue},
	".pem": {icons.FileKey, theme.HueYellow}, ".key": {icons.FileKey, theme.HueYellow}, ".crt": {icons.FileKey, theme.HueYellow},
}

// folderIconNames give well-known folders their own glyph; others keep the
// tree's open and closed folders.
var folderIconNames = map[string]icons.Icon{
	".git": icons.FolderGit, ".github": icons.FolderGit, ".gitlab": icons.FolderGit,
	".vscode": icons.FolderCog, ".idea": icons.FolderCog, ".hm": icons.FolderCog, ".cursor": icons.FolderCog, ".claude": icons.FolderCog,
	"node_modules": icons.FolderArchive, "vendor": icons.FolderArchive,
	"build": icons.FolderOutput, "dist": icons.FolderOutput, "out": icons.FolderOutput, "bin": icons.FolderOutput, "target": icons.FolderOutput,
	"test": icons.FolderCheck, "tests": icons.FolderCheck, "testdata": icons.FolderCheck,
	"docs": icons.FolderBookmark, "doc": icons.FolderBookmark,
	"src": icons.FolderCode, "internal": icons.FolderCode, "cmd": icons.FolderCode, "pkg": icons.FolderCode, "lib": icons.FolderCode,
}

// treeIcon picks the file tree's glyph for path. Colors are read from the
// current theme on every paint so a theme switch recolors the tree.
func treeIcon(path string, dir, open bool) (icons.Icon, render.Color) {
	th := theme.Current()
	name := strings.ToLower(filepath.Base(path))
	if dir {
		if ic, ok := folderIconNames[name]; ok {
			return ic, th.Accent
		}
		if open {
			return icons.FolderOpen, th.Accent
		}
		return icons.Folder, th.Accent
	}
	fi, ok := fileIconNames[name]
	if !ok {
		fi, ok = fileIconExts[filepath.Ext(name)]
	}
	if !ok {
		return icons.File, th.ForegroundMuted
	}
	if fi.hue == noHue {
		return fi.icon, th.ForegroundMuted
	}
	return fi.icon, th.Categorical[fi.hue]
}
