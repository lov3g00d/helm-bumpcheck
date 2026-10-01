package eval

import (
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"
)

func WriteJSON(w io.Writer, results []CaseResult, s Summary) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(struct {
		Summary Summary      `json:"summary"`
		Cases   []CaseResult `json:"cases"`
	}{s, results})
}

func WriteText(w io.Writer, results []CaseResult, s Summary) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	jevCol := fmt.Sprintf("jev@%.2f", s.Threshold)

	fmt.Fprintf(tw, "Cases: %d scored, %d failed. Columns show tp/fp/fn.\n\n", s.Scored, s.Failed)
	fmt.Fprintf(tw, "case\taffected\treleases\tkeyword\t%s\n", jevCol)
	for _, r := range results {
		if r.Error != "" {
			continue
		}
		fmt.Fprintf(tw, "%s\t%d\t%d\t%s\t%s\n", r.Case.Name, len(r.Case.Affected), r.Releases, cell(r.Keyword.Score), jevCell(r.Jev))
	}

	fmt.Fprintf(tw, "\nside\trecall\tprecision\trequests\tinput tokens\test. cost\n")
	writeSide(tw, "keyword", s.Keyword)
	if s.Jev != nil {
		writeSide(tw, jevCol, *s.Jev)
	}

	if len(s.Sweep) > 0 {
		fmt.Fprintf(tw, "\nJev threshold sweep. Choose a threshold on other cases, not these.\n")
		fmt.Fprintf(tw, "threshold\trecall\tprecision\n")
		for _, p := range s.Sweep {
			fmt.Fprintf(tw, "%.1f\t%s\t%s\n", p.Threshold, ratio(p.Score.Recall()), ratio(p.Score.Precision()))
		}
	}

	if s.Failed > 0 {
		fmt.Fprintf(tw, "\nFailed cases:\n")
		for _, r := range results {
			if r.Error != "" {
				fmt.Fprintf(tw, "  %s: %s\n", r.Case.Name, r.Error)
			}
		}
	}
	return tw.Flush()
}

func writeSide(w io.Writer, name string, s Side) {
	fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%d\t$%.4f\n", name, ratio(s.Score.Recall()), ratio(s.Score.Precision()), s.Requests, s.InputTokens, s.CostUSD)
}

func jevCell(j *JevCase) string {
	if j == nil {
		return "-"
	}
	return cell(j.Score)
}

func cell(s Score) string {
	return fmt.Sprintf("%d/%d/%d", s.TP, s.FP, s.FN)
}

func ratio(v float64, ok bool) string {
	if !ok {
		return "n/a"
	}
	return fmt.Sprintf("%.2f", v)
}
