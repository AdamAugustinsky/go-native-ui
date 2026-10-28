// Command testtimes reads the stream of `go test -json` on its standard
// input, passes the output through as plain text the way `go test` prints it
// without -v (a passing test's output is dropped, a failing or killed one's is
// shown, and so is everything outside a test) and ends with a table of the
// slowest packages and tests, which it also appends to the file named by
// $GITHUB_STEP_SUMMARY. The raw stream is kept with -raw, for an artifact.
//
//	go test -json ./... | go run ./tools/testtimes -raw test.json
//
// It exits 0 whatever the tests did: the pipeline's status is go test's
// (bash runs with pipefail). It is how the CI jobs' time is attributed to
// packages and tests (docs/QUARANTINE.md aside, the jobs' limits are in
// .github/workflows/ci.yml).
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

// event is one line of test2json's output (and cmd/go's build events).
type event struct {
	Action     string
	Package    string
	ImportPath string
	Test       string
	Elapsed    float64
	Output     string
}

type timing struct {
	name    string
	seconds float64
	result  string
}

func main() {
	raw := flag.String("raw", "", "also write the JSON stream to this file")
	title := flag.String("title", "", "a heading for the table (the job's name)")
	top := flag.Int("n", 15, "how many packages and tests to list")
	flag.Parse()

	var rawOut io.Writer = io.Discard
	if *raw != "" {
		f, err := os.Create(*raw)
		if err != nil {
			fmt.Fprintln(os.Stderr, "testtimes:", err)
			os.Exit(2)
		}
		defer f.Close()
		rawOut = f
	}

	var pkgs, tests []timing
	held := map[string][]string{} // a test's output, shown if it fails
	in := bufio.NewReaderSize(os.Stdin, 1<<20)
	out := bufio.NewWriter(os.Stdout)
	for {
		line, err := in.ReadBytes('\n')
		if len(line) > 0 {
			rawOut.Write(line)
			var e event
			if json.Unmarshal(line, &e) != nil {
				out.Write(line) // not an event: shown as it came
			} else {
				key := e.Package + "\x00" + e.Test
				switch {
				case e.Action == "output" && e.Test != "":
					held[key] = append(held[key], e.Output)
				case e.Output != "":
					out.WriteString(e.Output)
				}
				switch e.Action {
				case "pass", "fail", "skip":
					if e.Test != "" {
						if e.Action == "fail" {
							out.WriteString(strings.Join(held[key], ""))
						}
						delete(held, key)
					}
					switch {
					case e.Test == "" && e.Package != "":
						pkgs = append(pkgs, timing{e.Package, e.Elapsed, e.Action})
					case e.Test != "" && !strings.Contains(e.Test, "/"): // (a subtest is part of its test)
						tests = append(tests, timing{e.Package + "." + e.Test, e.Elapsed, e.Action})
					}
				}
			}
		}
		if err != nil {
			break
		}
	}
	// A test with no result when the stream ended was killed (a timeout, a crash):
	// what it printed is what explains it.
	keys := make([]string, 0, len(held))
	for k := range held {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		out.WriteString(strings.Join(held[k], ""))
	}
	out.Flush()

	var b strings.Builder
	if *title != "" {
		fmt.Fprintf(&b, "### Slowest in %s\n\n", *title)
	}
	table(&b, "package", pkgs, *top)
	table(&b, "test", tests, *top)
	fmt.Print("\n", b.String())
	if p := os.Getenv("GITHUB_STEP_SUMMARY"); p != "" {
		if f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644); err == nil {
			f.WriteString("\n" + b.String())
			f.Close()
		}
	}
}

// table writes the n slowest of ts as a Markdown table, and their sum.
func table(b *strings.Builder, kind string, ts []timing, n int) {
	sort.Slice(ts, func(i, j int) bool { return ts[i].seconds > ts[j].seconds })
	var sum float64
	for _, t := range ts {
		sum += t.seconds
	}
	fmt.Fprintf(b, "| %s | seconds | result |\n|---|---:|---|\n", kind)
	for _, t := range ts[:min(n, len(ts))] {
		fmt.Fprintf(b, "| %s | %.1f | %s |\n", strings.TrimPrefix(t.name, "gonativeui/"), t.seconds, t.result)
	}
	fmt.Fprintf(b, "| all %d (sum) | %.0f | |\n\n", len(ts), sum)
}
