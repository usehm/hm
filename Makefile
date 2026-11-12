TAG_NAME ?= $(shell git describe --tags --abbrev=0 2>/dev/null || echo v0.0.0)

# Packaging runs through the yoga CLI (see yoga.toml); make install_deps
# installs the version go.mod builds the app with. Artifacts land in dist/<os>/.
YOGA ?= yoga
YOGA_VERSION = $(shell go list -m -f '{{.Version}}' github.com/mirzakhany/yoga)
ARCH ?= $(shell go env GOARCH)
APP = dist/darwin/Hm.app

.PHONY: install_deps
install_deps:
	go install github.com/mirzakhany/yoga/cmd/yoga@$(YOGA_VERSION)

.PHONY: run
run:
	go run ./cmd/hm .

.PHONY: test
test:
	go test ./...

# Redraws build/appicon-1024.png and build/appicon.png from the Swift source.
.PHONY: icon
icon:
	swift build/icon/appicon.swift build/appicon-1024.png
	sips -z 512 512 build/appicon-1024.png --out build/appicon.png >/dev/null

# Unsigned (ad-hoc) Hm.app and DMG for this Mac's architecture. The binary
# reports version "dev", so a local build never offers to update itself.
.PHONY: app
app:
	$(YOGA) package -os darwin -arch $(ARCH) -version $(TAG_NAME) \
		-ldflags "-X github.com/usehm/hm/internal/version.Version=dev"

# Builds Hm.app, copies it to /Applications and installs the `hm` command.
# Inside the app, "Install 'hm' Command in PATH" does the same last step.
.PHONY: install
install: app
	rm -rf /Applications/Hm.app
	cp -R $(APP) /Applications/Hm.app
	/Applications/Hm.app/Contents/MacOS/Hm -install-cli

.PHONY: clean
clean:
	rm -rf ./dist

.PHONY: build_linux
build_linux:
	$(YOGA) package -os linux -version $(TAG_NAME)

.PHONY: build_windows
build_windows:
	$(YOGA) package -os windows -arch amd64 -version $(TAG_NAME)

# Tags VERSION on main and pushes the tag; .github/workflows/release.yml
# builds, signs and publishes the release. See RELEASING.md.
#   make release VERSION=v0.1.0
.PHONY: release
release:
	@echo "$(VERSION)" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$$' || { echo "usage: make release VERSION=vX.Y.Z[-pre]"; exit 2; }
	@test "$$(git branch --show-current)" = main || { echo "release from main"; exit 1; }
	@test -z "$$(git status --porcelain --untracked-files=no)" || { echo "commit or stash your changes first"; exit 1; }
	git fetch origin main --tags
	@test "$$(git rev-parse HEAD)" = "$$(git rev-parse origin/main)" || { echo "main is not the same as origin/main; push or pull first"; exit 1; }
	@! git rev-parse -q --verify "refs/tags/$(VERSION)" >/dev/null || { echo "tag $(VERSION) exists"; exit 1; }
	git tag -a $(VERSION) -m "hm $(VERSION)"
	git push origin $(VERSION)
