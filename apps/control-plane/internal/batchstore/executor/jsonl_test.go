package executor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
)

// fakeStorage records uploads and downloads in-memory for tests.
type fakeStorage struct {
	mu        sync.Mutex
	uploads   map[string][]byte
	downloads map[string][]byte
}

func newFakeStorage() *fakeStorage {
	return &fakeStorage{uploads: map[string][]byte{}, downloads: map[string][]byte{}}
}

func (f *fakeStorage) Upload(ctx context.Context, bucket, key string, body io.Reader, size int64, contentType string) error {
	data, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.uploads[bucket+"/"+key] = data
	return nil
}

func (f *fakeStorage) Download(ctx context.Context, bucket, key string) (io.ReadCloser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	data, ok := f.downloads[bucket+"/"+key]
	if !ok {
		return nil, fmt.Errorf("not found: %s/%s", bucket, key)
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func buildFixture(n int) []byte {
	var buf bytes.Buffer
	for i := 0; i < n; i++ {
		body := fmt.Sprintf(`{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hello %d"}]}`, i)
		line := fmt.Sprintf(`{"custom_id":"req-%d","method":"POST","url":"/v1/chat/completions","body":%s}`, i, body)
		buf.WriteString(line)
		buf.WriteString("\n")
	}
	return buf.Bytes()
}

// Test 1: ScanLines streams a 1000-line JSONL fixture without buffering whole
// file in memory; each yielded InputLine has CustomID, Method=POST,
// URL=/v1/chat/completions, Body raw bytes preserved.
func TestScanLines_StreamsLargeFixture(t *testing.T) {
	fixture := buildFixture(1000)
	ch := make(chan ScanResult, 16)
	go ScanLines(context.Background(), bytes.NewReader(fixture), ch)

	count := 0
	for r := range ch {
		if r.Err != nil {
			t.Fatalf("unexpected error at line %d: %v", count, r.Err)
		}
		if r.Line == nil {
			t.Fatalf("nil line at index %d", count)
		}
		want := fmt.Sprintf("req-%d", count)
		if r.Line.CustomID != want {
			t.Fatalf("line %d: custom_id=%q want %q", count, r.Line.CustomID, want)
		}
		if r.Line.Method != "POST" {
			t.Fatalf("line %d: method=%q want POST", count, r.Line.Method)
		}
		if r.Line.URL != "/v1/chat/completions" {
			t.Fatalf("line %d: url=%q", count, r.Line.URL)
		}
		// Body raw preserved — must contain the exact "hello N" payload.
		if !bytes.Contains(r.Line.Body, []byte(fmt.Sprintf(`hello %d`, count))) {
			t.Fatalf("line %d: body did not preserve payload", count)
		}
		count++
	}
	if count != 1000 {
		t.Fatalf("scanned %d lines, want 1000", count)
	}
}

// Test 2: malformed line is yielded as error variant — caller decides to write
// to errors.jsonl, not crash the scan.
func TestScanLines_MalformedLineYieldsError(t *testing.T) {
	input := []byte(strings.Join([]string{
		`{"custom_id":"a","method":"POST","url":"/v1/chat/completions","body":{}}`,
		`this is not json`,
		`{"custom_id":"b","method":"POST","url":"/v1/chat/completions","body":{}}`,
	}, "\n") + "\n")

	ch := make(chan ScanResult, 8)
	go ScanLines(context.Background(), bytes.NewReader(input), ch)

	var ok []*InputLine
	var bad []ScanResult
	for r := range ch {
		if r.Err != nil {
			if !IsInvalidJSON(r.Err) {
				t.Fatalf("expected invalid_json sentinel, got %v", r.Err)
			}
			bad = append(bad, r)
			continue
		}
		ok = append(ok, r.Line)
	}
	if len(ok) != 2 || len(bad) != 1 {
		t.Fatalf("ok=%d bad=%d, want 2/1", len(ok), len(bad))
	}
	if ok[0].CustomID != "a" || ok[1].CustomID != "b" {
		t.Fatalf("unexpected ok ids: %s %s", ok[0].CustomID, ok[1].CustomID)
	}
	if !bytes.Contains(bad[0].RawLine, []byte("this is not json")) {
		t.Fatalf("malformed raw bytes not preserved")
	}
}

// Test 3: output writer + error writer flush concurrently safely (mutex around
// append) — verified via -race with 32 goroutines writing.
func TestJSONLWriter_ConcurrentAppend(t *testing.T) {
	out := &JSONLWriter{}
	errs := &JSONLWriter{}
	const goroutines = 32
	const perGoroutine = 50

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				if (id+i)%2 == 0 {
					if _, err := out.Append(map[string]any{"g": id, "i": i}); err != nil {
						t.Errorf("append: %v", err)
						return
					}
				} else {
					if _, err := errs.Append(map[string]any{"g": id, "i": i}); err != nil {
						t.Errorf("append err: %v", err)
						return
					}
				}
			}
		}(g)
	}
	wg.Wait()

	totalExpected := goroutines * perGoroutine
	if got := out.Count() + errs.Count(); got != totalExpected {
		t.Fatalf("count=%d want %d", got, totalExpected)
	}

	// Every line in the buffer must be a valid JSON object; the count of
	// newline-terminated lines must equal Count().
	for name, w := range map[string]*JSONLWriter{"out": out, "errs": errs} {
		body := w.Bytes()
		lines := bytes.Split(bytes.TrimRight(body, "\n"), []byte("\n"))
		if len(lines) != w.Count() {
			t.Fatalf("%s: parsed %d lines, count %d", name, len(lines), w.Count())
		}
		for li, ln := range lines {
			var v map[string]any
			if err := json.Unmarshal(ln, &v); err != nil {
				t.Fatalf("%s: line %d: %v (%q)", name, li, err, ln)
			}
		}
	}
}

// Test 3b: Finalize with skipEmpty=true on empty writer skips upload.
func TestJSONLWriter_Finalize_SkipEmpty(t *testing.T) {
	w := &JSONLWriter{}
	store := newFakeStorage()
	uploaded, err := w.Finalize(context.Background(), store, "hive-files", "batches/abc/errors.jsonl", true)
	if err != nil {
		t.Fatalf("finalize: %v", err)
	}
	if uploaded {
		t.Fatalf("expected empty writer to skip upload")
	}
	if len(store.uploads) != 0 {
		t.Fatalf("unexpected upload: %v", store.uploads)
	}
}

// Test 3c: Finalize writes content with trailing newline per line.
func TestJSONLWriter_Finalize_UploadsContent(t *testing.T) {
	w := &JSONLWriter{}
	if _, err := w.Append(map[string]string{"k": "v1"}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if _, err := w.Append(map[string]string{"k": "v2"}); err != nil {
		t.Fatalf("append: %v", err)
	}
	store := newFakeStorage()
	uploaded, err := w.Finalize(context.Background(), store, "hive-files", "batches/abc/output.jsonl", true)
	if err != nil {
		t.Fatalf("finalize: %v", err)
	}
	if !uploaded {
		t.Fatalf("expected upload")
	}
	got := store.uploads["hive-files/batches/abc/output.jsonl"]
	if !bytes.HasSuffix(got, []byte("\n")) {
		t.Fatalf("expected trailing newline, got %q", got)
	}
	lines := bytes.Split(bytes.TrimRight(got, "\n"), []byte("\n"))
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}
}
