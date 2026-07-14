// Tests for log loading: readers, files, recursive directory scans,
// stdin, and deterministic ordering.
package usage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestParseReaderSkipsBlankLines(t *testing.T) {
	in := "{\"model\":\"a\"}\n\n   \n{\"model\":\"b\"}\n"
	recs, err := ParseReader(strings.NewReader(in), "s", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 || recs[0].Model != "a" || recs[1].Model != "b" {
		t.Fatalf("recs = %+v", recs)
	}
}

func TestParseReaderErrorCarriesRealLineNumber(t *testing.T) {
	// The bad line is line 4 counting blanks — the error must say 4,
	// not "2nd record", or users grep the wrong line.
	in := "{\"model\":\"a\"}\n\n\nnot-json\n"
	_, err := ParseReader(strings.NewReader(in), "run.jsonl", Options{})
	if err == nil || !strings.Contains(err.Error(), "run.jsonl:4") {
		t.Fatalf("err = %v, want run.jsonl:4", err)
	}
}

func TestLoadPathsSingleFile(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "usage.jsonl")
	write(t, f, `{"model":"a","suite":"unit"}`+"\n")
	recs, sources, err := LoadPaths([]string{f}, nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if sources != 1 || len(recs) != 1 {
		t.Fatalf("sources=%d recs=%d", sources, len(recs))
	}
	if recs[0].File != f || recs[0].Line != 1 {
		t.Fatalf("provenance = %s:%d", recs[0].File, recs[0].Line)
	}
}

func TestLoadPathsDirectoryRecursiveSorted(t *testing.T) {
	dir := t.TempDir()
	// Written out of order on purpose; loading must sort by path.
	write(t, filepath.Join(dir, "b", "later.jsonl"), `{"model":"b"}`+"\n")
	write(t, filepath.Join(dir, "a.ndjson"), `{"model":"a"}`+"\n")
	write(t, filepath.Join(dir, "notes.txt"), "not a log\n")
	recs, sources, err := LoadPaths([]string{dir}, nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if sources != 2 {
		t.Fatalf("sources = %d, want 2 (txt skipped)", sources)
	}
	if recs[0].Model != "a" || recs[1].Model != "b" {
		t.Fatalf("order = %s, %s; want a, b", recs[0].Model, recs[1].Model)
	}
}

func TestLoadPathsEmptyDirectoryIsAnError(t *testing.T) {
	dir := t.TempDir()
	_, _, err := LoadPaths([]string{dir}, nil, Options{})
	if err == nil || !strings.Contains(err.Error(), "no *.jsonl") {
		t.Fatalf("err = %v", err)
	}
}

func TestLoadPathsStdin(t *testing.T) {
	recs, sources, err := LoadPaths([]string{"-"}, strings.NewReader(`{"model":"s"}`+"\n"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if sources != 1 || len(recs) != 1 || recs[0].File != "stdin" {
		t.Fatalf("recs = %+v sources = %d", recs, sources)
	}
}

func TestLoadPathsErrorCases(t *testing.T) {
	if _, _, err := LoadPaths([]string{"-", "-"}, strings.NewReader(""), Options{}); err == nil || !strings.Contains(err.Error(), "more than once") {
		t.Fatalf("double stdin: err = %v", err)
	}
	if _, _, err := LoadPaths(nil, nil, Options{}); err == nil {
		t.Fatal("empty path list: want error")
	}
	if _, _, err := LoadPaths([]string{filepath.Join(t.TempDir(), "nope.jsonl")}, nil, Options{}); err == nil {
		t.Fatal("missing file: want error")
	}
}
