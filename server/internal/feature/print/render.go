package print

import (
	"bytes"
	"errors"
	"fmt"
	"html/template"
	"strings"
	"sync"

	"kidstudy/templates"
)

// 模板在启动时解析一次并缓存。
//
// 解析方式：base 与具体模板「两两成对」解析。原因是所有模板都定义同名的
// {{define "content"}}，一次全解析会互相覆盖；成对解析后每个 code 得到一份
// 自洽的模板集合，ExecuteTemplate 固定执行 "_base.html" 这个名字。
var (
	tmplOnce sync.Once
	tmpls    map[string]*template.Template
	tmplErr  error
	printCSS template.CSS
)

func loadTemplates() error {
	tmplOnce.Do(func() {
		raw, err := templates.PrintFS.ReadFile(templates.PrintDir + "/print.css")
		if err != nil {
			tmplErr = fmt.Errorf("读取 print.css 失败: %w", err)
			return
		}
		printCSS = template.CSS(raw)

		built := make(map[string]*template.Template, len(catalog))
		for _, spec := range catalog {
			base := templates.PrintDir + "/_base.html"
			page := templates.PrintDir + "/" + spec.Code + ".html"
			t, err := template.New("_base.html").Funcs(tmplFuncs()).ParseFS(templates.PrintFS, base, page)
			if err != nil {
				tmplErr = fmt.Errorf("解析模板 %s 失败: %w", spec.Code, err)
				return
			}
			built[spec.Code] = t
		}
		tmpls = built
	})
	return tmplErr
}

func tmplFuncs() template.FuncMap {
	return template.FuncMap{
		// css 把共享样式内联进 <head>：PDF 与屏幕预览走同一份 HTML 字符串，
		// 不依赖外部请求，chromedp 用 file:// 打开也能拿到样式。
		"css": func() template.CSS { return printCSS },
		// blank 把题面的「?」换成填空横线；比大小这类题面没有 ? 时原样返回。
		"blank": func(s string) string { return strings.Replace(s, "?", "____", 1) },
		// inc 模板里从 0 开始的序号转成 1 开始
		"inc": func(i int) int { return i + 1 },
	}
}

// Render 把 payload 渲染成完整 HTML 文档。
//
// 这是「屏幕预览 = 浏览器打印 = chromedp 出的 PDF」的唯一实现点：
// handler 的预览接口直接返回它，PDF 也是把同一个字符串喂给 Chromium。
// 任何一边另起一套渲染都会让三者产生差异，所以不允许别处再拼打印 HTML。
func Render(payload Payload) (string, error) {
	if err := loadTemplates(); err != nil {
		return "", err
	}
	t, ok := tmpls[payload.TemplateCode]
	if !ok {
		return "", fmt.Errorf("未知的打印模板: %s", payload.TemplateCode)
	}
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, "_base.html", payload); err != nil {
		return "", fmt.Errorf("渲染模板 %s 失败: %w", payload.TemplateCode, err)
	}
	return buf.String(), nil
}

// RenderBytes 是 Render 的 []byte 版本，省一次转换。
func RenderBytes(payload Payload) ([]byte, error) {
	s, err := Render(payload)
	if err != nil {
		return nil, err
	}
	return []byte(s), nil
}

// ErrUnknownTemplate 供上层判断「模板 code 不认识」，映射成 422 而不是 500。
var ErrUnknownTemplate = errors.New("未知的打印模板")
