# helm-bumpcheck

Tells you which of your values keys a chart version bump breaks, and which release note says so, without a cluster.

The checks are deterministic and run locally, with no server or Argo CD. Your values stay on your machine: with release notes on, only the key paths you set and the release-note text go to TypeSafe.

```
$ helm bumpcheck velero --repo https://vmware-tanzu.github.io/helm-charts --from 3.2.0 --to 4.0.1 -f values.yaml
velero 3.2.0 -> 4.0.1 (app 1.11.0)
Result: 4 breaking, 0 to check, 2 unverified

BREAKING
  type changed  configuration.backupStorageLocation   map -> list, 2 keys you set
  type changed  configuration.volumeSnapshotLocation  map -> list, 6 keys you set
  removed       configuration.provider                no longer in the chart
  render        the new chart rejects your values
                Error: execution error at (velero/templates/NOTES.txt:78:4):
                ERROR: Please make .configuration.backupStorageLocation from map to slice
                ...
```

## What it checks

For the keys in your values files, against the old and new chart:

- **removed**: you set a key the new chart no longer declares, or a key under a subchart the new chart no longer depends on.
- **type changed**: a block you set changed between map, list and scalar.
- **render**: the new chart fails `helm template` with your values while the old one rendered. helm checks a chart's `kubeVersion` against its built-in Kubernetes version, so pass `--kube-version` with your cluster's version; without it a `kubeVersion` failure is reported as not checked.
- **default changed**: a default you rely on changed.
- **meaning changed**: a key you set whose scalar default changed, or whose block gained a setting with a non-empty default, such as `image.repository` when the chart adds `image.registry: quay.io` beside it. Your value still applies, but the chart may now read it differently.
- **declared**: a key you set that the old chart did not declare and the new one does. The old chart most likely ignored it, so your value may only now take effect.
- **free-form and undeclared**: keys the chart cannot vouch for, such as app config passed through a `config: {}` block, or keys under a subchart or `global` that the parent chart does not declare.
- **release notes** (optional): which release-note entries concern those keys, judged by [TypeSafe Jev](https://docs.typesafe.ai).

Keys under standard Kubernetes blocks (`resources`, `nodeSelector`, annotations, labels) are summarized, not listed.

## Install

```
helm plugin install https://github.com/lov3g00d/helm-bumpcheck --version v0.1.0
```

Requires Helm 3.18 or later, or Helm 4. On Helm 4, add `--verify=false`; git installs carry no signature. The install hook downloads the release binary for linux or darwin on amd64 or arm64 and checks it against `checksums.txt`. `HELM_BUMPCHECK_RELEASE_URL` points it at a mirror.

Without Helm's plugin manager: `go install github.com/lov3g00d/helm-bumpcheck@latest` and run `helm-bumpcheck`.

To upgrade, uninstall and install the new version. `helm plugin update` does not move an install made with `--version`, because Helm leaves that checkout on a detached tag.

```
helm plugin uninstall bumpcheck
helm plugin install https://github.com/lov3g00d/helm-bumpcheck --version vX.Y.Z
```

On Helm 4, add `--verify=false` to the install as above.

If the install hook fails, for example on a network error, Helm keeps the plugin registered without a binary and the next install stops at `plugin already exists`. Run `helm plugin uninstall bumpcheck` first, then install again.

To remove the plugin: `helm plugin uninstall bumpcheck`.

Helm 3.17 and older cannot read the `platformHooks` in `plugin.yaml`. An install attempt there leaves a directory that stops helm from loading any plugin, and `helm plugin uninstall` cannot remove it. Delete it by hand: `rm -rf "$(helm env HELM_PLUGINS)/helm-bumpcheck"`.

## Use

One chart, two versions:

```
helm bumpcheck tempo --repo https://grafana.github.io/helm-charts \
  --from 1.10.0 --to 1.24.4 -f values.yaml --app-repo grafana/tempo
```

Every chart whose `Chart.yaml` version changed in a git range, one report per `values_*.yaml` or `values-*.yaml` in the chart directory:

```
helm bumpcheck diff --base origin/main --notes
```

A chart whose dependencies changed in `Chart.yaml`, `requirements.yaml` or their lock files without a version bump is listed as skipped, with each dependency change named; dependency upgrades are not analyzed yet. When the version did change, the dependency changes are listed under the report. Dependencies that are not vendored under `charts/` are fetched with `helm dependency build` into the extracted copy before rendering, never into your repository. A `file://` dependency inside the repository, such as a library chart at `file://../../lib/common`, is extracted from the same revision first, so the relative path resolves. One that points outside the repository skips the render check with that reason.

Both revisions are read from git objects. Symlinks in a chart are followed the way helm follows them, and the file they point to is copied in their place. A link that leaves the repository, points to nothing or loops makes the chart fail.

`--notes` picks the app and chart release-note repos from each chart's `sources`. `-o json` and `-o markdown` change the output; the markdown is meant for a pull request comment. [examples/github-pr-comment.yml](examples/github-pr-comment.yml) posts it on every pull request that touches a `Chart.yaml`, `requirements.yaml` or lock file.

## Release notes

`--app-repo owner/name` reads the app's GitHub releases over the chart's `appVersion` range. `--chart-notes owner/name` reads the chart's own releases over the chart version range, with tags `CHART-<version>` by default (`--chart-tag-prefix` changes it).

Both need `TYPESAFE_API_KEY`. Without it the rest of the report still runs. Only release-note text and the key paths you set are sent to TypeSafe; your values never leave the machine. `GITHUB_TOKEN` or `GH_TOKEN` raises the GitHub rate limit and is needed for large chart monorepos: without a token GitHub lists only the first 1000 releases. `--max-pages` (default 40, 10 without a token) caps the pages of 100 releases read per repo, and the report says when the listing stopped before the `from` release.

## Rendered manifests

helm-bumpcheck does not diff rendered manifests. [helm-diff](https://github.com/databus23/helm-diff) 3.15.7 and later does it without a cluster through `helm diff local`, given both chart directories. For a chart in git, check out the base next to your branch:

```
git worktree add ../base origin/main
helm diff local ../base/charts/tempo charts/tempo -f charts/tempo/values-prod.yaml
git worktree remove ../base
```

Run `helm dependency build` in both directories first if the chart has dependencies. For a chart from a repository, pull both versions:

```
helm pull tempo --repo https://grafana.github.io/helm-charts --version 1.10.0 --untar --untardir old
helm pull tempo --repo https://grafana.github.io/helm-charts --version 1.24.4 --untar --untardir new
helm diff local old/tempo new/tempo -f values.yaml
```

## Exit codes

| Code | Meaning |
|---|---|
| 0 | no finding matched `--fail-on` |
| 1 | error, or a chart in `diff` could not be analyzed, including one whose `Chart.yaml` or lock file in `--head` does not parse |
| 2 | a finding matched `--fail-on removed,type,render,default` |

Release-note matches never fail a run; their probabilities vary slightly between calls.

## Environment

| Variable | Use |
|---|---|
| `TYPESAFE_API_KEY` | enables release-note matching |
| `TYPESAFE_MODEL` | Jev model, default `jev-1.13.0` |
| `TYPESAFE_BASE_URL` | TypeSafe API endpoint |
| `GITHUB_TOKEN`, `GH_TOKEN` | GitHub API token |
| `GITHUB_API_URL` | GitHub API endpoint, for GitHub Enterprise |
| `HELM_BIN` | helm binary, set by helm for plugins |
| `NO_COLOR` | disables colour when set and not empty |

## Development

```
task test        # unit tests with -race
task lint        # gofmt, go vet, go fix, go mod tidy, govulncheck and shellcheck
task install     # build and install as a local helm plugin
task test:live   # tests against the real GitHub and TypeSafe APIs
task eval        # compare Jev with keyword matching on eval/cases.yaml
```

Each eval case pins a chart, two versions and a values file taken from a public GitOps repository at the commit before the bump. The eval pulls both versions, runs the same comparison as the CLI and sends Jev only the keys the CLI would send. Cases are labelled per key and per release-note entry, and split into `tune` and `holdout`: the threshold is picked on `tune` and reported on `holdout`. `task eval -- -skip-jev` scores the keyword baseline without calling TypeSafe.

`go test ./internal/report -update` rewrites the golden files after an intended output change.
