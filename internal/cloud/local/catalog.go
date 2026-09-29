package local

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"apm/internal/agentartifact"
	"apm/internal/catalog"
	"apm/internal/cloud"
	"apm/internal/domain"
	"apm/internal/pluginartifact"
	"apm/internal/submission"
	"apm/internal/utils/jsonstrict"
	"go.yaml.in/yaml/v3"
)

const (
	localCatalogMessage     = "local fixture"
	localCatalogPublishedAt = "1970-01-01T00:00:00Z"
	localCatalogFileMode    = fs.FileMode(0o600)
)

var _ cloud.CatalogSource = (*Client)(nil)

// CatalogVersion 每次从受信 fixture 重新生成确定性 catalog，并返回精确字节的 SHA-256 与长度；
// Local 不持有可变目录缓存。
func (c *Client) CatalogVersion(ctx context.Context) (domain.CatalogVersion, error) {
	contents, err := c.buildCatalog(ctx)
	if err != nil {
		return domain.CatalogVersion{}, err
	}
	digest := sha256.Sum256(contents)
	return domain.CatalogVersion{
		SHA256: hex.EncodeToString(digest[:]),
		Size:   int64(len(contents)),
	}, nil
}

// DownloadCatalog 将重新生成的精确 catalog 字节写入调用方给出的 clean absolute 路径；
// destination 必须尚不存在，且仅本地 destination 使用或清理失败才标记 ErrCatalogDestination。
func (c *Client) DownloadCatalog(ctx context.Context, destination string) (resultErr error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateLocalCatalogDestination(destination); err != nil {
		return catalogDestinationError("validate Local catalog destination", err)
	}
	contents, err := c.buildCatalog(ctx)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateLocalCatalogDestination(destination); err != nil {
		return catalogDestinationError("revalidate Local catalog destination", err)
	}

	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, localCatalogFileMode)
	if err != nil {
		return catalogDestinationError("create Local catalog destination", err)
	}
	if c.catalogDestinationAfterOpen != nil {
		c.catalogDestinationAfterOpen(destination, output)
	}
	var created fs.FileInfo
	var statErr error
	if c.catalogDestinationStat != nil {
		created, statErr = c.catalogDestinationStat(output)
	} else {
		created, statErr = output.Stat()
	}
	if statErr != nil {
		closeErr := output.Close()
		// 无 descriptor identity 时不能证明 pathname 仍属于本次调用，必须保守保留。
		return catalogDestinationError("inspect Local catalog destination", errors.Join(statErr, closeErr))
	}
	defer func() {
		if output != nil {
			resultErr = errors.Join(resultErr, catalogDestinationError("close Local catalog destination", output.Close()))
		}
		if resultErr != nil {
			resultErr = errors.Join(resultErr, catalogDestinationError("cleanup Local catalog destination", removeLocalCatalogDestination(destination, created)))
		}
	}()

	if err := writeAllContext(ctx, output, contents); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return catalogDestinationError("write Local catalog destination", err)
	}
	if err := output.Chmod(localCatalogFileMode); err != nil {
		return catalogDestinationError("set Local catalog destination mode", err)
	}
	if err := verifyLocalCatalogDestination(output, destination, created); err != nil {
		return catalogDestinationError("verify Local catalog destination", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := output.Close(); err != nil {
		output = nil
		return catalogDestinationError("close Local catalog destination", err)
	}
	output = nil
	return nil
}

func (c *Client) buildCatalog(ctx context.Context) ([]byte, error) {
	refs, err := c.catalogRefs(ctx)
	if err != nil {
		return nil, fmt.Errorf("enumerate Local catalog: %w", err)
	}
	entries := make([]domain.CatalogEntry, 0, len(refs))
	for _, ref := range refs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		content, err := c.loadCatalogContent(ctx, ref)
		if err != nil {
			return nil, err
		}
		entries = append(entries, content.entry)
	}
	contents, err := catalog.Encode(entries)
	if err != nil {
		return nil, fmt.Errorf("encode Local catalog: %w", err)
	}
	return contents, nil
}

type localCatalogContent struct {
	entry domain.CatalogEntry
	bytes []byte
	tree  *treeSnapshot
}

