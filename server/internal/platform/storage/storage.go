// Package storage 是运行期对象存储的抽象（设计文档 §2.1）。
//
// 现在只有本地文件系统一种实现：单机部署 + PDF/音频这类「生成后很少改」的对象，
// 本地盘足够。接口按 S3 的最小面来定（Put/Get/Size/Delete + 相对路径），
// 日后要换对象存储只需再写一个实现，调用方（print / 未来的 TTS 预生成）不动。
//
// 相对路径约定：一律用正斜杠、不含前导斜杠，例如 "print/<uuid>.pdf"。
// 实现必须拒绝越权路径（.. 逃逸出根目录），这是唯一的安全边界。
package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Store 是对象存储接口。
type Store interface {
	// Put 写入对象，返回写入字节数；父目录自动创建。同路径重复写入直接覆盖。
	Put(ctx context.Context, relPath string, r io.Reader) (int64, error)
	// Get 打开对象读取；对象不存在时返回 ErrNotExist。
	Get(ctx context.Context, relPath string) (io.ReadCloser, error)
	// Size 返回对象字节数；不存在时返回 ErrNotExist。
	Size(ctx context.Context, relPath string) (int64, error)
	// Delete 删除对象；对象不存在时返回 nil（删除是幂等的）。
	Delete(ctx context.Context, relPath string) error
	// LocalPath 返回对象在本机的绝对路径。
	//
	// 只有本地实现能提供它，用途单一：chromedp 需要一个 file:// URL 才能渲染 PDF。
	// 换 S3 后这个方法会返回错误，届时 PDF 渲染改为先把对象取回本地临时目录。
	LocalPath(relPath string) (string, error)
}

// ErrNotExist 表示对象不存在。调用方用 errors.Is 判断，不依赖具体实现。
var ErrNotExist = errors.New("对象不存在")

// Local 是本地文件系统实现。
type Local struct {
	root string
}

// NewLocal 创建（必要时）根目录并返回本地存储。
func NewLocal(root string) (*Local, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("STORAGE_DIR 为空")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("解析 STORAGE_DIR 失败: %w", err)
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, fmt.Errorf("创建 STORAGE_DIR 失败: %w", err)
	}
	return &Local{root: abs}, nil
}

// Root 返回根目录绝对路径（日志与健康检查用）。
func (l *Local) Root() string { return l.root }

// resolve 把相对路径安全地解析成绝对路径。
//
// 两道防线：① path.Clean 后必须是「相对的、不以 .. 开头」的规范形式；
// ② 拼出来的绝对路径必须仍在根目录之内。第二道是为了兜住符号链接与
// Windows 盘符/UNC 之类的边角情况。
func (l *Local) resolve(relPath string) (string, error) {
	if strings.TrimSpace(relPath) == "" {
		return "", errors.New("对象路径为空")
	}
	rel := strings.ReplaceAll(relPath, "\\", "/")
	if strings.HasPrefix(rel, "/") {
		return "", fmt.Errorf("对象路径不允许以 / 开头: %q", relPath)
	}
	clean := path.Clean(rel)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("对象路径越界: %q", relPath)
	}
	abs := filepath.Join(l.root, filepath.FromSlash(clean))
	// 再确认一次落点在根目录内（filepath.Rel 比字符串前缀更可靠）
	relCheck, err := filepath.Rel(l.root, abs)
	if err != nil || relCheck == ".." || strings.HasPrefix(relCheck, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("对象路径越界: %q", relPath)
	}
	return abs, nil
}

// Put 先写临时文件再原子改名：避免读到「写了一半」的 PDF（下载接口与 worker 并发时会发生）。
func (l *Local) Put(ctx context.Context, relPath string, r io.Reader) (int64, error) {
	abs, err := l.resolve(relPath)
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return 0, fmt.Errorf("创建对象目录失败: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(abs), ".tmp-*")
	if err != nil {
		return 0, fmt.Errorf("创建临时文件失败: %w", err)
	}
	tmpName := tmp.Name()
	// 任何一条失败路径都要把临时文件收掉，否则 STORAGE_DIR 会攒垃圾
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}()

	n, err := io.Copy(tmp, r)
	if err != nil {
		return 0, fmt.Errorf("写入对象失败: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return 0, fmt.Errorf("刷盘失败: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return 0, fmt.Errorf("关闭临时文件失败: %w", err)
	}
	if err := os.Rename(tmpName, abs); err != nil {
		return 0, fmt.Errorf("改名对象失败: %w", err)
	}
	return n, nil
}

func (l *Local) Get(ctx context.Context, relPath string) (io.ReadCloser, error) {
	abs, err := l.resolve(relPath)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %s", ErrNotExist, relPath)
		}
		return nil, fmt.Errorf("打开对象失败: %w", err)
	}
	return f, nil
}

func (l *Local) Size(ctx context.Context, relPath string) (int64, error) {
	abs, err := l.resolve(relPath)
	if err != nil {
		return 0, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, fmt.Errorf("%w: %s", ErrNotExist, relPath)
		}
		return 0, fmt.Errorf("读取对象信息失败: %w", err)
	}
	return info.Size(), nil
}

func (l *Local) Delete(ctx context.Context, relPath string) error {
	abs, err := l.resolve(relPath)
	if err != nil {
		return err
	}
	if err := os.Remove(abs); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("删除对象失败: %w", err)
	}
	return nil
}

func (l *Local) LocalPath(relPath string) (string, error) {
	return l.resolve(relPath)
}
