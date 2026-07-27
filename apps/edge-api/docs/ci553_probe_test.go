package docs

import "testing"

// Throwaway deliberately failing test for the issue 553 merge-gate probe.
// Case: code plus docs, where the code is broken. The point is to prove the
// real suite reports a failure on the required check and that nothing can
// report success under the same name instead. This branch is never merged.
func TestCI553ProbeDeliberateFailure(t *testing.T) {
	t.Fatal("deliberate failure: issue 553 merge-gate probe")
}
