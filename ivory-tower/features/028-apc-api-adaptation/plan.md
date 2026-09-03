# APC-API v1 接口适配与 APM 验收实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将 Remote Client 与 APM CLI 完整对齐 APC-API.md v1，并以自动化测试和测试环境验收证明七类接口的行为。

**Architecture:** 保留 `cloud.Client` 与现有 Remote/Local provider 边界，只在 `internal/cloud/remote` 调整 APC wire 适配：Submission 三个接口仅发送 `X-API-Key`，metadata 补齐 `schema_version=1`，公开目录/content 保持匿名和严格校验。CLI 只改变 Remote 默认根地址；不引入 Bearer 回退、双协议发送或服务端兼容分支。

**Tech Stack:** Go、`net/http`、`mime/multipart`、现有 `jsonstrict`、`httptest`、curl、APC 测试环境。

**Spec:** `ivory-tower/features/028-apc-api-adaptation/spec.md`

## Global Constraints

- APC-API.md 是客户端唯一权威协议；Submission 只发送 `X-API-Key`，不得发送 `Authorization`。
- `APC_API_KEY` 只用于 Submission；匿名目录和 content 请求不得携带任何凭据。
- metadata 必须包含 `type`、`name`、`version`、`schema_version: 1`、`description`、`message`；非 MCP 必须有 `content_md5`。
- Skill/Plugin/Agent content type 为 `application/gzip`，MCP 为 `application/json`；Catalog 返回其它类型时 fail closed。
- 不实现 Bearer 回退、双发认证、自动重试或生产真实提交；服务端偏差必须作为验收限制报告。
- 重要接口新增或修改需添加简短规范中文注释；不把真实 submit key 写入源码、测试 fixture、日志或提交。
- 行为完成后按一次用户请求更新 `docs/Changelog.md`；文档/测试-only 中间提交不单独记录。

## File Map

- Modify `internal/cloud/remote/submission.go`: APC Submission metadata DTO、认证头和 wire 校验。
- Modify `internal/cloud/remote/submission_test.go`: X-API-Key、schema_version 和错误契约测试。
- Modify `internal/cloud/remote/client.go`/`catalog.go`/`content.go` only when tests expose a documented protocol gap; preserve anonymous redirect and integrity boundaries.
- Modify `cmd/apm/cloud.go` and `cmd/apm/cloud_test.go`: test-environment default URL and composition assertions.
- Modify `BUILD.md`, `ivory-tower/project-overview.md`, `ivory-tower/features/021-submission-validate/spec.md`, `ivory-tower/features/023-submission-resource-metadata-query/spec.md`, and relevant ADR references.
- Create `ivory-tower/adr/ADR-025-apc-api-client-contract.md`.
- Modify `docs/Changelog.md` only after behavior and verification are complete.

### Task 1: Freeze current behavior and add APC wire regression tests

**Files:**
- Test: `internal/cloud/remote/submission_test.go`
- Test: `cmd/apm/cloud_test.go`

**Interfaces:**
- Consumes existing `remote.Client.Submit/GetSubmission/ListSubmissions` and `newCloudClient`.
- Produces failing tests that define the new `X-API-Key`-only and default URL contract for later tasks.

- [ ] **Step 1: Add failing request-header assertions.** Update the submission fake handlers so `r.Header.Get("X-API-Key") == "official-key"` and `r.Header.Values("Authorization")` is empty for POST, GET detail, and GET list. Keep `Accept: application/json` and existing query assertions.
- [ ] **Step 2: Add failing metadata assertion.** In `readSubmissionParts` callers, assert `metadata["schema_version"]` is the JSON number `1` for all resource types and continue asserting `message`, identity, and MD5.
- [ ] **Step 3: Add failing default URL assertion.** In `cmd/apm/cloud_test.go`, clear `APC_BASE_URL`, construct the Remote provider through the existing test seam, and assert its request URL starts with `https://developer.bianbu.xyz/` (use a transport recorder; do not make a network call).
- [ ] **Step 4: Run the focused tests to confirm failure.**

  Run: `go test ./internal/cloud/remote ./cmd/apm -run 'Test(Submit|GetSubmission|ListSubmissions)|Test.*Default.*URL'`

  Expected: FAIL because current code sends `Authorization: Bearer` and omits `schema_version`, and the default URL is loopback.
- [ ] **Step 5: Commit the red tests.**

  ```bash
  git add internal/cloud/remote/submission_test.go cmd/apm/cloud_test.go
  git commit -m "test(apc): define X-API-Key wire contract"
  ```

### Task 2: Adapt Submission authentication and metadata

**Files:**
- Modify: `internal/cloud/remote/submission.go`
- Test: `internal/cloud/remote/submission_test.go`

**Interfaces:**
- Consumes `domain.SubmissionRequest` and existing response/error decoders.
- Produces unchanged `cloud.Submission` methods whose authenticated requests use only `X-API-Key` and whose metadata includes `schema_version`.

