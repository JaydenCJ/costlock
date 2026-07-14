// Log loading: files, directories (recursive *.jsonl / *.ndjson), and
// stdin via "-", with deterministic ordering.
package usage

import (
	"bufio"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// maxLineBytes bounds a single JSONL line; usage events are small, so
// 4 MiB is generous while still catching runaway inputs.
const maxLineBytes = 4 << 20

// ParseReader parses a stream of JSONL usage records. Blank lines are
// skipped; anything else must be a JSON object. name labels errors.
func ParseReader(r io.Reader, name string, opts Options) ([]Record, error) {
	var recs []Record
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), maxLineBytes)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		rec, err := ParseLine([]byte(line), name, lineNo, opts)
		if err != nil {
			return nil, err
		}
		recs = append(recs, rec)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("%s: %v", name, err)
	}
	return recs, nil
}

// LoadPaths loads records from a list of paths. Each path may be a
// JSONL file, a directory (searched recursively for *.jsonl and
// *.ndjson, sorted), or "-" for stdin. It returns the records and the
// number of log sources read.
func LoadPaths(paths []string, stdin io.Reader, opts Options) ([]Record, int, error) {
	if len(paths) == 0 {
		return nil, 0, fmt.Errorf("no usage logs given (pass files, directories, or -)")
	}
	var files []string
	stdinUsed := false
	for _, p := range paths {
		if p == "-" {
			if stdinUsed {
				return nil, 0, fmt.Errorf("stdin (-) given more than once")
			}
			stdinUsed = true
			continue
		}
		info, err := os.Stat(p)
		if err != nil {
			return nil, 0, err
		}
		if !info.IsDir() {
			files = append(files, p)
			continue
		}
		found, err := findLogs(p)
		if err != nil {
			return nil, 0, err
		}
		if len(found) == 0 {
			return nil, 0, fmt.Errorf("%s: no *.jsonl or *.ndjson files found", p)
		}
		files = append(files, found...)
	}

	var recs []Record
	sources := 0
	for _, f := range files {
		fh, err := os.Open(f)
		if err != nil {
			return nil, 0, err
		}
		got, err := ParseReader(fh, f, opts)
		fh.Close()
		if err != nil {
			return nil, 0, err
		}
		recs = append(recs, got...)
		sources++
	}
	if stdinUsed {
		got, err := ParseReader(stdin, "stdin", opts)
		if err != nil {
			return nil, 0, err
		}
		recs = append(recs, got...)
		sources++
	}
	return recs, sources, nil
}

// findLogs collects *.jsonl / *.ndjson under dir, sorted for
// deterministic ordering across filesystems.
func findLogs(dir string) ([]string, error) {
	var found []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		switch strings.ToLower(filepath.Ext(path)) {
		case ".jsonl", ".ndjson":
			found = append(found, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(found)
	return found, nil
}
