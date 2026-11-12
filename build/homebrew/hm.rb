# Homebrew cask for hm, rendered into usehm/homebrew-tap by the homebrew job
# in .github/workflows/release.yml, which fills in @VERSION@ and the
# @SHA256_...@ digests. Edit it here, not in the tap.
#
#   brew install --cask usehm/tap/hm
cask "hm" do
  arch arm: "arm64", intel: "amd64"

  version "@VERSION@"
  sha256 arm:   "@SHA256_ARM@",
         intel: "@SHA256_INTEL@"

  url "https://github.com/usehm/hm/releases/download/v#{version}/hm-macos-v#{version}-#{arch}.dmg",
      verified: "github.com/usehm/hm/"
  name "Hm"
  desc "Native code editor with a built-in AI coding agent"
  homepage "https://usehm.dev/"

  livecheck do
    url :url
    strategy :github_latest
  end

  depends_on :macos

  # hm.sh is the `hm` command: `hm .` opens a folder in a new window,
  # `hm agent ...` runs the agent in the terminal. Keep it in step with
  # cliScript in internal/app/cli.go, which writes the same launcher for
  # "Install 'hm' Command in PATH".
  app "Hm.app"
  binary "hm.sh", target: "hm"

  preflight_steps do
    write_file "hm.sh", <<~SH, base: :staged_path
      #!/bin/sh
      # hm — open a folder in hm. Installed by the Homebrew cask.
      set -e
      app='{{appdir}}/Hm.app'
      if [ "${1-}" = agent ]; then
        exec "$app/Contents/MacOS/Hm" "$@"
      fi
      if [ $# -gt 0 ]; then
        target=$1
        shift
        if [ -f "$target" ]; then
          target=$(dirname -- "$target")
        fi
        if [ ! -d "$target" ]; then
          echo "hm: $target: no such folder" >&2
          exit 1
        fi
        set -- "$(cd -- "$target" && pwd -P)" "$@"
      fi
      exec open -n -a "$app" --args "$@"
    SH
    set_permissions "hm.sh", "0755", base: :staged_path
  end

  zap trash: [
    "~/.config/hm",
    "~/Library/Application Support/hm",
    "~/Library/Caches/me.mirzakhani.hm",
    "~/Library/Preferences/me.mirzakhani.hm.plist",
    "~/Library/Saved Application State/me.mirzakhani.hm.savedState",
  ]
end
