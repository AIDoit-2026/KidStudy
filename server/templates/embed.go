// Package templates 仅用于把打印模板嵌入二进制（设计文档 §2.1 的 templates/print/）。
//
// 模板与 CSS 放在代码库而不是运行期目录：打印件是「同一份 HTML 三处复用」
// （屏幕预览 / 浏览器 Ctrl+P / chromedp 出 PDF），必须和二进制一起版本化 ——
// 否则部署时漏传一个模板文件，家长看到的就是空白页。
package templates

import "embed"

// PrintFS 包含打印模板的全部 HTML 与 CSS。
//
//go:embed print/*.html print/*.css
var PrintFS embed.FS

// PrintDir 是 PrintFS 里打印模板所在的子目录。
const PrintDir = "print"
