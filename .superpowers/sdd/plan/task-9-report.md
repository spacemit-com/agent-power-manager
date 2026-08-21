# Feature 020 Task 9 Report: Select Providers in the Composition Root

## RevisionRound2

### Status

Task 9 review findings are resolved in the isolated worktree. Direct Remote construction no longer includes caller-supplied base URL data or parser errors in configuration failures. Local root validation now has platform-specific filesystem inspection: non-Windows builds retain component-wise `Lstat`, while Windows opens every component with native no-follow reparse-point handles and rejects all reparse attributes and non-directories.

Planned commit message: `fix(cloud): harden provider construction`

### Root causes and changes

1. `remote.New` joined the raw `url.Parse` error and three validation branches interpolated caller-supplied scheme, path, or host values. Parse and validation failures now return stable, input-independent categories that continue to wrap `cloud.ErrProviderConfig`; validation rules were not relaxed.
2. The composition root used `os.Lstat` on every platform. On Windows that was insufficient as the native no-follow contract for junctions, cloud placeholders, ProjFS nodes, and other reparse points. The selected clean absolute path check remains common; physical validation is build-tagged. Windows opens each root-to-leaf component with `FILE_FLAG_OPEN_REPARSE_POINT | FILE_FLAG_BACKUP_SEMANTICS`, reads `ByHandleFileInformation`, rejects `FILE_ATTRIBUTE_REPARSE_POINT`, and requires `FILE_ATTRIBUTE_DIRECTORY`.
3. The Local symlink regression created links directly from an untagged test. It now calls a build-tagged fixture helper. Windows skips only `windows.ERROR_PRIVILEGE_NOT_HELD`; all other setup failures remain fatal. Other platforms never skip.
4. The component walker accepts an instance-local inspector function rather than package-global mutable hooks. Linux-runnable tests inject attributes to prove root-to-leaf order and rejection of ancestor/terminal reparse points, non-directories, and inspector errors without Windows privileges or shared state.

### TDD evidence

#### Remote constructor redaction RED

```text
go test -count=1 ./internal/cloud/remote -run '^TestNewRedactsInvalidBaseURLInput$'
```

Exited 1. The malformed escape case exposed the complete base URL and `%zz`; the non-root path exposed `path-secret`; the unsupported scheme exposed `scheme-secret`; the non-loopback host exposed `host-secret`; and the malformed host exposed its caller input through `url.Parse`.

#### Remote constructor redaction GREEN

```text
go test -count=1 ./internal/cloud/remote -run '^TestNew(RedactsInvalidBaseURLInput|ValidatesBaseURLAndHTTPClient)$'
ok  apm/internal/cloud/remote
```

The direct API matrix covers malformed escape, path, userinfo, query, fragment, unsupported scheme, non-loopback host, and malformed host. Every failure remains classifiable with `cloud.ErrProviderConfig`, retains safe Remote base URL context, and excludes the raw value and marked secret substrings.

#### Platform split RED

```text
go test -count=1 ./cmd/apm -run '^TestValidatePhysicalLocalRootWithInspector'
```

Exited 1 because `localRootComponentInspection` and `validatePhysicalLocalRootWithInspector` did not exist. This was the intended missing boundary for platform-native inspection.

#### Platform split GREEN

```text
go test -count=1 ./cmd/apm -run '^Test(ValidatePhysicalLocalRootWithInspector|NewCloudClientRejectsNonPhysicalLocalRoot|NewCloudClientAcceptsPhysicalLocalRoot)'
ok  apm/cmd/apm
```

### Verification

The following commands exited 0:

```text
go test -count=1 ./internal/cloud/remote ./cmd/apm
go test -count=1 ./cmd/apm ./internal/cli ./internal/lifecycle
go test -race -count=1 ./internal/cloud/remote ./cmd/apm ./internal/lifecycle
go test -count=1 ./...
go vet ./...
go build -o /dev/null ./cmd/apm
GOOS=windows GOARCH=amd64 go test -c -o /dev/null cmd/apm/cloud.go cmd/apm/cloud_local_root.go cmd/apm/cloud_local_root_windows.go cmd/apm/cloud_test.go cmd/apm/cloud_local_root_test.go cmd/apm/cloud_symlink_windows_test.go
test -z "$(gofmt -l cmd internal)"
git diff --check
```

The staged scope contains only this report plus Task 9 files under `cmd/apm` and `internal/cloud/remote`; `docs/Changelog.md` is unchanged.

### Windows support caveat

The platform-specific composition-root production and test files cross-compile for Windows. A full `GOOS=windows GOARCH=amd64 go test -c ./cmd/apm` remains blocked outside Task 9 scope by pre-existing Unix process-control calls in `internal/adapter/cli_runner.go` (`syscall.SysProcAttr.Setpgid` and `syscall.Kill`). No Windows runner was available, so native symlink execution was not performed here. On a Windows runner the existing Local-root regression exercises native reparse inspection and skips only when symlink creation specifically returns `ERROR_PRIVILEGE_NOT_HELD`.

No Changelog entry or unrelated documentation was changed.
