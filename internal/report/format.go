package report

import (
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
)

type format struct {
	report func(io.Writer, Report) error
	batch  func(io.Writer, Batch) error
}

var formats = map[string]format{
	"json":     {WriteJSON, writeBatchJSON},
	"markdown": {WriteMarkdown, writeBatchMarkdown},
	"text":     {WriteText, writeBatchText},
}

func Formats() []string {
	return slices.Sorted(maps.Keys(formats))
}

func CheckFormat(name string) error {
	if _, ok := formats[name]; !ok {
		return fmt.Errorf("unknown output format %q, want one of %s", name, strings.Join(Formats(), ", "))
	}
	return nil
}

func Write(w io.Writer, name string, r Report) error {
	if err := CheckFormat(name); err != nil {
		return err
	}
	return formats[name].report(w, r)
}

func WriteBatch(w io.Writer, name string, b Batch) error {
	if err := CheckFormat(name); err != nil {
		return err
	}
	return formats[name].batch(w, b)
}
