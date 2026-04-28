# Third-Party Licenses

This document lists third-party software linked into spk-mail. Section 1
covers Go modules (server + desktop binary). Section 2 covers npm
packages bundled into the frontend.

## 1. Go modules

This section lists Go modules linked into the spk-mail binary (union of
`go build ./cmd/spk-mail` and `go build -tags=wails ./cmd/spk-mail`),
their detected licenses, and links to upstream LICENSE files.

The list was generated with [`go-licenses`](https://github.com/google/go-licenses).
To regenerate:

    go install github.com/google/go-licenses@latest
    go-licenses csv ./cmd/spk-mail > /tmp/default.csv
    GOFLAGS='-tags=wails' go-licenses csv ./cmd/spk-mail > /tmp/wails.csv
    sort -u /tmp/default.csv /tmp/wails.csv

The `sort -u` output is raw CSV (`module,LICENSE-url,license`); paste each
row into the `### Modules` table below as `| <module> | <license> | <url> |`,
then re-run `make build` to confirm nothing was lost. The `### Summary`
counts must be re-tallied by hand from the new module list.

Apache-2.0 modules with their own NOTICE files have their attribution
notices reproduced verbatim in the project's `NOTICE` file at the repo
root.

### Summary

| License | Module count |
|---|---|
| BSD-3-Clause | 20 |
| MIT | 19 |
| Apache-2.0 | 7 |
| BSD-2-Clause | 4 |
| MPL-2.0 | 1 |

### Modules

| Module | License | URL |
|---|---|---|
| `github.com/go-git/go-billy/v5` | Apache-2.0 | https://github.com/go-git/go-billy/blob/v5.8.0/LICENSE |
| `github.com/go-git/go-git/v5` | Apache-2.0 | https://github.com/go-git/go-git/blob/v5.17.1/LICENSE |
| `github.com/golang/groupcache/lru` | Apache-2.0 | https://github.com/golang/groupcache/blob/2c02b8208cf8/LICENSE |
| `github.com/pjbgf/sha1cd` | Apache-2.0 | https://github.com/pjbgf/sha1cd/blob/v0.5.0/LICENSE |
| `github.com/skeema/knownhosts` | Apache-2.0 | https://github.com/skeema/knownhosts/blob/v1.3.2/LICENSE |
| `github.com/spf13/cobra` | Apache-2.0 | https://github.com/spf13/cobra/blob/v1.10.2/LICENSE.txt |
| `github.com/xanzy/ssh-agent` | Apache-2.0 | https://github.com/xanzy/ssh-agent/blob/v0.3.3/LICENSE |
| `github.com/emirpasic/gods` | BSD-2-Clause | https://github.com/emirpasic/gods/blob/v1.18.1/LICENSE |
| `github.com/godbus/dbus/v5` | BSD-2-Clause | https://github.com/godbus/dbus/blob/v5.2.2/LICENSE |
| `github.com/pkg/browser` | BSD-2-Clause | https://github.com/pkg/browser/blob/5ac0b6a4141c/LICENSE |
| `gopkg.in/warnings.v0` | BSD-2-Clause | https://github.com/go-warnings/warnings/blob/v0.1.2/LICENSE |
| `dario.cat/mergo` | BSD-3-Clause | https://github.com/imdario/mergo/blob/v1.0.2/LICENSE |
| `github.com/cloudflare/circl` | BSD-3-Clause | https://github.com/cloudflare/circl/blob/v1.6.3/LICENSE |
| `github.com/go-git/gcfg` | BSD-3-Clause | https://github.com/go-git/gcfg/blob/3a3c6141e376/LICENSE |
| `github.com/google/uuid` | BSD-3-Clause | https://github.com/google/uuid/blob/v1.6.0/LICENSE |
| `github.com/gorilla/css/scanner` | BSD-3-Clause | https://github.com/gorilla/css/blob/v1.0.1/LICENSE |
| `github.com/microcosm-cc/bluemonday` | BSD-3-Clause | https://github.com/microcosm-cc/bluemonday/blob/v1.0.27/LICENSE.md |
| `github.com/ProtonMail/go-crypto` | BSD-3-Clause | https://github.com/ProtonMail/go-crypto/blob/v1.3.0/LICENSE |
| `github.com/remyoudompheng/bigfft` | BSD-3-Clause | https://github.com/remyoudompheng/bigfft/blob/24d4a6f8daec/LICENSE |
| `github.com/spf13/pflag` | BSD-3-Clause | https://github.com/spf13/pflag/blob/v1.0.10/LICENSE |
| `golang.org/x/crypto` | BSD-3-Clause | https://cs.opensource.google/go/x/crypto/+/v0.50.0:LICENSE |
| `golang.org/x/crypto/pbkdf2` | BSD-3-Clause | https://cs.opensource.google/go/x/crypto/+/v0.50.0:LICENSE |
| `golang.org/x/image` | BSD-3-Clause | https://cs.opensource.google/go/x/image/+/v0.39.0:LICENSE |
| `golang.org/x/net` | BSD-3-Clause | https://cs.opensource.google/go/x/net/+/v0.52.0:LICENSE |
| `golang.org/x/net/html` | BSD-3-Clause | https://cs.opensource.google/go/x/net/+/v0.52.0:LICENSE |
| `golang.org/x/sys` | BSD-3-Clause | https://cs.opensource.google/go/x/sys/+/v0.43.0:LICENSE |
| `golang.org/x/sys/unix` | BSD-3-Clause | https://cs.opensource.google/go/x/sys/+/v0.43.0:LICENSE |
| `golang.org/x/text` | BSD-3-Clause | https://cs.opensource.google/go/x/text/+/v0.36.0:LICENSE |
| `modernc.org/mathutil` | BSD-3-Clause | https://gitlab.com/cznic/mathutil/-/blob/master/LICENSE |
| `modernc.org/memory` | BSD-3-Clause | https://gitlab.com/cznic/memory/blob/v1.11.0/LICENSE-GO |
| `modernc.org/sqlite` | BSD-3-Clause | https://gitlab.com/cznic/sqlite/blob/v1.50.0/LICENSE |
| `github.com/adrg/xdg` | MIT | https://github.com/adrg/xdg/blob/v0.5.3/LICENSE |
| `github.com/aymerick/douceur` | MIT | https://github.com/aymerick/douceur/blob/v0.2.0/LICENSE |
| `github.com/bep/debounce` | MIT | https://github.com/bep/debounce/blob/v1.2.1/LICENSE |
| `github.com/dustin/go-humanize` | MIT | https://github.com/dustin/go-humanize/blob/v1.0.1/LICENSE |
| `github.com/emersion/go-imap/v2` | MIT | https://github.com/emersion/go-imap/blob/v2.0.0-beta.8/LICENSE |
| `github.com/emersion/go-message` | MIT | https://github.com/emersion/go-message/blob/v0.18.2/LICENSE |
| `github.com/emersion/go-sasl` | MIT | https://github.com/emersion/go-sasl/blob/b788ff22d5a6/LICENSE |
| `github.com/jbenet/go-context/io` | MIT | https://github.com/jbenet/go-context/blob/d14ea06fba99/LICENSE |
| `github.com/kevinburke/ssh_config` | MIT | https://github.com/kevinburke/ssh_config/blob/v1.4.0/LICENSE |
| `github.com/klauspost/cpuid/v2` | MIT | https://github.com/klauspost/cpuid/blob/v2.3.0/LICENSE |
| `github.com/leaanthony/u` | MIT | https://github.com/leaanthony/u/blob/v1.1.1/LICENSE |
| `github.com/lmittmann/tint` | MIT | https://github.com/lmittmann/tint/blob/v1.1.2/LICENSE |
| `github.com/mattn/go-isatty` | MIT | https://github.com/mattn/go-isatty/blob/v0.0.20/LICENSE |
| `github.com/samber/lo` | MIT | https://github.com/samber/lo/blob/v1.52.0/LICENSE |
| `github.com/sergi/go-diff/diffmatchpatch` | MIT | https://github.com/sergi/go-diff/blob/v1.4.0/LICENSE |
| `github.com/wailsapp/wails/v3` | MIT | https://github.com/wailsapp/wails/blob/v3.0.0-alpha.78/v3/LICENSE |
| `github.com/zalando/go-keyring` | MIT | https://github.com/zalando/go-keyring/blob/v0.2.8/LICENSE |
| `gopkg.in/yaml.v3` | MIT | https://github.com/go-yaml/yaml/blob/v3.0.1/LICENSE |
| `modernc.org/libc` | MIT | https://gitlab.com/cznic/libc/blob/v1.72.0/LICENSE-3RD-PARTY.md |
| `github.com/cyphar/filepath-securejoin` | MPL-2.0 | https://github.com/cyphar/filepath-securejoin/blob/v0.6.1/COPYING.md |

### License texts

The full text of each license can be found at the URL listed above. The
canonical text of common licenses is also included in this repository:

- **Apache-2.0** — see `LICENSE` (root) for the full text; `NOTICE`
  reproduces the attribution notices required by §4(d) for Apache-2.0
  modules that ship a NOTICE file.
- **MIT, BSD-2-Clause, BSD-3-Clause, MPL-2.0** — the upstream LICENSE
  file is reachable via the URL column above. spk-mail does not modify or
  redistribute the source of these dependencies; binaries embed the
  compiled object code per the terms of the respective licenses.

## 2. Frontend npm dependencies

The React frontend ships a separately-bundled JavaScript blob under
`frontend/dist/` and embeds it via Go `embed`. The list below covers
runtime (`dependencies`) packages only — build-only tooling (Tailwind,
Vite, Vitest, TypeScript, ESLint, Playwright) lives in
`devDependencies` and does not ship in the distributed bundle.

Generated with [`license-checker`](https://www.npmjs.com/package/license-checker):

    cd frontend && npx license-checker --production --excludePrivatePackages --json

### Summary

| License | Package count |
|---|---|
| MIT | 6 |

### Packages

| Package | License | Repository |
|---|---|---|
| `@wailsio/runtime@3.0.0-alpha.78` | MIT | https://github.com/wailsapp/wails |
| `csstype@3.2.3` | MIT | https://github.com/frenic/csstype |
| `react@19.2.5` | MIT | https://github.com/facebook/react |
| `react-dom@19.2.5` | MIT | https://github.com/facebook/react |
| `scheduler@0.27.0` | MIT | https://github.com/facebook/react |
| `zustand@5.0.12` | MIT | https://github.com/pmndrs/zustand |

All six are MIT-licensed; none ship a `NOTICE` file. MIT requires the
copyright notice and license text be retained in distribution — the
bundler (Vite) injects them into `frontend/dist/` source maps and
license comments are preserved by default.