- [ ] **Step 1: Extend the metadata DTO.** Add `SchemaVersion int ` + "`json:\"schema_version\"`" + ` to `submissionMetadataDTO`; set it to `1` in `Submit` and retain `message` trimming and MD5 behavior.
- [ ] **Step 2: Replace authenticated request headers.** In `Submit` and `getSubmissionJSON`, replace `Authorization: Bearer ` with `X-API-Key`; do not set either header when `apiKey` is empty because existing preflight returns `cloud.ErrAuth`.
- [ ] **Step 3: Add Chinese contract comments.** Document that the header is the APC v1 Submission credential and that catalog/content requests remain anonymous; do not log the value.
- [ ] **Step 4: Run focused tests.**

  Run: `go test ./internal/cloud/remote -run 'Test(Submit|GetSubmission|ListSubmissions|SubmissionQuery)'`

  Expected: PASS, including accepted/replayed/error mapping and no credential leakage tests.
- [ ] **Step 5: Commit the protocol adapter.**

  ```bash
  git add internal/cloud/remote/submission.go internal/cloud/remote/submission_test.go
  git commit -m "feat(apc): use X-API-Key and schema version metadata"
  ```

### Task 3: Verify catalog/content mappings remain strict

**Files:**
- Test: `internal/cloud/remote/catalog_test.go`
- Test: `internal/cloud/remote/content_test.go`
- Test: `internal/cloud/contract_test.go`
- Modify only if required: `internal/cloud/remote/catalog.go`, `internal/cloud/remote/content.go`, `internal/domain/catalog.go`

**Interfaces:**
- Consumes existing CatalogSource and resource download boundaries.
- Produces tests proving all public APC endpoints are anonymous, redirects are same-origin constrained, and type/media mappings reject the test environment's observed invalid MCP catalog entry.

- [ ] **Step 1: Add explicit four-type mapping tests.** Assert Skill/Plugin/Agent require `application/gzip`; MCP requires `application/json`; assert generated endpoint path escapes name/version safely and sends no `X-API-Key` or `Authorization` even when the client has a key.
- [ ] **Step 2: Add a catalog negative fixture.** Feed an MCP item with `content_type: application/gzip` and assert catalog decoding returns `cloud.ErrInvalidResponse`; do not loosen `domain.CatalogEntry.Validate`.
- [ ] **Step 3: Add live-shaped redirect/content headers.** Cover `302` catalog redirect, CDN final response, exact `Content-Length`, MD5, and 404/410 mapping using fake handlers.
- [ ] **Step 4: Run the focused suite.**

  Run: `go test ./internal/cloud/remote ./internal/cloud -run 'Test(Catalog|Download|Remote|.*MCP)'`

  Expected: PASS with no production code change unless a documented gap is demonstrated.
- [ ] **Step 5: Commit strict public endpoint coverage.**

  ```bash
  git add internal/cloud/remote internal/cloud/contract_test.go internal/domain/catalog.go
  git commit -m "test(apc): verify anonymous catalog and content contracts"
  ```

### Task 4: Change the Remote default endpoint

**Files:**
- Modify: `cmd/apm/cloud.go`
- Test: `cmd/apm/cloud_test.go`
- Modify: `BUILD.md` only in the documentation task below unless a test comment must change.

**Interfaces:**
- Consumes `APC_BASE_URL` override and existing base URL validation.
- Produces default `https://developer.bianbu.xyz` while preserving explicit overrides and Local provider behavior.

- [ ] **Step 1: Change `defaultAPCBaseURL`.** Set it to `https://developer.bianbu.xyz`; keep `APC_BASE_URL` precedence and `remote.NewWithAPIKey` validation unchanged.
- [ ] **Step 2: Run configuration tests.**

  Run: `go test ./cmd/apm -run 'Test(NewCloudClient|.*APC.*URL|.*Provider)'`

  Expected: PASS, including invalid URL, HTTP loopback, Local provider, and explicit override cases.
- [ ] **Step 3: Commit the runtime configuration change.**

  ```bash
  git add cmd/apm/cloud.go cmd/apm/cloud_test.go
  git commit -m "feat(apc): default remote endpoint to test environment"
  ```

### Task 5: Update ADR and project documentation

**Files:**
- Create: `ivory-tower/adr/ADR-025-apc-api-client-contract.md`
- Modify: `BUILD.md`
- Modify: `ivory-tower/project-overview.md`
- Modify: `ivory-tower/features/021-submission-validate/spec.md`
- Modify: `ivory-tower/features/023-submission-resource-metadata-query/spec.md`
- Modify: `ivory-tower/features/024-catalog-cache/spec.md` only where the auth/content statement is referenced.

**Interfaces:**
- Consumes Feature 028 and existing ADR-016/018/020/021 decisions.
- Produces a discoverable decision record and consistent operator/client documentation.

