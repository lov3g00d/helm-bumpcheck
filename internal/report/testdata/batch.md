<!-- helm-bumpcheck -->
## helm bumpcheck

4 charts bumped between 8387ab7ac96b and 81b35ce08326: 2 reports checked, 1 skipped, 1 failed.

| Chart | Values | Result |
|---|---|---|
| `velero 3.2.0 -> 4.0.1` | `values_prod.yaml` | 3 breaking, 0 to check, 1 unverified |
| `app 1.0.0 -> 2.0.0 \| x @octo-org/admins <img src=https://evil.example/p.png> [l](https://evil.example)  click` | `values_a\|b.yaml` | nothing in this upgrade affects the values you set |

---

### helm bumpcheck: `velero 3.2.0 -> 4.0.1 (app 1.10.0) [values_prod.yaml]`

**3 breaking, 0 to check, 1 unverified**

#### Breaking

| | Key | Change |
|---|---|---|
| type changed | `configuration.backupStorageLocation` | map -&gt; list, 2 keys you set |
| removed | `configuration.provider` | no longer in the chart |
| render |  | the new chart rejects your values |

<details><summary>Evidence</summary>

- `configuration.backupStorageLocation`
  - note 0.71, velero-4.0.0 What's Changed: \[velero\] Add support for multiple backupstoragelocations and volumesnapshotlocations
- render
  ```
  Error: execution error at (velero/templates/NOTES.txt:78:4):
  ERROR: Please make .configuration.backupStorageLocation from map to slice
  ```

</details>

<details><summary>Unverified (1): the chart does not declare these keys and no release note matched them</summary>

- free-form `schedules.daily.schedule`

</details>

<sub>Release notes (chart): vmware-tanzu/helm-charts 3.2.0 -&gt; 4.0.1, 2 releases, 4 entries, 8 questions</sub><br>
<sub>Release notes (app): skipped, the version did not change</sub><br>
<sub>2 keys added in 4.0.1. Use -o json to list them.</sub><br>

---

### helm bumpcheck: `app 1.0.0 -> 2.0.0 | x @octo-org/admins <img src=https://evil.example/p.png> [l](https://evil.example)  click [values_a|b.yaml]`

**nothing in this upgrade affects the values you set**

**Could not be analyzed**

- `charts/broken`: helm template charts/broken: exit status 1: Error: bad @&#8203;octo-org/admins &lt;img src=x&gt; second line

<details><summary>Skipped</summary>

- `charts/lib`: no values files match values_*.yaml, values-*.yaml

</details>
