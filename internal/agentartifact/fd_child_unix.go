//go:build linux || darwin || freebsd

package agentartifact

import (
	"os"

	"golang.org/x/sys/unix"
)

// openChildFd 在固定目录句柄内以 fd 相对方式打开子节点并拒绝 symlink；
// 返回的句柄仍绑定调用方持有的目录边界，不依赖任何可被并发重定向的路径。
func openChildFd(directory *os.File, name string) (*os.File, error) {
	fd, err := unix.Openat(int(directory.Fd()), name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_NOCTTY, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), name), nil
}
