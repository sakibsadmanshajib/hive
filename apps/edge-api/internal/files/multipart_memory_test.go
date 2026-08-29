package files

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

// TestMultipartMemoryBudgetBoundsRAMNotUploadSize pins the distinction the
// budget rests on: r.ParseMultipartForm's argument is a buffering threshold,
// not an acceptance limit. A part larger than multipartMemoryBudget is still
// read back whole, and the bytes past the budget live in a temporary file
// rather than in the heap that edge-api's container memory limit governs.
//
// Without this, lowering the budget from MaxFileSize looks like a size
// regression to the next reader, and raising it back looks free.
func TestMultipartMemoryBudgetBoundsRAMNotUploadSize(t *testing.T) {
	// A literal, deliberately, not multipartMemoryBudget plus a delta. Sizing
	// the part off the constant makes the spill assertion below self-fulfilling:
	// raise the budget to 512 MiB and the part grows with it, still spills, and
	// the test stays green over exactly the regression it exists to catch.
	const partSize = 40 << 20
	if partSize <= multipartMemoryBudget {
		t.Fatalf("multipartMemoryBudget is %d, at or above this test's %d byte part: raising the "+
			"in-memory budget is the regression this test exists to catch, so justify the new number "+
			"rather than growing the part to match it", multipartMemoryBudget, partSize)
	}

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("file", "big.bin")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := io.Copy(part, io.LimitReader(neverEndingA{}, int64(partSize))); err != nil {
		t.Fatalf("write part: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/files", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	// parseUploadForm, not r.ParseMultipartForm directly: this has to be the
	// same call the upload handlers make, or raising the budget back at a
	// call site would leave this test green.
	if err := parseUploadForm(req); err != nil {
		t.Fatalf("parseUploadForm: %v", err)
	}
	t.Cleanup(func() { _ = req.MultipartForm.RemoveAll() })

	file, header, err := req.FormFile("file")
	if err != nil {
		t.Fatalf("FormFile: %v", err)
	}
	defer file.Close()
	if header.Size != int64(partSize) {
		t.Fatalf("part size = %d, want %d: the memory budget must not cap what is accepted", header.Size, partSize)
	}
	if _, ok := file.(*os.File); !ok {
		t.Fatalf("a part of %d bytes was served from memory (%T), want a spilled temporary file: "+
			"one upload holding that much live heap is what OOM-kills the container", partSize, file)
	}
	if multipartMemoryBudget >= MaxFileSize {
		t.Fatalf("multipartMemoryBudget (%d) must stay well under MaxFileSize (%d)", multipartMemoryBudget, MaxFileSize)
	}
}

// neverEndingA is an endless source of one repeated byte, so the test body is
// built without allocating a second copy of it.
type neverEndingA struct{}

func (neverEndingA) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'a'
	}
	return len(p), nil
}
