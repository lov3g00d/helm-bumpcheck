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

	fmt.Fprintf(tw, "Cases: %d scored, %d failed. Jev was asked about %d of %d keys, the ones the CLI checks in release notes.\n", s.Scored, s.Failed, s.Asked, s.Keys)
	fmt.Fprintf(tw, "Key columns show tp/fp/fn. Pair columns add unlabelled pairs, which are not counted as fp.\n\n")
	fmt.Fprintf(tw, "case\tsplit\tasked\taffected\treleases\tkeyword\t%s\tpairs\n", jevCol)
	for _, r := range results {
		if r.Error != "" {
			continue
		}
		fmt.Fprintf(tw, "%s\t%s\t%d/%d\t%d\t%d\t%s\t%s\t%s\n", r.Case.Name, r.Case.Split, len(r.Asked), r.Keys, len(r.Case.Affected), r.Releases, cell(r.Keyword.Score), jevCell(r.Jev), jevPairCell(r.Jev))
	}

	fmt.Fprintf(tw, "\ncode meaning_changed: %s, keys values.Compare flags against meaning_changed labels\n", cell(s.Code))
	fmt.Fprintf(tw, "\nside\trecall\tprecision\tquestions\trequests\tinput tokens\test. cost\n")
	writeSide(tw, "keyword", s.Keyword)
	if s.Jev != nil {
		writeSide(tw, jevCol, *s.Jev)
		fmt.Fprintf(tw, "%s pairs: %s\n", jevCol, pairCell(*s.Jev.Pairs))
	}

	if len(s.Sweep) > 0 {
		fmt.Fprintf(tw, "\nJev threshold sweep on the tune split.\n")
		fmt.Fprintf(tw, "threshold\trecall\tprecision\tkeys\tpairs\n")
		for _, p := range s.Sweep {
			fmt.Fprintf(tw, "%.1f\t%s\t%s\t%s\t%s\n", p.Threshold, ratio(p.Score.Recall()), ratio(p.Score.Precision()), cell(p.Score), pairCell(p.Pairs))
		}
		if p := s.Pick; p != nil {
			fmt.Fprintf(tw, "\nPicked %.1f on tune by F1, ties going to the higher threshold. Holdout at %.1f: keys %s, pairs %s.\n", p.Threshold, p.Threshold, cell(p.Holdout), pairCell(p.HoldoutPairs))
		} else {
			fmt.Fprintf(tw, "\nNo threshold picked: the tune split has no affected keys.\n")
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
	fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%d\t%d\t$%.4f\n", name, ratio(s.Score.Recall()), ratio(s.Score.Precision()), s.Questions, s.Requests, s.InputTokens, s.CostUSD)
}

func jevCell(j *JevCase) string {
	if j == nil {
		return "-"
	}
	return cell(j.Score)
}

func jevPairCell(j *JevCase) string {
	if j == nil {
		return "-"
	}
	return pairCell(j.Pairs)
}

func cell(s Score) string {
	return fmt.Sprintf("%d/%d/%d", s.TP, s.FP, s.FN)
}

func pairCell(s PairScore) string {
	return fmt.Sprintf("%d/%d/%d/%d", s.TP, s.FP, s.FN, s.Unlabelled)
}

func ratio(v float64, ok bool) string {
	if !ok {
		return "n/a"
	}
	return fmt.Sprintf("%.2f", v)
}
