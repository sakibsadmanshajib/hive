---
phase: 7
slug: media-file-and-async-api-surface
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-04-09
---

# Phase 7 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | go test |
| **Config file** | none — uses standard Go test toolchain |
| **Quick run command** | `docker compose exec edge-api go test ./apps/edge-api/internal/inference/... -count=1 -short` |
| **Full suite command** | `docker compose exec edge-api go test ./apps/edge-api/... -count=1` |
| **Estimated runtime** | ~30 seconds |

---

## Sampling Rate

- **After every task commit:** Run `docker compose exec edge-api go test ./apps/edge-api/internal/inference/... -count=1 -short`
- **After every plan wave:** Run `docker compose exec edge-api go test ./apps/edge-api/... -count=1`
- **Before `/gsd:verify-work`:** Full suite must be green
- **Max feedback latency:** 30 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 07-01-xx | 01 | 1 | API-05 | unit+integration | `go test ./apps/edge-api/internal/inference/files_test.go` | ❌ W0 | ⬜ pending |
| 07-01-xx | 01 | 1 | API-05 | unit+integration | `go test ./apps/edge-api/internal/inference/uploads_test.go` | ❌ W0 | ⬜ pending |
| 07-01-xx | 01 | 1 | API-05 | unit+integration | `go test ./apps/edge-api/internal/inference/batches_test.go` | ❌ W0 | ⬜ pending |
| 07-02-xx | 02 | 2 | API-06 | unit+integration | `go test ./apps/edge-api/internal/inference/images_test.go` | ❌ W0 | ⬜ pending |
| 07-03-xx | 03 | 2 | API-07 | unit+integration | `go test ./apps/edge-api/internal/inference/audio_test.go` | ❌ W0 | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `apps/edge-api/internal/inference/files_test.go` — stubs for API-05 file operations
- [ ] `apps/edge-api/internal/inference/uploads_test.go` — stubs for API-05 multipart uploads
- [ ] `apps/edge-api/internal/inference/batches_test.go` — stubs for API-05 batch processing
- [ ] `apps/edge-api/internal/inference/images_test.go` — stubs for API-06 image endpoints
- [ ] `apps/edge-api/internal/inference/audio_test.go` — stubs for API-07 audio endpoints

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Supabase Storage S3 upload/download | API-05 | Requires live S3-compatible endpoint | Verify via legacy local object-store emulator console in docker-compose dev environment |
| Binary audio stream playback | API-07 | Audio quality requires human verification | Curl TTS endpoint, play resulting audio file |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 30s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
