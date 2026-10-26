module github.com/mirzakhany/hm

go 1.26.2

require (
	github.com/anthropics/anthropic-sdk-go v1.84.0
	github.com/fsnotify/fsnotify v1.10.1
	github.com/mirzakhany/yoga v0.26.2
	github.com/modelcontextprotocol/go-sdk v1.8.0
	github.com/zalando/go-keyring v0.2.8
	golang.org/x/net v0.58.0
	gopkg.in/yaml.v3 v3.0.1
)

require (
	codeberg.org/go-pdf/fpdf v0.12.0 // indirect
	github.com/BurntSushi/freetype-go v0.0.0-20160129220410-b763ddbfe298 // indirect
	github.com/BurntSushi/graphics-go v0.0.0-20160129215708-b43f31a4a966 // indirect
	github.com/BurntSushi/toml v1.6.0 // indirect
	github.com/BurntSushi/xgb v0.0.0-20210121224620-deaf085860bc // indirect
	github.com/BurntSushi/xgbutil v0.0.0-20190907113008-ad855c713046 // indirect
	github.com/ByteArena/poly2tri-go v0.0.0-20170716161910-d102ad91854f // indirect
	github.com/andybalholm/brotli v1.2.2 // indirect
	github.com/bahlo/generic-list-go v0.2.0 // indirect
	github.com/benoitkugler/textlayout v0.3.2 // indirect
	github.com/benoitkugler/textprocessing v0.0.6 // indirect
	github.com/buger/jsonparser v1.1.2 // indirect
	github.com/cogentcore/webgpu v0.23.0 // indirect
	github.com/danieljoos/wincred v1.2.3 // indirect
	github.com/go-fonts/latin-modern v0.3.3 // indirect
	github.com/go-gl/glfw/v3.3/glfw v0.0.0-20260802143932-8fa725040a18 // indirect
	github.com/go-logr/logr v1.4.2 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/go-text/typesetting v0.3.4 // indirect
	github.com/godbus/dbus/v5 v5.2.2 // indirect
	github.com/golang/freetype v0.0.0-20170609003504-e2365dfdc4a0 // indirect
	github.com/google/jsonschema-go v0.4.3 // indirect
	github.com/invopop/jsonschema v0.14.0 // indirect
	github.com/mattn/go-pointer v0.0.1 // indirect
	github.com/nfnt/resize v0.0.0-20180221191011-83c6a9932646 // indirect
	github.com/pb33f/ordered-map/v2 v2.3.1 // indirect
	github.com/segmentio/asm v1.1.3 // indirect
	github.com/segmentio/encoding v0.5.4 // indirect
	github.com/srwiley/rasterx v0.0.0-20220730225603-2ab79fcdd4ef // indirect
	github.com/srwiley/scanx v0.0.0-20190309010443-e94503791388 // indirect
	github.com/standard-webhooks/standard-webhooks/libraries v0.0.1 // indirect
	github.com/tc-hib/winres v0.3.1 // indirect
	github.com/tdewolff/canvas v0.0.0-20260901160717-dae8cd8e19a7 // indirect
	github.com/tdewolff/font v0.0.0-20260822205238-d0d2f004b117 // indirect
	github.com/tdewolff/minify/v2 v2.24.17 // indirect
	github.com/tdewolff/parse/v2 v2.8.16 // indirect
	github.com/tidwall/gjson v1.18.0 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.1 // indirect
	github.com/tidwall/sjson v1.2.5 // indirect
	github.com/tree-sitter-grammars/tree-sitter-xml v0.7.0 // indirect
	github.com/tree-sitter-grammars/tree-sitter-yaml v0.7.2 // indirect
	github.com/tree-sitter/go-tree-sitter v0.25.0 // indirect
	github.com/tree-sitter/tree-sitter-bash v0.25.1 // indirect
	github.com/tree-sitter/tree-sitter-css v0.25.0 // indirect
	github.com/tree-sitter/tree-sitter-go v0.25.0 // indirect
	github.com/tree-sitter/tree-sitter-html v0.23.2 // indirect
	github.com/tree-sitter/tree-sitter-javascript v0.25.0 // indirect
	github.com/tree-sitter/tree-sitter-json v0.24.8 // indirect
	github.com/tree-sitter/tree-sitter-python v0.23.6 // indirect
	github.com/tree-sitter/tree-sitter-rust v0.24.2 // indirect
	github.com/tree-sitter/tree-sitter-typescript v0.23.2 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	github.com/yuin/goldmark v1.8.5 // indirect
	go.opentelemetry.io/auto/sdk v1.1.0 // indirect
	go.opentelemetry.io/otel v1.33.0 // indirect
	go.opentelemetry.io/otel/metric v1.33.0 // indirect
	go.opentelemetry.io/otel/trace v1.33.0 // indirect
	go.yaml.in/yaml/v4 v4.0.0-rc.2 // indirect
	golang.org/x/image v0.45.0 // indirect
	golang.org/x/oauth2 v0.35.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.41.0 // indirect
	golang.org/x/time v0.15.0 // indirect
	modernc.org/knuth v0.6.0 // indirect
	modernc.org/token v1.1.0 // indirect
	star-tex.org/x/tex v0.7.1 // indirect
)

// Local checkout so LSP and highlighter additions can land in yoga first.
replace github.com/mirzakhany/yoga => ../yoga

tool github.com/mirzakhany/yoga/cmd/yoga
