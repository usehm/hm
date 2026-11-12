# Releasing hm

```sh
make release VERSION=v0.1.0
```

That checks you are on an up-to-date, clean `main`, then tags and pushes `v0.1.0`. The tag starts [`.github/workflows/release.yml`](.github/workflows/release.yml), which:

1. runs the tests;
2. creates a **draft** GitHub release with generated notes;
3. builds, in parallel:
   - `hm-macos-vX.Y.Z-arm64.dmg` and `-amd64.dmg`: signed with the Developer ID, notarized and stapled;
   - `hm-linux-vX.Y.Z-amd64.tar.xz` and `-arm64.tar.xz`;
   - `hm-windows-vX.Y.Z-amd64.zip`;
   and signs each one for the in-app updater (`.sig` beside it);
4. checks all ten files are attached and **publishes** the release. Only now do the in-app updater and `releases/latest` see it;
5. updates the Homebrew cask in [usehm/homebrew-tap](https://github.com/usehm/homebrew-tap).

A tag with a suffix, such as `v0.2.0-rc.1`, makes a prerelease: the in-app updater offers it only to people with `updates.channel: prerelease`, and Homebrew is not updated.

If a job fails, fix the cause and **re-run the failed jobs** from the Actions page. Uploads replace what an earlier attempt left, and the release stays a draft until everything is there. To drop a failed release entirely, delete the draft and the tag (`git push origin :refs/tags/vX.Y.Z`).

## One-time setup

### Repository secrets

Settings → Secrets and variables → Actions → New repository secret. The release checks for all of them first and names any that are missing.

| Secret | What |
|---|---|
| `UPDATE_SIGNING_KEY` | The private seed matching `internal/update/release_key.pub`. Without the matching seed, released apps reject every update. If it is lost, run `go run ./build/updatesign genkey -out signing.key`, commit the new `release_key.pub`, and store `signing.key` here; apps released before then cannot verify updates and fall back to a manual download. |
| `APPLE_DEVELOPER_CERTIFICATE_P12` | The Developer ID Application certificate and key, exported from Keychain Access as .p12, base64-encoded (`base64 -i cert.p12 \| pbcopy`). Chapar's can be reused. |
| `APPLE_DEVELOPER_CERTIFICATE_PASSWORD` | The .p12 export password |
| `APPLE_SIGNING_IDENTITY` | e.g. `Developer ID Application: Mohsen Mirzakhani (TEAMID)` |
| `APPLE_ID` | The Apple ID that notarizes |
| `APPLE_APP_SPECIFIC_PASSWORD` | An app-specific password for that Apple ID (appleid.apple.com) |
| `APPLE_TEAM_ID` | The 10-character team ID |
| `HOMEBREW_TAP_TOKEN` | A fine-grained personal access token with **Contents: read and write** on `usehm/homebrew-tap` only |

### The Homebrew tap

Create the public repository `usehm/homebrew-tap` with a README, so it has a first commit. The release writes `Casks/hm.rb` there from [`build/homebrew/hm.rb`](build/homebrew/hm.rb); edit the cask in this repository, not in the tap. Users install with:

```sh
brew install --cask usehm/tap/hm
```

The cask installs `Hm.app` and the `hm` command. The app notices it came from Homebrew and updates through `brew upgrade --cask hm`.

## Not signed yet

- **Windows:** the zip is not Authenticode-signed, so SmartScreen warns on first run until the app builds a reputation, or until there is a code signing certificate.
- **Linux:** the tar.xz is signed only for the in-app updater.