// localMetadata 是单个 Local fixture 从其权威文件解析出的发布元数据：description 必填，
// 三个 v2 展示字段可选（缺失为空串）。四类资源各自只有一个权威来源——Skill 的
// SKILL.md frontmatter、Plugin 的 apc-plugin.json、MCP 的 MCP.json、Agent 的 agent.yaml；
// 派生的 Agent 落盘清单与内嵌 Skill 不得覆盖它（ADR-038/ADR-040）。
type localMetadata struct {
	description   string
	nameZH        string
	nameEN        string
	descriptionZH string
}

// loadCatalogContent 通过现有 Local 物理快照边界固定一个精确 fixture，再从同一快照
// 构建下载字节与权威 metadata；Task 7 必须复用本入口，不能从目录名重建 metadata。
func (c *Client) loadCatalogContent(ctx context.Context, ref domain.PowerRef) (localCatalogContent, error) {
	if err := ctx.Err(); err != nil {
		return localCatalogContent{}, err
	}
	if _, err := c.catalogSourcePath(ref); err != nil {
		return localCatalogContent{}, err
	}
	var metadata localMetadata
	var contentType string
	var contents []byte
	var tree *treeSnapshot
	var err error
	switch ref.Type {
	case domain.PowerTypeSkill:
		metadata, contents, tree, err = c.loadCatalogSkill(ctx, ref)
		contentType = "application/gzip"
	case domain.PowerTypePlugin:
		metadata, contents, tree, err = c.loadCatalogPlugin(ctx, ref)
		contentType = "application/gzip"
	case domain.PowerTypeMCP:
		metadata, contents, err = c.loadCatalogMCP(ctx, ref)
		contentType = "application/json"
	case domain.PowerTypeAgent:
		metadata, contents, tree, err = c.loadCatalogAgent(ctx, ref)
		contentType = "application/gzip"
	default:
		return localCatalogContent{}, cloud.ErrNotFound
	}
	if err != nil {
		return localCatalogContent{}, err
	}
	if err := ctx.Err(); err != nil {
		return localCatalogContent{}, err
	}
	digest := md5.Sum(contents)
	return localCatalogContent{
		entry: domain.CatalogEntry{
			Ref: ref, Description: metadata.description, Message: localCatalogMessage,
			ContentType: contentType, ContentSize: int64(len(contents)), ContentMD5: hex.EncodeToString(digest[:]),
			PublishedAt: localCatalogPublishedAt, Tags: []string{},
			NameZH: metadata.nameZH, NameEN: metadata.nameEN, DescriptionZH: metadata.descriptionZH,
		},
		bytes: append([]byte(nil), contents...),
		tree:  tree,
	}, nil
}

// loadMatchingCatalogContent 从一次 fresh 物理快照同时取得内容与 metadata，并要求调用方 entry 完全一致。
func (c *Client) loadMatchingCatalogContent(ctx context.Context, entry domain.CatalogEntry, powerType domain.PowerType) (localCatalogContent, error) {
	if err := ctx.Err(); err != nil {
		return localCatalogContent{}, err
	}
	if entry.Ref.Type != powerType {
		return localCatalogContent{}, cloud.ErrNotFound
	}
	if _, err := c.catalogSourcePath(entry.Ref); err != nil {
		return localCatalogContent{}, err
	}
	if err := entry.Validate(); err != nil {
		return localCatalogContent{}, fmt.Errorf("validate Local content catalog entry: %w", errors.Join(cloud.ErrInvalidResponse, err))
	}
	content, err := c.loadCatalogContent(ctx, entry.Ref)
	if err != nil {
		return localCatalogContent{}, err
	}
	if !equalCatalogEntry(content.entry, entry) {
		return localCatalogContent{}, fmt.Errorf("Local %s content no longer matches catalog entry %+v: %w", powerType, entry.Ref, cloud.ErrIntegrity)
	}
	return content, nil
}

func equalCatalogEntry(left, right domain.CatalogEntry) bool {
	return left.Ref == right.Ref && left.Description == right.Description && left.Message == right.Message &&
		left.ContentType == right.ContentType && left.ContentSize == right.ContentSize &&
		left.ContentMD5 == right.ContentMD5 && left.PublishedAt == right.PublishedAt && slices.Equal(left.Tags, right.Tags) &&
		left.NameZH == right.NameZH && left.NameEN == right.NameEN && left.DescriptionZH == right.DescriptionZH
}

