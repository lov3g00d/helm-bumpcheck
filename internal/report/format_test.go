package report

import (
	"bytes"
	"slices"
	"testing"
)

func TestEveryFormatWritesReportsAndBatches(t *testing.T) {
	if got, want := Formats(), []string{"json", "markdown", "text"}; !slices.Equal(got, want) {
		t.Errorf("Formats() = %q, want %q", got, want)
	}
	for _, name := range Formats() {
		t.Run(name, func(t *testing.T) {
			var single, batch bytes.Buffer
			if err := Write(&single, name, veleroReport()); err != nil || single.Len() == 0 {
				t.Errorf("Write: %v, %d bytes", err, single.Len())
			}
			if err := WriteBatch(&batch, name, batchFixture()); err != nil || batch.Len() == 0 {
				t.Errorf("WriteBatch: %v, %d bytes", err, batch.Len())
			}
		})
	}
}

func TestUnknownFormat(t *testing.T) {
	var b bytes.Buffer
	if err := Write(&b, "yaml", veleroReport()); err == nil {
		t.Error("Write accepted an unknown format")
	}
	if err := WriteBatch(&b, "yaml", batchFixture()); err == nil {
		t.Error("WriteBatch accepted an unknown format")
	}
	if b.Len() != 0 {
		t.Errorf("wrote %q for an unknown format", b.String())
	}
}
