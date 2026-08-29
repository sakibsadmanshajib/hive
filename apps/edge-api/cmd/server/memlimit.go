package main

import (
	"fmt"
	"math"
	"os"
	"runtime/debug"
	"strconv"
	"strings"
)

// cgroupMemoryMaxPath is where cgroup v2 reports the container's hard memory
// limit. Under docker compose's mem_limit this holds that value in bytes, or
// the literal "max" when the container is unlimited.
const cgroupMemoryMaxPath = "/sys/fs/cgroup/memory.max"

// memoryLimitWarning compares the Go runtime's soft memory limit (GOMEMLIMIT)
// against the container's hard cgroup limit and returns a warning when the
// pair cannot do its job, or "" when it can.
//
// The two are set in different places and nothing keeps them consistent:
// GOMEMLIMIT is an environment variable an operator can override, the cgroup
// limit comes from the container runtime. GOMEMLIMIT is a SOFT limit, so it
// cannot prevent an OOM kill by itself; all it does is make the collector
// aggressive before the kernel gets involved. Set at or above the hard limit
// it never gets the chance, and the first symptom is the container being
// killed with every in-flight SSE stream on it. Set with too little headroom
// it also fails, because the runtime does not count thread stacks, mmapped
// runtime metadata or allocator overhead against the soft limit.
//
// Warning rather than fatal on purpose: refusing to boot on a memory-sizing
// heuristic would turn a misconfiguration into an outage, which is the very
// thing this pair exists to avoid.
func memoryLimitWarning(soft, hard int64) string {
	if hard <= 0 || soft <= 0 || soft == math.MaxInt64 {
		// No cgroup limit readable, or GOMEMLIMIT unset (the runtime reports
		// math.MaxInt64). Nothing to compare, and nothing to warn about.
		return ""
	}
	if soft >= hard {
		return fmt.Sprintf("WARNING: GOMEMLIMIT=%d is at or above the container memory limit of %d; "+
			"the Go runtime will not start collecting harder until the kernel has already OOM-killed this container", soft, hard)
	}
	if headroom := hard - soft; headroom < hard/10 {
		return fmt.Sprintf("WARNING: GOMEMLIMIT=%d leaves only %d bytes under the container memory limit of %d; "+
			"the runtime does not count thread stacks and allocator overhead against GOMEMLIMIT, so this container can still be OOM-killed", soft, headroom, hard)
	}
	return ""
}

// parseCgroupMemoryMax parses the contents of cgroup v2's memory.max. It
// returns 0 for "max" (unlimited) and for anything unparseable, which callers
// treat as "no limit to compare against" rather than as an error.
func parseCgroupMemoryMax(contents string) int64 {
	v, err := strconv.ParseInt(strings.TrimSpace(contents), 10, 64)
	if err != nil {
		return 0
	}
	return v
}

// logMemoryLimit emits the warning from memoryLimitWarning, if any, at boot.
// Silent when the pair is sane, so the absence of a line here is the normal
// case and its presence is the signal.
func logMemoryLimit(logf func(string, ...any)) {
	raw, err := os.ReadFile(cgroupMemoryMaxPath)
	if err != nil {
		return
	}
	// SetMemoryLimit(-1) reports the current soft limit without changing it.
	if msg := memoryLimitWarning(debug.SetMemoryLimit(-1), parseCgroupMemoryMax(string(raw))); msg != "" {
		logf("%s", msg)
	}
}
