package storage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestStore(t *testing.T) *Local {
	t.Helper()
	s, err := NewLocal(t.TempDir())
	if err != nil {
		t.Fatalf("创建本地存储失败: %v", err)
	}
	return s
}

func TestLocalPutGetRoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	n, err := s.Put(ctx, "print/a.pdf", strings.NewReader("hello"))
	if err != nil {
		t.Fatalf("Put 失败: %v", err)
	}
	if n != 5 {
		t.Fatalf("Put 返回字节数 %d，期望 5", n)
	}

	size, err := s.Size(ctx, "print/a.pdf")
	if err != nil || size != 5 {
		t.Fatalf("Size 得到 %d, %v", size, err)
	}

	rc, err := s.Get(ctx, "print/a.pdf")
	if err != nil {
		t.Fatalf("Get 失败: %v", err)
	}
	defer rc.Close()
	got, _ := io.ReadAll(rc)
	if string(got) != "hello" {
		t.Fatalf("读到 %q", got)
	}
}

// 覆盖写必须原子：先写临时文件再改名，下载接口就不会读到写了一半的 PDF。
func TestLocalPutOverwritesAtomically(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.Put(ctx, "print/x.pdf", bytes.NewReader(bytes.Repeat([]byte("A"), 1000))); err != nil {
		t.Fatalf("首次 Put 失败: %v", err)
	}
	if _, err := s.Put(ctx, "print/x.pdf", strings.NewReader("B")); err != nil {
		t.Fatalf("覆盖 Put 失败: %v", err)
	}
	rc, err := s.Get(ctx, "print/x.pdf")
	if err != nil {
		t.Fatalf("Get 失败: %v", err)
	}
	defer rc.Close()
	got, _ := io.ReadAll(rc)
	if string(got) != "B" {
		t.Fatalf("覆盖后读到 %q，期望 B", got)
	}
	// 临时文件不能留在根目录下
	entries, err := os.ReadDir(filepath.Join(s.Root(), "print"))
	if err != nil {
		t.Fatalf("列目录失败: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			t.Fatalf("残留了临时文件 %s", e.Name())
		}
	}
}

func TestLocalRejectsPathTraversal(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// 这些路径一个都不能被接受：越界写会把文件丢到 STORAGE_DIR 之外
	bad := []string{
		"../escape.pdf",
		"a/../../escape.pdf",
		"..",
		".",
		"/abs.pdf",
		"",
		"   ",
		`..\escape.pdf`,
	}
	for _, p := range bad {
		if _, err := s.Put(ctx, p, strings.NewReader("x")); err == nil {
			t.Errorf("Put 接受了越界路径 %q", p)
		}
		if _, err := s.Get(ctx, p); err == nil {
			t.Errorf("Get 接受了越界路径 %q", p)
		}
		if _, err := s.LocalPath(p); err == nil {
			t.Errorf("LocalPath 接受了越界路径 %q", p)
		}
	}

	// 正常路径仍要能通过
	if _, err := s.LocalPath("print/ok.pdf"); err != nil {
		t.Fatalf("正常路径被拒: %v", err)
	}
}

func TestLocalGetMissingIsErrNotExist(t *testing.T) {
	s := newTestStore(t)
	_, err := s.Get(context.Background(), "print/nope.pdf")
	if !errors.Is(err, ErrNotExist) {
		t.Fatalf("不存在的对象应返回 ErrNotExist，得到 %v", err)
	}
	if _, err := s.Size(context.Background(), "print/nope.pdf"); !errors.Is(err, ErrNotExist) {
		t.Fatalf("Size 不存在的对象应返回 ErrNotExist，得到 %v", err)
	}
}

// 删除必须幂等：worker 重试清理时不该因为文件已不在而失败。
func TestLocalDeleteIsIdempotent(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if err := s.Delete(ctx, "print/never.pdf"); err != nil {
		t.Fatalf("删除不存在的对象应返回 nil，得到 %v", err)
	}
	if _, err := s.Put(ctx, "print/d.pdf", strings.NewReader("x")); err != nil {
		t.Fatalf("Put 失败: %v", err)
	}
	if err := s.Delete(ctx, "print/d.pdf"); err != nil {
		t.Fatalf("首次删除失败: %v", err)
	}
	if err := s.Delete(ctx, "print/d.pdf"); err != nil {
		t.Fatalf("重复删除失败: %v", err)
	}
}

func TestNewLocalCreatesRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "nested", "dir")
	s, err := NewLocal(root)
	if err != nil {
		t.Fatalf("NewLocal 失败: %v", err)
	}
	info, err := os.Stat(s.Root())
	if err != nil || !info.IsDir() {
		t.Fatalf("根目录未创建: %v", err)
	}
	if _, err := NewLocal("  "); err == nil {
		t.Fatal("空 STORAGE_DIR 应报错")
	}
}