func (c *Client) loadCatalogSkill(ctx context.Context, ref domain.PowerRef) (localMetadata, []byte, *treeSnapshot, error) {
	source := filepath.Join(c.resourcesRoot, "skills", ref.Name, ref.Version)
	snapshot, err := c.validateSkillSource(ctx, source)
	if err != nil {
		return localMetadata{}, nil, nil, err
	}
	metadata, err := parseLocalSkillMetadata(snapshot)
	if err != nil {
		return localMetadata{}, nil, nil, unsafeContent("parse Local Skill metadata %s/%s: %v", ref.Name, ref.Version, err)
	}
	if metadata.name != ref.Name || metadata.version != ref.Version {
		return localMetadata{}, nil, nil, unsafeContent("Local Skill metadata %s/%s does not match fixture path %s/%s", metadata.name, metadata.version, ref.Name, ref.Version)
	}
	contents, err := buildLocalCatalogArchive(ctx, snapshot, c.catalogArchiveAfterBodyChunk)
	return metadata.localMetadata, contents, snapshot, err
}

func (c *Client) loadCatalogPlugin(ctx context.Context, ref domain.PowerRef) (localMetadata, []byte, *treeSnapshot, error) {
	source := filepath.Join(c.resourcesRoot, "plugins", ref.Name, ref.Version)
	info, err := os.Lstat(source)
	if errors.Is(err, fs.ErrNotExist) {
		return localMetadata{}, nil, nil, cloud.ErrNotFound
	}
	if err != nil {
		return localMetadata{}, nil, nil, fmt.Errorf("stat raw Plugin package %q: %w", source, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return localMetadata{}, nil, nil, unsafeContent("raw Plugin package %q is not a physical directory", source)
	}
	snapshot, err := capturePhysicalTree(ctx, c.resourcesRoot, source, c.beforeTreeSnapshot, c.beforeSnapshotFileOpen, c.afterSnapshotDataOpen)
	if err != nil {
		return localMetadata{}, nil, nil, err
	}
	manifestBytes, err := snapshot.regularFile("apc-plugin.json")
	if err != nil {
		return localMetadata{}, nil, nil, err
	}
	var manifest pluginartifact.APCPluginManifest
	if err := jsonstrict.Decode(manifestBytes, &manifest, true); err != nil {
		return localMetadata{}, nil, nil, unsafeContent("decode Local Plugin manifest %s/%s: %v", ref.Name, ref.Version, err)
	}
	if err := pluginartifact.ValidateAPCPluginManifest(ref, manifest); err != nil {
		return localMetadata{}, nil, nil, unsafeContent("validate Local Plugin manifest %s/%s: %v", ref.Name, ref.Version, err)
	}
	entries := make([]pluginartifact.RawPackageEntry, 0, len(snapshot.nodes))
	for _, node := range snapshot.nodes {
		entries = append(entries, pluginartifact.RawPackageEntry{
			Path: filepath.ToSlash(node.relative), Directory: node.directory,
		})
	}
	if err := pluginartifact.ValidateRawPackageEntries(manifest, entries); err != nil {
		return localMetadata{}, nil, nil, unsafeContent("validate Local Plugin snapshot %s/%s: %v", ref.Name, ref.Version, err)
	}
	contents, err := buildLocalCatalogArchive(ctx, snapshot, c.catalogArchiveAfterBodyChunk)
	// 插件展示字段的唯一权威来源是 canonical apc-plugin.json；内嵌 Skill 的同名字段忽略。
	return localMetadata{
		description:   manifest.Description,
		nameZH:        manifest.NameZH,
		nameEN:        manifest.NameEN,
		descriptionZH: manifest.DescriptionZH,
	}, contents, snapshot, err
}

func (c *Client) loadCatalogMCP(ctx context.Context, ref domain.PowerRef) (localMetadata, []byte, error) {
	source := filepath.Join(c.resourcesRoot, "mcps", ref.Name, ref.Version, "MCP.json")
	if _, err := os.Lstat(source); errors.Is(err, fs.ErrNotExist) {
		return localMetadata{}, nil, cloud.ErrNotFound
	} else if err != nil {
		return localMetadata{}, nil, fmt.Errorf("stat Local MCP resource %q: %w", source, err)
	}
	raw, _, err := capturePhysicalFile(ctx, c.resourcesRoot, source, maxSingleSize, c.beforeSnapshotFileOpen, c.afterSnapshotDataOpen)
	if err != nil {
		return localMetadata{}, nil, err
	}
	resource, err := domain.DecodeMCPResourceJSON(raw)
	if err != nil {
		return localMetadata{}, nil, unsafeContent("decode Local MCP resource %s/%s: %v", ref.Name, ref.Version, err)
	}
	if err := resource.ValidatePowerRef(ref); err != nil {
		return localMetadata{}, nil, unsafeContent("validate Local MCP resource %s/%s: %v", ref.Name, ref.Version, err)
	}
	contents, err := submission.CanonicalizeJSON(raw)
	if err != nil {
		return localMetadata{}, nil, unsafeContent("canonicalize Local MCP resource %s/%s: %v", ref.Name, ref.Version, err)
	}
	return localMetadata{
		description:   resource.Description,
		nameZH:        resource.NameZH,
		nameEN:        resource.NameEN,
		descriptionZH: resource.DescriptionZH,
	}, contents, nil
}

// loadCatalogAgent 固定版本目录树，先按 Agent 内容策略捕获（agent.yaml 必须文本、
// assets/ 允许二进制），再转成 artifact Entries 做权威语义校验，最后构建确定性
// gzip 归档；返回发布元数据、归档字节与已校验快照树。
func (c *Client) loadCatalogAgent(ctx context.Context, ref domain.PowerRef) (localMetadata, []byte, *treeSnapshot, error) {
	source := filepath.Join(c.resourcesRoot, "agents", ref.Name, ref.Version)
	info, err := os.Lstat(source)
	if errors.Is(err, fs.ErrNotExist) {
		return localMetadata{}, nil, nil, cloud.ErrNotFound
	}
	if err != nil {
		return localMetadata{}, nil, nil, fmt.Errorf("stat Local Agent package %q: %w", source, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return localMetadata{}, nil, nil, unsafeContent("Local Agent package %q is not a physical directory", source)
	}
	snapshot, err := capturePhysicalTreeWithPolicy(ctx, c.resourcesRoot, source, agentLocalTreeContentPolicy, c.beforeTreeSnapshot, c.beforeSnapshotFileOpen, c.afterSnapshotDataOpen)
	if err != nil {
		return localMetadata{}, nil, nil, err
	}
	entries := make([]agentartifact.Entry, 0, len(snapshot.nodes))
	for _, node := range snapshot.nodes {
		entries = append(entries, agentartifact.Entry{
			Path: filepath.ToSlash(node.relative), Directory: node.directory,
			Mode: node.mode, Data: node.contents,
		})
	}
	validated, err := agentartifact.Validate(entries, &ref)
	if err != nil {
		// agentartifact.Validate 的语义失败已包裹 domain.ErrInvalidAgent，必须原样保留
		// 在错误链中（与 Remote provider 对齐），不得再归类为 cloud.ErrUnsafeContent。
		return localMetadata{}, nil, nil, fmt.Errorf("validate Local Agent snapshot %s/%s: %w", ref.Name, ref.Version, err)
	}
	contents, err := buildLocalCatalogArchive(ctx, snapshot, c.catalogArchiveAfterBodyChunk)
	agent := validated.Config.Agent
	return localMetadata{
		description:   agent.Description,
		nameZH:        agent.NameZH,
		nameEN:        agent.NameEN,
		descriptionZH: agent.DescriptionZH,
	}, contents, snapshot, err
}

func (snapshot *treeSnapshot) regularFile(relative string) ([]byte, error) {
	want := filepath.Clean(relative)
	for _, node := range snapshot.nodes {
		if filepath.Clean(node.relative) == want && !node.directory {
			return append([]byte(nil), node.contents...), nil
		}
	}
	return nil, unsafeContent("captured tree requires physical regular file %q", relative)
}

// localSkillMetadata 是 SKILL.md frontmatter 解析结果：身份（name/version）加发布元数据。
type localSkillMetadata struct {
	name    string
	version string
	localMetadata
}

func parseLocalSkillMetadata(snapshot *treeSnapshot) (localSkillMetadata, error) {
	contents, err := snapshot.regularFile("SKILL.md")
	if err != nil {
		return localSkillMetadata{}, err
	}
	frontmatter, err := extractLocalSkillFrontmatter(contents)
	if err != nil {
		return localSkillMetadata{}, err
	}
	var document yaml.Node
	if err := yaml.Unmarshal(frontmatter, &document); err != nil {
		return localSkillMetadata{}, err
	}
	if document.Kind != yaml.DocumentNode || len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return localSkillMetadata{}, fmt.Errorf("SKILL.md frontmatter must be one mapping")
	}
	mapping := document.Content[0]
	seen := make(map[string]struct{}, len(mapping.Content)/2)
	values := make(map[string]string, 6)
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		key, value := mapping.Content[index], mapping.Content[index+1]
		if key.Kind != yaml.ScalarNode || key.Tag != "!!str" {
			return localSkillMetadata{}, fmt.Errorf("SKILL.md frontmatter key must be a string")
		}
		if _, duplicate := seen[key.Value]; duplicate {
			return localSkillMetadata{}, fmt.Errorf("SKILL.md frontmatter key %q is duplicated", key.Value)
		}
		seen[key.Value] = struct{}{}
		switch key.Value {
		case "name", "version", "description":
			if value.Kind != yaml.ScalarNode || value.Tag != "!!str" || value.Value == "" || strings.TrimSpace(value.Value) != value.Value {
				return localSkillMetadata{}, fmt.Errorf("SKILL.md frontmatter %s must be a non-empty trimmed string", key.Value)
			}
			values[key.Value] = value.Value
		case "name_zh", "name_en", "description_zh":
			// v2 展示字段可选：缺失合法；提供时必须是字符串，形状交给 domain 统一校验。
			if value.Kind != yaml.ScalarNode || value.Tag != "!!str" {
				return localSkillMetadata{}, fmt.Errorf("SKILL.md frontmatter %s must be a string", key.Value)
			}
			values[key.Value] = value.Value
		}
	}
	if !domain.IsCanonicalPowerName(values["name"]) {
		return localSkillMetadata{}, fmt.Errorf("SKILL.md name %q is not canonical", values["name"])
	}
	if _, err := domain.CanonicalPowerVersion(values["version"]); err != nil {
		return localSkillMetadata{}, err
	}
	if values["description"] == "" {
		return localSkillMetadata{}, fmt.Errorf("SKILL.md description is required")
	}
	metadata := localMetadata{
		description:   values["description"],
		nameZH:        values["name_zh"],
		nameEN:        values["name_en"],
		descriptionZH: values["description_zh"],
	}
	if err := domain.ValidateOptionalDisplayMetadata(metadata.nameZH, metadata.nameEN, metadata.descriptionZH); err != nil {
		return localSkillMetadata{}, fmt.Errorf("SKILL.md frontmatter display metadata: %w", err)
	}
	return localSkillMetadata{name: values["name"], version: values["version"], localMetadata: metadata}, nil
}

