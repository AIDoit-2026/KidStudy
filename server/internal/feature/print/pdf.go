package print

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

// ErrNoChrome 表示找不到可用的 Chromium 内核。
//
// 刻意不做成启动期 fail-fast：API 进程不渲染 PDF，只有 worker 需要它。
// 让 API 因为这台机器没装浏览器而拒绝启动，会把「看报表」一起拖下水。
var ErrNoChrome = errors.New("未找到可用的 Chromium 内核（请设置 CHROME_PATH）")

// PDFRenderer 用 Chromium 把打印 HTML 渲染成 PDF。
//
// 单例复用一台浏览器实例：Chromium 冷启动约 5 秒，每次渲染都新起一个进程
// 会让「生成 PDF」慢到不可用。渲染各用独立标签页，互不干扰。
type PDFRenderer struct {
	chromePath string
	timeout    time.Duration

	once        sync.Once
	allocCtx    context.Context
	allocCancel context.CancelFunc
	initErr     error
}

// NewPDFRenderer 创建渲染器。chromePath 为空时在首次渲染时自动探测。
func NewPDFRenderer(chromePath string, timeout time.Duration) *PDFRenderer {
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	return &PDFRenderer{chromePath: chromePath, timeout: timeout}
}

// ChromePath 返回实际使用的内核路径（未初始化时返回配置值，可能为空）。
func (r *PDFRenderer) ChromePath() string {
	if r.chromePath != "" {
		return r.chromePath
	}
	return DetectChromePath()
}

// ensure 懒启动浏览器。多次调用只生效一次；出错也会记住，不反复重试。
func (r *PDFRenderer) ensure() error {
	r.once.Do(func() {
		exe := r.chromePath
		if strings.TrimSpace(exe) == "" {
			exe = DetectChromePath()
		}
		if exe == "" {
			r.initErr = ErrNoChrome
			return
		}
		info, err := os.Stat(exe)
		if err != nil || info.IsDir() {
			r.initErr = fmt.Errorf("Chromium 内核不可用 %q: %w", exe, err)
			return
		}

		opts := append(chromedp.DefaultExecAllocatorOptions[:],
			chromedp.ExecPath(exe),
			chromedp.NoFirstRun,
			chromedp.NoDefaultBrowserCheck,
			// 容器/CI 里没有沙箱权限；本项目渲染的是自己生成的静态 HTML，不开沙箱
			// 不引入外部内容风险。
			chromedp.Flag("no-sandbox", true),
			chromedp.Flag("disable-gpu", true),
			chromedp.Flag("disable-dev-shm-usage", true),
		)
		r.allocCtx, r.allocCancel = chromedp.NewExecAllocator(context.Background(), opts...)
		r.chromePath = exe
	})
	return r.initErr
}

