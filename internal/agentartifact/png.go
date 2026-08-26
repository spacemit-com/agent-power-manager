package agentartifact

import (
	"bytes"
	"image"
	"strings"

	"apm/internal/domain"
)

const (
	// maxAvatarBytes 是 avatar PNG 的最大字节数（含）。
	maxAvatarBytes = 1 << 20
	// minAvatarSide 与 maxAvatarSide 是 avatar PNG 宽高的允许闭区间。
	minAvatarSide = 128
	maxAvatarSide = 1024
)

// pngSignature 是 PNG 文件头魔数。
var pngSignature = []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}

// validateAvatar 校验 metadata.avatar：nil 表示缺省（跳过全部检查，不要求 assets/）；
// 非 nil 必须为 assets/ 下规范 PNG 相对路径，解析到快照内真实普通文件，
// 大小 1..1 MiB、PNG 签名有效、image.DecodeConfig 格式为 png、正方形且
// 宽高均在 128..1024（含）。不解码全部像素，不校验 DPI/色彩/alpha。
func validateAvatar(config domain.AgentConfig, index map[string]Entry) error {
	avatar := config.Agent.Metadata.Avatar
	if avatar == nil {
		return nil
	}
	value := *avatar
	if strings.TrimSpace(value) == "" {
		return agentError("agent avatar must not be blank")
	}
	if err := canonicalEntryPath(value); err != nil {
		return agentError("agent avatar path %q is not canonical: %v", value, err)
	}
	if !strings.HasPrefix(value, "assets/") {
		return agentError("agent avatar %q must be an assets/ relative path", value)
	}
	if !strings.HasSuffix(value, ".png") {
		return agentError("agent avatar %q must end with .png", value)
	}
	entry, ok := index[value]
	if !ok || entry.Directory {
		return agentError("agent avatar %q must resolve to a regular file in the snapshot", value)
	}
	if len(entry.Data) == 0 {
		return agentError("agent avatar %q must be a nonempty file", value)
	}
	if len(entry.Data) > maxAvatarBytes {
		return agentError("agent avatar %q exceeds %d bytes", value, maxAvatarBytes)
	}
	if !bytes.HasPrefix(entry.Data, pngSignature) {
		return agentError("agent avatar %q is not a PNG", value)
	}
	configImage, format, err := image.DecodeConfig(bytes.NewReader(entry.Data))
	if err != nil || format != "png" {
		return agentError("agent avatar %q is not a decodable PNG", value)
	}
	if configImage.Width < minAvatarSide || configImage.Width > maxAvatarSide ||
		configImage.Height < minAvatarSide || configImage.Height > maxAvatarSide {
		return agentError("agent avatar %q dimensions %dx%d are outside %d..%d",
			value, configImage.Width, configImage.Height, minAvatarSide, maxAvatarSide)
	}
	if configImage.Width != configImage.Height {
		return agentError("agent avatar %q dimensions %dx%d are not square",
			value, configImage.Width, configImage.Height)
	}
	return nil
}