func extractLocalSkillFrontmatter(contents []byte) ([]byte, error) {
	firstLineEnd := bytes.IndexByte(contents, '\n')
	if firstLineEnd < 0 {
		return nil, fmt.Errorf("SKILL.md must start with YAML frontmatter")
	}
	opener := contents[:firstLineEnd]
	if len(opener) > 0 && opener[len(opener)-1] == '\r' {
		opener = opener[:len(opener)-1]
	}
	if !bytes.HasPrefix(opener, []byte("---")) || len(bytes.Trim(opener[len("---"):], " \t")) != 0 {
		return nil, fmt.Errorf("SKILL.md must start with YAML frontmatter")
	}
	rest := contents[firstLineEnd+1:]
	lineStart := 0
	for lineStart <= len(rest) {
		lineLength := bytes.IndexByte(rest[lineStart:], '\n')
		lineEnd := len(rest)
		if lineLength >= 0 {
			lineEnd = lineStart + lineLength
		}
		marker := strings.TrimSpace(strings.TrimSuffix(string(rest[lineStart:lineEnd]), "\r"))
		if marker == "---" || marker == "..." {
			return rest[:lineStart], nil
		}
		if lineLength < 0 {
			break
		}
		lineStart = lineEnd + 1
	}
	return nil, fmt.Errorf("SKILL.md frontmatter is not closed")
}