- [ ] **Step 1: Write ADR-025.** Record context (APC-API.md vs observed test server), decision (X-API-Key only, no Bearer fallback, schema v1 metadata, test default URL), rejected alternatives, consequences, and the known test-server authentication/content-type discrepancy.
- [ ] **Step 2: Update BUILD and overview.** Replace loopback default examples with `https://developer.bianbu.xyz`, describe `X-API-Key`, and state that anonymous catalog/content calls do not carry credentials; retain `APC_BASE_URL` override.
- [ ] **Step 3: Mark historical docs accurately.** In Feature 021/023, add a local supersession note pointing to ADR-025 rather than rewriting historical accepted decisions; update Feature 024 only if its current auth statement would otherwise contradict the new runtime behavior.
- [ ] **Step 4: Validate documentation links and diff.**

  Run: `git diff --check` and `rg -n 'Authorization: Bearer|http://127.0.0.1:8080' BUILD.md ivory-tower/project-overview.md ivory-tower/features/028-apc-api-adaptation ivory-tower/adr/ADR-025-apc-api-client-contract.md`

  Expected: only explicitly labeled historical/superseded references remain.
- [ ] **Step 5: Commit documentation and ADR.**

  ```bash
  git add BUILD.md ivory-tower/project-overview.md ivory-tower/features/021-submission-validate/spec.md ivory-tower/features/023-submission-resource-metadata-query/spec.md ivory-tower/features/024-catalog-cache/spec.md ivory-tower/adr/ADR-025-apc-api-client-contract.md
  git commit -m "docs(apc): record API v1 client contract decision"
  ```

### Task 6: Run pre-integration test and live APC probes

**Files:**
- Create only if useful: `ivory-tower/features/028-apc-api-adaptation/live-probe.md` (do not store key or response secrets).

**Interfaces:**
- Consumes the built `apm` binary and APC test environment.
- Produces reproducible evidence for public endpoints and the approved key's Submission behavior.

- [ ] **Step 1: Run full local verification before network calls.**

  Run: `go test ./...`, `go test -race ./...`, `go vet ./...`, `go build -o <isolated-temp>/apm ./cmd/apm`, `git diff --check`.

  Expected: all commands exit 0; build output is outside the repository and removed after verification.
- [ ] **Step 2: Probe public APC endpoints with normal TLS first.** Request `/resources/version`, `/resources` with `-L`, and one catalog content URL; capture status, location, content type, length, and checksum without storing credentials.
- [ ] **Step 3: If the container CA chain blocks TLS, repeat only the probe with `curl -k` and record that product TLS remains enabled.** Do not change Go TLS configuration.
- [ ] **Step 4: Probe Submission auth with `X-API-Key` and (for diagnosis only) Bearer.** Redact the key and record status/error code. Do not retry POST automatically.
- [ ] **Step 5: Use a valid repository fixture for one POST only if the server accepts `X-API-Key` and the key is valid.** Record `submission_id/status/status_url`, then query it through APM; otherwise mark POST acceptance blocked by server authentication and do not substitute a different header.
- [ ] **Step 6: Commit probe evidence only if it contains no key, bearer token, private response detail, or unstable timestamps.** Otherwise keep evidence in the final report, not the repository.

### Task 7: End-to-end APM acceptance and changelog

**Files:**
- Modify: `docs/Changelog.md`
- Test only as needed: `internal/cloud/contract_test.go`, `cmd/apm/*_test.go`

**Interfaces:**
- Consumes all prior implementation and verification outputs.
- Produces final acceptance evidence and one changelog entry for this user request.

- [ ] **Step 1: Exercise APM commands against the test endpoint.** Run `APC_BASE_URL=https://developer.bianbu.xyz apm list`/`search` with a clean temporary APM home or equivalent isolated paths; verify catalog sync and resource listing. Do not run install against a resource whose observed catalog media type is invalid.
- [ ] **Step 2: Exercise submission query and submit paths with the approved key only if Task 6 proves server compatibility.** Verify CLI text/json output and quota/error sentinel behavior through fake APC where real service data is unavailable.
- [ ] **Step 3: Re-run final gates.**

  Run: `go test -count=1 ./...`, `go test -race -count=1 ./...`, `go vet ./...`, isolated `go build`, `git diff --check`, and `git status --short`.

  Expected: all gates pass; no key or temporary build artifact remains.
- [ ] **Step 4: Add one reverse-chronological Changelog entry.** Include user request, all behavior commits, protocol changes, test commands, public endpoint results, and the exact safe limitation that real POST is blocked if the server still requires Authorization or rejects the provided key.
- [ ] **Step 5: Commit the final acceptance record.**

  ```bash
  git add docs/Changelog.md
  git commit -m "docs(changelog): record APC API adaptation acceptance"
  ```

## Self-Review Checklist

- [ ] Every APC-API.md endpoint maps to a Remote Client method and a test task.
- [ ] No task sends Bearer or silently broadens MCP content-type acceptance.
- [ ] Metadata schema_version and X-API-Key assertions fail before implementation and pass after it.
- [ ] Default URL, environment override, anonymous credential isolation, and Local provider behavior are covered.
- [ ] Error mapping, strict JSON/timeline validation, 404/410, MD5, and redirect constraints remain covered by existing or new tests.
- [ ] ADR-025 is referenced where runtime/auth decisions are documented.
- [ ] No placeholder/TBD/TODO language remains in this plan.