// Render 把一份完整的打印 HTML 渲染成 PDF 字节。
//
// 实现要点：先把 HTML 落到临时文件、再用 file:// 打开，而不是让 Chromium 去请求
// 一个接口。这样 worker 不需要反向依赖 API 的地址与鉴权，渲染结果也与
// 「家长在预览页 Ctrl+P」完全等价（同一份 HTML、同一套 CSS）。
func (r *PDFRenderer) Render(ctx context.Context, htmlDoc string) ([]byte, error) {
	if strings.TrimSpace(htmlDoc) == "" {
		return nil, errors.New("待渲染的 HTML 为空")
	}
	if err := r.ensure(); err != nil {
		return nil, err
	}

	dir, err := os.MkdirTemp("", "kidstudy-print-*")
	if err != nil {
		return nil, fmt.Errorf("创建渲染临时目录失败: %w", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	src := filepath.Join(dir, "page.html")
	if err := os.WriteFile(src, []byte(htmlDoc), 0o644); err != nil {
		return nil, fmt.Errorf("写入待渲染 HTML 失败: %w", err)
	}

	tabCtx, cancelTab := chromedp.NewContext(r.allocCtx)
	defer cancelTab()

	// 调用方的取消/超时也要能中断渲染，否则 worker 停机会被渲染卡住
	watchDone := make(chan struct{})
	defer close(watchDone)
	go func() {
		select {
		case <-ctx.Done():
			cancelTab()
		case <-watchDone:
		}
	}()

	tabCtx, cancelTimeout := context.WithTimeout(tabCtx, r.timeout)
	defer cancelTimeout()

	var buf []byte
	err = chromedp.Run(tabCtx,
		chromedp.Navigate("file:///"+filepath.ToSlash(src)),
		chromedp.ActionFunc(func(ctx context.Context) error {
			var e error
			// PreferCSSPageSize：以模板里的 @page 尺寸为准（A4/A5 由模板决定）
			buf, _, e = page.PrintToPDF().
				WithPrintBackground(true).
				WithPreferCSSPageSize(true).
				Do(ctx)
			return e
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("Chromium 渲染 PDF 失败: %w", err)
	}
	if len(buf) == 0 {
		return nil, errors.New("Chromium 返回了空 PDF")
	}
	return buf, nil
}

// Close 关掉浏览器实例。调用后不可再 Render。
func (r *PDFRenderer) Close() {
	if r.allocCancel != nil {
		r.allocCancel()
	}
}

// DetectChromePath 按「显式配置 → Playwright 缓存 → 系统安装路径」的顺序探测。
func DetectChromePath() string {
	if p := strings.TrimSpace(os.Getenv("CHROME_PATH")); p != "" {
		return p
	}
	for _, p := range chromeCandidates() {
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p
		}
	}
	return ""
}

func chromeCandidates() []string {
	var out []string
	if runtime.GOOS == "windows" {
		if local := os.Getenv("LOCALAPPDATA"); local != "" {
			// Playwright 自带的 Chromium 是这台机器上最可能存在的一份：
			// 同目录下会有 chromium-<build> 多个版本，按 build 号倒序取最新。
			out = append(out, newestByBuild(glob(
				filepath.Join(local, "ms-playwright", "chromium-*", "chrome-win64", "chrome.exe")))...)
		}
		out = append(out,
			`C:\Program Files\Google\Chrome\Application\chrome.exe`,
			`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
			`C:\Program Files\Microsoft\Edge\Application\msedge.exe`,
			`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`,
		)
		return out
	}
	return append(out,
		"/usr/bin/chromium",
		"/usr/bin/chromium-browser",
		"/usr/bin/google-chrome",
		"/usr/bin/google-chrome-stable",
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
	)
}

func glob(pattern string) []string {
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil
	}
	return matches
}

// newestByBuild 取路径中 `-<数字>` 那一段最大的几个，数字大的排前面。
//
// 不能用字符串倒序：字符串比较下 "chromium-999" 会排在 "chromium-1234" 之前。
func newestByBuild(paths []string) []string {
	buildOf := func(p string) int {
		for _, seg := range strings.Split(filepath.ToSlash(p), "/") {
			i := strings.LastIndex(seg, "-")
			if i < 0 || i == len(seg)-1 {
				continue
			}
			if n, err := strconv.Atoi(seg[i+1:]); err == nil {
				return n
			}
		}
		return -1
	}
	sort.SliceStable(paths, func(i, j int) bool {
		return buildOf(paths[i]) > buildOf(paths[j])
	})
	return paths
}

// maxSanePages 是页数的合理性上限：超过它只可能是把压缩流里的巧合字节当成了结构，
// 不能当真实页数用。
const maxSanePages = 500

// pdfPageCount 从 PDF 字节里读出总页数。
//
// 为什么不自己按排版规则估：估出来的数字会被家长当成事实（「共 3 页」），
// 而 page_count 还要写进库、进列表、进统计。Chromium 写出的 PDF 页树是**未压缩**
// 的对象（实测 ObjStm 计数为 0），所以直接读结构是可靠的。
//
// 两个来源互为校验：
//   - /Type /Page 出现次数（排除 /Type /Pages，它是前缀包含关系，必须用 [^s] 区分）
//   - 各级 /Pages 节点的 /Count —— Chrome 会按子树拆（例如 8 + 7 与根 15），
//     所以取最大值而不是第一个
//
// 两者都有且相等才直接采信；只有其一可用时用可用的那个；都对不上就返回 false，
// 由调用方退回预估页数并如实标注。
func pdfPageCount(buf []byte) (int, bool) {
	if len(buf) == 0 {
		return 0, false
	}
	byType := countTypePage(buf)
	byCount := maxCountField(buf)

	if byType > 0 && byType <= maxSanePages && byType == byCount {
		return byType, true
	}
	if byType > 0 && byType <= maxSanePages {
		return byType, true
	}
	if byCount > 0 && byCount <= maxSanePages {
		return byCount, true
	}
	return 0, false
}

// countTypePage 数 `/Type /Page` 且后面不是 's' 的次数（即排除 /Pages）。
func countTypePage(buf []byte) int {
	needle := []byte("/Type")
	n := 0
	for i := 0; ; {
		j := bytes.Index(buf[i:], needle)
		if j < 0 {
			return n
		}
		p := i + j + len(needle)
		// 跳过空白
		for p < len(buf) && isPDFSpace(buf[p]) {
			p++
		}
		if bytes.HasPrefix(buf[p:], []byte("/Pages")) {
			i = p + len("/Pages")
			continue
		}
		if bytes.HasPrefix(buf[p:], []byte("/Page")) {
			n++
			i = p + len("/Page")
			continue
		}
		i = p
	}
}

// maxCountField 取所有 `/Count <n>` 里的最大值。
func maxCountField(buf []byte) int {
	needle := []byte("/Count")
	best := 0
	for i := 0; ; {
		j := bytes.Index(buf[i:], needle)
		if j < 0 {
			return best
		}
		p := i + j + len(needle)
		for p < len(buf) && isPDFSpace(buf[p]) {
			p++
		}
		start := p
		for p < len(buf) && buf[p] >= '0' && buf[p] <= '9' {
			p++
		}
		if p > start {
			if n, err := strconv.Atoi(string(buf[start:p])); err == nil && n > best {
				best = n
			}
		}
		i = p
	}
}

func isPDFSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\r' || b == '\n' || b == '\f' || b == 0
}