func buildLocalCatalogArchive(ctx context.Context, snapshot *treeSnapshot, afterBodyChunk func(string)) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	nodes := append([]treeNodeSnapshot(nil), snapshot.nodes...)
	sort.Slice(nodes, func(left, right int) bool {
		return filepath.ToSlash(nodes[left].relative) < filepath.ToSlash(nodes[right].relative)
	})
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var output bytes.Buffer
	gzipWriter := gzip.NewWriter(&output)
	gzipWriter.Header.ModTime = time.Time{}
	gzipWriter.Header.Name = ""
	tarWriter := tar.NewWriter(gzipWriter)
	for _, node := range nodes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		name := filepath.ToSlash(node.relative)
		header := &tar.Header{Name: name, Uid: 0, Gid: 0, ModTime: time.Unix(0, 0), Size: int64(len(node.contents))}
		if node.directory {
			header.Name += "/"
			header.Mode = int64(publishedDirectoryMode.Perm())
			header.Typeflag = tar.TypeDir
			header.Size = 0
		} else {
			header.Mode = int64(sanitizedTreeFileMode(node.mode).Perm())
			header.Typeflag = tar.TypeReg
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !node.directory {
			if err := writeLocalArchiveBody(ctx, tarWriter, name, node.contents, afterBodyChunk); err != nil {
				return nil, err
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := tarWriter.Close(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := gzipWriter.Close(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func writeLocalArchiveBody(ctx context.Context, writer io.Writer, relative string, contents []byte, afterChunk func(string)) error {
	for len(contents) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		chunk := contents
		if len(chunk) > sourceCopyChunkSize {
			chunk = chunk[:sourceCopyChunkSize]
		}
		written, err := writer.Write(chunk)
		if err != nil {
			return err
		}
		if written != len(chunk) {
			return io.ErrShortWrite
		}
		contents = contents[written:]
		if afterChunk != nil {
			afterChunk(relative)
		}
	}
	return ctx.Err()
}

func (c *Client) catalogSourcePath(ref domain.PowerRef) (string, error) {
	if !safePowerIdentity(ref) || !isCanonicalVersion(ref.Version) {
		return "", cloud.ErrNotFound
	}
	switch ref.Type {
	case domain.PowerTypeSkill:
		return filepath.Join(c.resourcesRoot, "skills", ref.Name, ref.Version), nil
	case domain.PowerTypePlugin:
		return filepath.Join(c.resourcesRoot, "plugins", ref.Name, ref.Version), nil
	case domain.PowerTypeMCP:
		return filepath.Join(c.resourcesRoot, "mcps", ref.Name, ref.Version, "MCP.json"), nil
	case domain.PowerTypeAgent:
		return filepath.Join(c.resourcesRoot, "agents", ref.Name, ref.Version), nil
	default:
		return "", cloud.ErrNotFound
	}
}

func validateLocalCatalogDestination(destination string) error {
	if destination == "" || !filepath.IsAbs(destination) || filepath.Clean(destination) != destination {
		return fmt.Errorf("destination %q must be a clean absolute path", destination)
	}
	name := filepath.Base(destination)
	if name == "" || name == "." || name == ".." {
		return fmt.Errorf("destination %q has an invalid base name", destination)
	}
	parent := filepath.Dir(destination)
	if err := validateDestinationAncestors(parent); err != nil {
		return err
	}
	info, err := os.Lstat(parent)
	if err != nil {
		return fmt.Errorf("inspect destination parent %q: %w", parent, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("destination parent %q is not a physical directory", parent)
	}
	if _, err := os.Lstat(destination); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("inspect destination %q: %w", destination, err)
	}
	return fmt.Errorf("destination %q already exists", destination)
}

func verifyLocalCatalogDestination(output *os.File, destination string, created fs.FileInfo) error {
	actual, err := output.Stat()
	if err != nil {
		return err
	}
	named, err := os.Lstat(destination)
	if err != nil {
		return err
	}
	if !actual.Mode().IsRegular() || actual.Mode().Perm() != localCatalogFileMode || !os.SameFile(created, actual) || !os.SameFile(actual, named) {
		return fmt.Errorf("destination %q changed identity, type, or mode", destination)
	}
	return nil
}

func removeLocalCatalogDestination(destination string, created fs.FileInfo) error {
	if created == nil {
		return fmt.Errorf("refuse cleanup of destination %q without created identity", destination)
	}
	named, err := os.Lstat(destination)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if created != nil && !os.SameFile(created, named) {
		return fmt.Errorf("refuse cleanup of substituted destination %q", destination)
	}
	return os.Remove(destination)
}

func catalogDestinationError(operation string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", operation, errors.Join(cloud.ErrCatalogDestination, err))
}
