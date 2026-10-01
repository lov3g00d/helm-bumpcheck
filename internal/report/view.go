package report

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/lov3g00d/helm-bumpcheck/internal/match"
	"github.com/lov3g00d/helm-bumpcheck/internal/values"
)

const evidencePerRow = 2

type evidence struct {
	Probability float64
	Release     string
	Section     string
	Text        string
	Notable     string
}

type row struct {
	Kind     string
	Path     string
	Detail   string
	Lines    []string
	Evidence []evidence
	More     int
}

type view struct {
	Title      string
	Verdict    string
	Breaking   []row
	Check      []row
	Unverified []row
	Why        string
	Footer     []string
}

func build(res Report) view {
	r := res.Report
	idx := evidenceIndex(res.Notes)
	v := view{Title: title(res)}

	for _, tc := range r.TypeChanged {
		row := row{Kind: "type changed", Path: tc.Path, Detail: fmt.Sprintf("%s -> %s, %d %s you set", tc.Old, tc.New, len(tc.Keys), plural(len(tc.Keys), "key", "keys"))}
		row.Evidence, row.More = pickEvidence(idx, tc.Keys)
		v.Breaking = append(v.Breaking, row)
	}
	for _, k := range r.Removed {
		row := row{Kind: "removed", Path: k, Detail: "no longer in the chart"}
		row.Evidence, row.More = pickEvidence(idx, []string{k})
		v.Breaking = append(v.Breaking, row)
	}
	if res.Render.Breaking() {
		v.Breaking = append(v.Breaking, row{Kind: "render", Detail: "the new chart rejects your values", Lines: strings.Split(res.Render.NewError, "\n")})
	}

	for _, c := range r.DefaultChanged {
		v.Check = append(v.Check, row{Kind: "default", Path: c.Path, Detail: describeChange(c)})
	}
	unchecked := []struct {
		kind string
		keys []string
	}{{"free-form", r.Freeform}, {"undeclared", r.Undeclared}}
	for _, u := range unchecked {
		for _, k := range u.keys {
			ev, more := pickEvidence(idx, []string{k})
			if len(ev) == 0 {
				v.Unverified = append(v.Unverified, row{Kind: u.kind, Path: k})
				continue
			}
			v.Check = append(v.Check, row{Kind: "notes", Path: k, Detail: u.kind + " key named in release notes", Evidence: ev, More: more})
		}
	}
	if res.Render != nil && !res.Render.OldOK && !res.Render.NewOK {
		v.Check = append(v.Check, row{Kind: "render", Detail: "neither version renders with your values, so this check is inconclusive", Lines: strings.Split(res.Render.NewError, "\n")})
	}

	v.Verdict = verdict(len(v.Breaking), len(v.Check), len(v.Unverified))
	v.Why = unverifiedReason(res.Notes)
	v.Footer = footer(res)
	return v
}

func title(res Report) string {
	t := fmt.Sprintf("%s %s -> %s", res.Chart, res.From, res.To)
	switch {
	case res.AppFrom == "" && res.AppTo == "":
	case res.AppFrom == res.AppTo:
		t += fmt.Sprintf(" (app %s)", res.AppTo)
	default:
		t += fmt.Sprintf(" (app %s -> %s)", res.AppFrom, res.AppTo)
	}
	if res.Values != "" {
		t += fmt.Sprintf(" [%s]", res.Values)
	}
	return t
}

func verdict(breaking, check, unverified int) string {
	if breaking+check+unverified == 0 {
		return "Result: nothing in this upgrade affects the values you set"
	}
	return fmt.Sprintf("Result: %d breaking, %d to check, %d unverified", breaking, check, unverified)
}

func unverifiedReason(notes []*Notes) string {
	if len(notes) == 0 {
		return "the chart does not declare these keys; release notes were not checked"
	}
	var skipped []string
	for _, n := range notes {
		if n.Skipped == "" {
			return "the chart does not declare these keys and no release note matched them"
		}
		skipped = append(skipped, n.Source+" notes skipped")
	}
	return "the chart does not declare these keys; " + strings.Join(skipped, ", ")
}

func footer(res Report) []string {
	var out []string
	if r := res.Render; r != nil && !r.Breaking() && (r.OldOK || r.NewOK) {
		if r.OldOK {
			out = append(out, "Render: both versions render with your values")
		} else {
			out = append(out, "Render: the new version renders with your values, the old one did not")
		}
	}
	for _, n := range res.Notes {
		if n.Skipped != "" {
			out = append(out, fmt.Sprintf("Release notes (%s): skipped, %s", n.Source, n.Skipped))
			continue
		}
		out = append(out, fmt.Sprintf("Release notes (%s): %s %s -> %s, %d releases, %d entries, %d questions",
			n.Source, n.Repo, n.From, n.To, n.Releases, n.Entries, n.Stats.Questions))
	}
	if n := len(res.Standard); n > 0 {
		out = append(out, fmt.Sprintf("%d %s in standard Kubernetes blocks (%s) not listed. Use -o json to list them.",
			n, plural(n, "key", "keys"), strings.Join(values.StandardBlocks(res.Standard), ", ")))
	}
	if n := len(res.Added); n > 0 {
		out = append(out, fmt.Sprintf("%d %s added in %s. Use -o json to list them.", n, plural(n, "key", "keys"), res.To))
	}
	return out
}

func evidenceIndex(notes []*Notes) map[string][]match.Match {
	idx := map[string][]match.Match{}
	for _, n := range notes {
		for _, m := range n.Matches {
			if m.Probability >= n.Threshold {
				idx[m.Key] = append(idx[m.Key], m)
			}
		}
	}
	return idx
}

func pickEvidence(idx map[string][]match.Match, keys []string) ([]evidence, int) {
	best := map[string]evidence{}
	for _, k := range keys {
		for _, m := range idx[k] {
			id := m.Release + "\x00" + m.Text
			if cur, ok := best[id]; ok && cur.Probability >= m.Probability {
				continue
			}
			best[id] = evidence{Probability: m.Probability, Release: m.Release, Section: m.Section, Text: cleanEntry(m.Text), Notable: notableTag(m.Section)}
		}
	}
	all := make([]evidence, 0, len(best))
	for _, e := range best {
		all = append(all, e)
	}
	slices.SortFunc(all, func(a, b evidence) int {
		if c := cmp.Compare(rankTag(b.Notable), rankTag(a.Notable)); c != 0 {
			return c
		}
		if c := cmp.Compare(b.Probability, a.Probability); c != 0 {
			return c
		}
		return cmp.Compare(a.Release+a.Text, b.Release+b.Text)
	})
	n := min(evidencePerRow, len(all))
	return all[:n], len(all) - n
}
