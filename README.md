# helm-bumpcheck

Tells you which of your values keys a chart version bump breaks, and which release note says so, without a cluster.

```
$ helm bumpcheck velero --repo https://vmware-tanzu.github.io/helm-charts --from 3.2.0 --to 4.0.1 -f values.yaml
velero 3.2.0 -> 4.0.1 (app 1.11.0)
Result: 4 breaking, 0 to check, 2 unverified

BREAKING
  type changed  configuration.backupStorageLocation   map -> list, 2 keys you set
  type changed  configuration.volumeSnapshotLocation  map -> list, 6 keys you set
  removed       configuration.provider                no longer in the chart
  render        the new chart rejects your values
                ERROR: Please make .configuration.backupStorageLocation from map to slice
                ...
```

## What it checks

For the keys in your values files, against the old and new chart:

- **removed**: you set a key the new chart no longer declares.
- **type changed**: a block you set changed between map, list and scalar.
- **render**: the new chart fails `helm template` with your values while the old one rendered.
- **default changed**: a default you rely on changed.
- **free-form and undeclared**: keys the chart cannot vouch for, such as app config passed through a `config: {}` block.
- **release notes** (optional): which release-note entries concern those keys, judged by [TypeSafe Jev](https://docs.typesafe.ai).

Keys under standard Kubernetes blocks (`resources`, `nodeSelector`, annotations, labels) are summarized, not listed.

## Install

```
helm plugin install https://github.com/lov3g00d/helm-bumpcheck --version v0.1.0
```

On Helm 4, add `--verify=false`; git installs carry no signature. The install hook downloads the release binary for linux or darwin on amd64 or arm64 and checks it against `checksums.txt`. `HELM_BUMPCHECK_RELEASE_URL` points it at a mirror.

Without Helm's plugin manager: `go install github.com/lov3g00d/helm-bumpcheck@latest` and run `helm-bumpcheck`.

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

A chart whose dependencies changed in `Chart.yaml` or `requirements.yaml` without a version bump is listed as skipped, with each dependency change named; dependency upgrades are not analyzed yet.

`--notes` picks the app and chart release-note repos from each chart's `sources`. `-o json` and `-o markdown` change the output; the markdown is meant for a pull request comment. [examples/github-pr-comment.yml](examples/github-pr-comment.yml) posts it on every pull request that touches a `Chart.yaml`.

## Release notes

`--app-repo owner/name` reads the app's GitHub releases over the chart's `appVersion` range. `--chart-notes owner/name` reads the chart's own releases over the chart version range, with tags `CHART-<version>` by default (`--chart-tag-prefix` changes it).

Both need `TYPESAFE_API_KEY`. Without it the rest of the report still runs. Only release-note text and the key paths you set are sent to TypeSafe; your values never leave the machine. `GITHUB_TOKEN` or `GH_TOKEN` raises the GitHub rate limit and is needed for large chart monorepos.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | no finding matched `--fail-on` |
| 1 | error, or a chart in `diff` could not be analyzed |
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
| `NO_COLOR` | disables colour |

## Development

```
task test        # unit tests with -race
task lint        # gofmt and go vet
task install     # build and install as a local helm plugin
task test:live   # tests against the real GitHub and TypeSafe APIs
task eval        # compare Jev with keyword matching on eval/cases.yaml
```

`go test ./internal/report -update` rewrites the golden files after an intended output change.
