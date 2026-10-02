package cli

import (
	"context"
	"slices"
	"sync"
	"testing"

	"github.com/lov3g00d/helm-bumpcheck/internal/jev"
	"github.com/lov3g00d/helm-bumpcheck/internal/match"
	"github.com/lov3g00d/helm-bumpcheck/internal/notes"
	"github.com/lov3g00d/helm-bumpcheck/internal/values"
)

type recordingJev struct {
	mu    sync.Mutex
	asked map[string][]string
}

func (r *recordingJev) Evaluate(ctx context.Context, state any, questions map[string]jev.Question) (*jev.Response, error) {
	entry := state.(map[string]string)["entry"]
	resp := &jev.Response{Answers: map[string]jev.Answer{}}
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, q := range questions {
		r.asked[entry] = append(r.asked[entry], q.Instructions.(map[string]any)["config_key"].(string))
		resp.Answers[id] = jev.Answer{Type: "noul", Noul: 0.1}
	}
	slices.Sort(r.asked[entry])
	return resp, nil
}

func TestAskNotesSendsMeaningAndLinkedKeys(t *testing.T) {
	ev := &recordingJev{asked: map[string][]string{}}
	d := &deps{jev: ev}
	cs := &chartStage{spec: spec{name: "velero"}, memo: match.NewMemo()}
	f := &fetchedNotes{
		src:      notesSource{kind: "chart", repo: "vmware-tanzu/helm-charts", tagPrefix: "velero-", from: "3.2.0", to: "4.0.1"},
		releases: 1,
		entries: []notes.Entry{
			{Release: "velero-4.0.0", Section: "What's Changed", Text: "Add support for multiple backupstoragelocations"},
			{Release: "velero-4.0.0", Section: "What's Changed", Text: "Refactor the image settings"},
		},
	}
	r := values.Report{
		Removed:        []string{"configuration.provider"},
		TypeChanged:    []values.TypeChange{{Path: "configuration.backupStorageLocation", Old: "map", New: "list", Keys: []string{"configuration.backupStorageLocation.bucket"}}},
		MeaningChanged: []values.MeaningChange{{Path: "image.repository", Old: "a", New: "b"}},
	}

	res, err := askNotes(context.Background(), d, cs, f, r, options{threshold: 0.5})
	if err != nil || res.Skipped != "" {
		t.Fatalf("askNotes: %v %q", err, res.Skipped)
	}
	want := map[string][]string{
		"Add support for multiple backupstoragelocations": {"configuration.backupStorageLocation.bucket", "configuration.provider"},
		"Refactor the image settings":                     {"image.repository"},
	}
	for entry, keys := range want {
		if !slices.Equal(ev.asked[entry], keys) {
			t.Errorf("asked about %q: %v, want %v", entry, ev.asked[entry], keys)
		}
	}
}
