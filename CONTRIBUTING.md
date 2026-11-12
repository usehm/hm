# Contributing to Hm

Thanks for helping. Bug reports, ideas and pull requests are all welcome.

## Before you start

- **Bugs:** open an issue with what you did, what you expected, what happened, your OS and `hm` version, and any error text.
- **Features:** open an issue first to talk it over, so a pull request doesn't go to waste.
- **Security issues:** don't open a public issue. Use GitHub's [private vulnerability reporting](https://github.com/usehm/hm/security/advisories/new).

## Building and testing

You need Go (see `go.mod` for the version).

```sh
make run    # run hm from source in the current folder
make test   # run the tests
```

Before sending a pull request:

```sh
gofmt -l .     # prints nothing
go vet ./...
make test
```

## Pull requests

- Keep each pull request to one change, with tests for what it fixes or adds.
- Match the code around you: its naming, its comments, its idioms.
- Update the README or `docs/` when you change what users see.
- New source files start with the license header:

  ```go
  // Copyright (C) 2026 Mohsen Mirzakhani
  // SPDX-License-Identifier: AGPL-3.0-only
  ```

## Contributor License Agreement

Before your first pull request can be merged, you need to sign the [Contributor License Agreement](CLA.md). A bot comments on your pull request with instructions; signing is one comment, and it covers every later contribution.

The CLA lets the project offer Hm under licenses other than the AGPL, such as a commercial license for companies that can't use the AGPL. That income keeps Hm going. You keep the copyright in your work, and every published version of Hm stays available under the AGPL.

## License

Hm is licensed under the [GNU Affero General Public License v3.0](LICENSE). The name "Hm" and the Hm logo are covered by the [trademark policy](TRADEMARKS.md), not by the license.
