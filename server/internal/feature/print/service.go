package print

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"

	"kidstudy/internal/dbgen"
	"kidstudy/internal/feature/content"
	"kidstudy/internal/feature/mastery"
	"kidstudy/internal/feature/report"
	"kidstudy/internal/pkg/mathgen"
	"kidstudy/internal/pkg/randx"
	"kidstudy/internal/platform/apperr"
	"kidstudy/internal/platform/storage"
)

// 打印任务状态（与迁移里的 CHECK 约束一致）。
const (
	StatusCreated   = "created"
	StatusQueued    = "queued"
	StatusRendering = "rendering"
	StatusReady     = "ready"
	StatusFailed    = "failed"
)

// ContentProvider 是打印需要的素材来源，由 content.Service 实现。
type ContentProvider interface {
	LoadMaterials(ctx context.Context, kpIDs []uuid.UUID) (content.Materials, error)
	GetMathTemplate(ctx context.Context, code string) (content.MathMaterial, error)
}

// ReportSource 是周学习报告的数据来源，由 report.Service 实现。
//
// 这里比设计文档的依赖图多了一条 print → report 的边：周报模板要的就是
// 「趋势 + 学科分布 + 建议」，让 print 自己再写一遍这些查询等于把报表口径抄两份，
// 迟早对不上。方向仍是单向的（report 不认识 print），且只读。
type ReportSource interface {
	Overview(ctx context.Context, childID uuid.UUID) (report.OverviewView, error)
	Trend(ctx context.Context, childID uuid.UUID, days int, subject string) (report.TrendView, error)
	Suggestions(ctx context.Context, childID uuid.UUID) (report.SuggestionsView, error)
}

// Service 承载打印模板的数据生成、任务生命周期与纸质补录。不 import net/http。
type Service struct {
	repo     *Repository
	content  ContentProvider
	mastery  *mastery.Service
	report   ReportSource
	store    storage.Store
	renderer *PDFRenderer
	log      *slog.Logger
	now      func() time.Time
}

// NewService 构造打印服务。reportSvc 可以为 nil（周报模板会返回空表而不是报错）。
func NewService(repo *Repository, contentSvc ContentProvider, masterySvc *mastery.Service,
	reportSvc ReportSource, store storage.Store, renderer *PDFRenderer, log *slog.Logger) *Service {
	return &Service{
		repo: repo, content: contentSvc, mastery: masterySvc, report: reportSvc,
		store: store, renderer: renderer, log: log, now: time.Now,
	}
}

// Templates 返回模板注册表，供家长端渲染表单（§4.6 步骤 1）。
func (s *Service) Templates() []TemplateSpec { return Catalog() }

// ---------------------------------------------------------------- 请求与视图

// CreateRequest 是建打印任务的入参。
type CreateRequest struct {
	TemplateCode string
	ChildID      *uuid.UUID
	Params       Params
}

// JobView 是打印任务的对外视图。
type JobView struct {
	ID           string          `json:"id"`
	ChildID      string          `json:"child_id"`
	TemplateCode string          `json:"template_code"`
	TemplateName string          `json:"template_name"`
	Title        string          `json:"title"`
	Params       json.RawMessage `json:"params"`
	Status       string          `json:"status"`
	PlannedPages int             `json:"planned_pages"`
	PageCount    int             `json:"page_count"`
	PDFReady     bool            `json:"pdf_ready"`
	MarkedDone   bool            `json:"marked_done"`
	MarkedDoneAt string          `json:"marked_done_at,omitempty"`
	ErrorMessage string          `json:"error_message,omitempty"`
	PreviewURL   string          `json:"preview_url"`
	DataURL      string          `json:"data_url"`
	PDFURL       string          `json:"pdf_url"`
	CreatedAt    string          `json:"created_at"`
}

// MarkDoneItem 是补录时对单个知识点的判定。
type MarkDoneItem struct {
	KpID    string `json:"kp_id"`
	Correct *bool  `json:"correct"`
}

// MarkDoneRequest 是纸质补录入参。items 为空表示「全部做对」。
type MarkDoneRequest struct {
	Items []MarkDoneItem `json:"items"`
	Note  string         `json:"note"`
}

// MarkDoneResult 是补录结果。
type MarkDoneResult struct {
	JobID        string `json:"job_id"`
	ChildID      string `json:"child_id"`
	SessionID    string `json:"session_id"`
	ItemCount    int    `json:"item_count"`
	CorrectCount int    `json:"correct_count"`
	Mastered     int    `json:"mastered"`
	AlreadyDone  bool   `json:"already_done"`
}

// ---------------------------------------------------------------- 建任务

// CreateJob 校验参数、生成题面数据快照并落库。
func (s *Service) CreateJob(ctx context.Context, parentID uuid.UUID, req CreateRequest) (JobView, error) {
	spec, ok := SpecByCode(strings.TrimSpace(req.TemplateCode))
	if !ok {
		return JobView{}, apperr.BadRequest(fmt.Sprintf("没有这个打印模板：%s", req.TemplateCode))
	}

	params := normalize(spec, req.Params)

	var child Child
	hasChild := req.ChildID != nil && *req.ChildID != uuid.Nil
	if hasChild {
		c, notFound, err := s.repo.ChildBasic(ctx, parentID, *req.ChildID)
		if err != nil {
			return JobView{}, apperr.Internal(err)
		}
		if notFound {
			// 与 children 模块一致：越权当作不存在，不暴露 ID 是否有效
			return JobView{}, apperr.NotFound("孩子档案不存在")
		}
		child = c
	} else if spec.NeedsChild {
		return JobView{}, apperr.BadRequest(fmt.Sprintf("「%s」需要先选择孩子", spec.Name))
	}

	payload, err := s.buildPayload(ctx, spec, params, child, hasChild)
	if err != nil {
		return JobView{}, err
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return JobView{}, apperr.Internal(fmt.Errorf("序列化题面数据失败: %w", err))
	}
	// 注意：normalize 与 effectiveSeed 可能已经改过 params（补默认值、回写 seed），
	// 所以序列化必须发生在 buildPayload 之后。
	paramsBytes, err := json.Marshal(params)
	if err != nil {
		return JobView{}, apperr.Internal(fmt.Errorf("序列化打印参数失败: %w", err))
	}

	childID := uuid.Nil
	if hasChild {
		childID = child.ID
	}
	job, err := s.repo.CreateJob(ctx, parentID, childID, spec.Code, paramsBytes, payloadBytes, len(payload.Items))
	if err != nil {
		return JobView{}, apperr.Internal(fmt.Errorf("创建打印任务失败: %w", err))
	}

	s.log.Info("打印任务已创建",
		"job_id", job.ID, "template", spec.Code, "child_id", childID, "items", len(payload.Items))

	return jobView(job, spec, payload), nil
}

// normalize 用模板定义的默认值补齐缺项，并把数值夹到合法区间。
//
// 家长端填的是自由 JSON，边界处不信任：越界的字号会把一页塞爆，
// 负数的题量会生成空卷。这里统一收口，service 下游拿到的都是合法值。
func normalize(spec TemplateSpec, in Params) Params {
	out := Params{}
	for k, v := range in {
		out[k] = v
	}
	for _, ps := range spec.Params {
		if _, exists := out[ps.Name]; !exists {
			out[ps.Name] = ps.Default
		}
		if ps.Type != "int" {
			continue
		}
		v := out.GetInt(ps.Name, 0)
		if ps.Min != nil && v < *ps.Min {
			v = *ps.Min
		}
		if ps.Max != nil && v > *ps.Max {
			v = *ps.Max
		}
		out[ps.Name] = v
	}
	return out
}

func jobView(job dbgen.PrintJob, spec TemplateSpec, payload Payload) JobView {
	view := JobView{
		ID:           job.ID.String(),
		ChildID:      job.ChildID.String(),
		TemplateCode: spec.Code,
		TemplateName: spec.Name,
		Title:        payload.Title,
		Params:       json.RawMessage(job.Params),
		Status:       job.Status,
		PlannedPages: planPages(payload),
		PageCount:    int(job.PageCount),
		PDFReady:     job.Status == StatusReady && job.PdfPath.Valid,
		MarkedDone:   job.MarkedDoneAt.Valid,
		ErrorMessage: job.ErrorMessage,
		PreviewURL:   fmt.Sprintf("/print/jobs/%s/preview", job.ID),
		DataURL:      fmt.Sprintf("/print/jobs/%s/data", job.ID),
		PDFURL:       fmt.Sprintf("/print/jobs/%s/pdf", job.ID),
		CreatedAt:    job.CreatedAt.Time.Format(time.RFC3339),
	}
	if job.MarkedDoneAt.Valid {
		view.MarkedDoneAt = job.MarkedDoneAt.Time.Format(time.RFC3339)
	}
	return view
}

// planPages 按排版规则推算页数，供「还没出 PDF 时」展示与预估打印成本。
//
// 这是**预估值**：真实页数在 PDF 渲染完成后写回 page_count（见 pdfPageCount）。
// 视图里两者是两个字段，不拿预估冒充真实。
func planPages(p Payload) int {
	if p.Options.PerPage <= 0 {
		return 1
	}
	pages := int(math.Ceil(float64(len(p.Items)) / float64(p.Options.PerPage)))
	if pages < 1 {
		pages = 1
	}
	if p.Options.WithAnswer && len(p.AnswerItems) > 0 {
		pages++
	}
	if len(p.Sheets) > 0 {
		pages++
	}
	return pages
}

// ---------------------------------------------------------------- 读任务

// GetJob 取单个任务视图。
func (s *Service) GetJob(ctx context.Context, parentID, id uuid.UUID) (JobView, error) {
	job, notFound, err := s.repo.GetJob(ctx, parentID, id)
	if err != nil {
		return JobView{}, apperr.Internal(err)
	}
	if notFound {
		return JobView{}, apperr.NotFound("打印任务不存在")
	}
	spec, _ := SpecByCode(job.TemplateCode)
	var payload Payload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return JobView{}, apperr.Internal(fmt.Errorf("解析题面数据失败: %w", err))
	}
	return jobView(job, spec, payload), nil
}

// LoadJob 取任务的 payload；供渲染与补录复用。
func (s *Service) loadJob(ctx context.Context, parentID, id uuid.UUID) (dbgen.PrintJob, Payload, error) {
	job, notFound, err := s.repo.GetJob(ctx, parentID, id)
	if err != nil {
		return dbgen.PrintJob{}, Payload{}, apperr.Internal(err)
	}
	if notFound {
		return dbgen.PrintJob{}, Payload{}, apperr.NotFound("打印任务不存在")
	}
	var payload Payload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return dbgen.PrintJob{}, Payload{}, apperr.Internal(fmt.Errorf("解析题面数据失败: %w", err))
	}
	return job, payload, nil
}

// Payload 取渲染数据快照，原样返回（/print/jobs/{id}/data）。
func (s *Service) Payload(ctx context.Context, parentID, id uuid.UUID) (Payload, error) {
	_, payload, err := s.loadJob(ctx, parentID, id)
	return payload, err
}

// PreviewHTML 渲染预览 HTML。
//
// 与 PDF 走同一个 Render：这就是「屏幕 = 打印 = PDF」的落点。
func (s *Service) PreviewHTML(ctx context.Context, parentID, id uuid.UUID) (string, error) {
	_, payload, err := s.loadJob(ctx, parentID, id)
	if err != nil {
		return "", err
	}
	html, err := Render(payload)
	if err != nil {
		return "", apperr.Internal(err)
	}
	return html, nil
}

// List 列某家长的打印任务。childID 为零值时不过滤孩子。
func (s *Service) List(ctx context.Context, parentID, childID uuid.UUID, limit, offset int) ([]JobView, int64, error) {
	rows, err := s.repo.ListJobs(ctx, parentID, childID, limit, offset)
	if err != nil {
		return nil, 0, apperr.Internal(err)
	}
	total, err := s.repo.CountJobs(ctx, parentID, childID)
	if err != nil {
		return nil, 0, apperr.Internal(err)
	}
	out := make([]JobView, 0, len(rows))
	for _, row := range rows {
		spec, _ := SpecByCode(row.TemplateCode)
		var payload Payload
		// 列表里 payload 解析失败不该让整页 500：退化成只有模板名的条目
		_ = json.Unmarshal(row.Payload, &payload)
		out = append(out, jobView(row, spec, payload))
	}
	return out, total, nil
}

// ---------------------------------------------------------------- PDF 排队与下载

// QueuePDF 把任务排进渲染队列。幂等：已就绪的任务保持 ready。
func (s *Service) QueuePDF(ctx context.Context, parentID, id uuid.UUID) (JobView, error) {
	_, notFound, err := s.repo.MarkQueued(ctx, parentID, id)
	if err != nil {
		return JobView{}, apperr.Internal(err)
	}
	if notFound {
		return JobView{}, apperr.NotFound("打印任务不存在")
	}
	return s.GetJob(ctx, parentID, id)
}

// PDFPath 取已生成 PDF 的存储相对路径；未就绪时返回 notReady=true。
func (s *Service) PDFPath(ctx context.Context, parentID, id uuid.UUID) (string, bool, error) {
	job, notFound, err := s.repo.GetJob(ctx, parentID, id)
	if err != nil {
		return "", false, apperr.Internal(err)
	}
	if notFound {
		return "", false, apperr.NotFound("打印任务不存在")
	}
	if job.Status != StatusReady || !job.PdfPath.Valid {
		return "", true, nil
	}
	return job.PdfPath.String, false, nil
}

// ---------------------------------------------------------------- 纸质补录

// MarkDone 把「纸上已做完」写回进度。
//
// 事务边界：标记完成 + 建补录会话 + 逐个写掌握度 + 清专项指派，四步要么全成要么全不成。
// 幂等：marked_done_at 只在为空时写，重复提交返回 AlreadyDone 而不是重复计分。
func (s *Service) MarkDone(ctx context.Context, parentID, id uuid.UUID, req MarkDoneRequest) (MarkDoneResult, error) {
	spec, payload, err := s.specAndPayload(ctx, parentID, id)
	if err != nil {
		return MarkDoneResult{}, err
	}
	if !spec.Answerable {
		return MarkDoneResult{}, apperr.BadRequest(fmt.Sprintf("「%s」是纯教具，没有可补录的作答内容", spec.Name))
	}

	// 补录的知识点以 payload 为准：payload 是不可变快照，不随内容库变动。
	items := collectKPs(payload)
	if len(items) == 0 {
		return MarkDoneResult{}, apperr.BadRequest("这次打印没有可补录的知识点")
	}

	verdicts, err := mergeVerdicts(items, req.Items)
	if err != nil {
		return MarkDoneResult{}, err
	}

	tx, err := s.repo.Begin(ctx)
	if err != nil {
		return MarkDoneResult{}, apperr.Internal(fmt.Errorf("开启事务失败: %w", err))
	}
	defer func() { _ = tx.Rollback(ctx) }()

	repoTx := s.repo.WithTx(tx)
	row, alreadyDone, err := repoTx.MarkDone(ctx, parentID, id)
	if err != nil {
		return MarkDoneResult{}, apperr.Internal(err)
	}
	if alreadyDone {
		// 已经补录过：不再写掌握度，直接告诉调用方这是重复提交
		return MarkDoneResult{JobID: id.String(), AlreadyDone: true}, nil
	}

	childID := row.ChildID
	kpIDs := make([]uuid.UUID, 0, len(verdicts))
	correctCount := 0
	for _, v := range verdicts {
		kpIDs = append(kpIDs, v.kpID)
		if v.correct {
			correctCount++
		}
	}

	sessionID, err := repoTx.InsertSession(ctx, childID, id, len(verdicts), len(verdicts), correctCount, req.Note)
	if err != nil {
		return MarkDoneResult{}, apperr.Internal(fmt.Errorf("建补录会话失败: %w", err))
	}

	masteryTx := s.mastery.WithTx(tx)
	mastered := 0
	for _, v := range verdicts {
		res, err := masteryTx.ApplyResult(ctx, childID, v.kpID, v.correct, false, 0)
		if err != nil {
			return MarkDoneResult{}, apperr.Internal(fmt.Errorf("写掌握度失败: %w", err))
		}
		if res.Mastered {
			mastered++
		}
	}

	if err := repoTx.MarkAssignmentsDone(ctx, childID, kpIDs); err != nil {
		return MarkDoneResult{}, apperr.Internal(fmt.Errorf("清除专项指派失败: %w", err))
	}

	if err := tx.Commit(ctx); err != nil {
		return MarkDoneResult{}, apperr.Internal(fmt.Errorf("提交补录事务失败: %w", err))
	}

	s.log.Info("纸质补录完成",
		"job_id", id, "child_id", childID, "items", len(verdicts),
		"correct", correctCount, "mastered", mastered)

	return MarkDoneResult{
		JobID:        id.String(),
		ChildID:      childID.String(),
		SessionID:    sessionID.String(),
		ItemCount:    len(verdicts),
		CorrectCount: correctCount,
		Mastered:     mastered,
	}, nil
}

func (s *Service) specAndPayload(ctx context.Context, parentID, id uuid.UUID) (TemplateSpec, Payload, error) {
	job, payload, err := s.loadJob(ctx, parentID, id)
	if err != nil {
		return TemplateSpec{}, Payload{}, err
	}
	spec, ok := SpecByCode(job.TemplateCode)
	if !ok {
		return TemplateSpec{}, Payload{}, apperr.Internal(fmt.Errorf("任务引用了未知模板 %q", job.TemplateCode))
	}
	return spec, payload, nil
}

// verdict 是一个知识点的补录判定。
type verdict struct {
	kpID    uuid.UUID
	correct bool
}

// collectKPs 从 payload 里去重收集带 kp_id 的元素（保持出现顺序）。
func collectKPs(payload Payload) []uuid.UUID {
	seen := map[uuid.UUID]struct{}{}
	out := make([]uuid.UUID, 0, len(payload.Items))
	for _, it := range payload.Items {
		if it.KpID == "" {
			continue
		}
		id, err := uuid.Parse(it.KpID)
		if err != nil {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

// mergeVerdicts 把请求里的逐项判定合并到 payload 的知识点集合上。
//
// 规则：请求里没提到的知识点按「做对」处理（家长通常是「大部分对了，只有两个错」），
// 请求里出现但不在 payload 的 kp 直接忽略 —— 防止用别的任务的知识点来回填进度。
func mergeVerdicts(kpIDs []uuid.UUID, req []MarkDoneItem) ([]verdict, error) {
	override := map[uuid.UUID]bool{}
	for _, it := range req {
		id, err := uuid.Parse(strings.TrimSpace(it.KpID))
		if err != nil {
			return nil, apperr.BadRequest(fmt.Sprintf("知识点 ID 格式不正确：%s", it.KpID))
		}
		correct := true
		if it.Correct != nil {
			correct = *it.Correct
		}
		override[id] = correct
	}
	out := make([]verdict, 0, len(kpIDs))
	for _, id := range kpIDs {
		correct := true
		if v, ok := override[id]; ok {
			correct = v
		}
		out = append(out, verdict{kpID: id, correct: correct})
	}
	return out, nil
}

// ---------------------------------------------------------------- 参数与模板小工具

func (s *Service) optionsOf(p Params) Options {
	return Options{
		FontSize:   p.GetInt("font_size", 12),
		WithPinyin: p.GetBool("with_pinyin", true),
		WithAnswer: p.GetBool("with_answer", false),
		PerPage:    p.GetInt("per_page", 12),
		Columns:    p.GetInt("columns", 2),
		BlankLines: p.GetBool("blank_lines", false),
		ShowStroke: p.GetBool("show_stroke", false),
	}
}

// effectiveSeed 取本次打印的随机种子。
//
// seed=0 表示「每次随机」，此时抽一个种子并**回写进 params**：题面快照本身不可变，
// 但家长看到 seed 之后可以再建一个任务复现同一套题（§4.2「同一 seed 可复现」）。
func effectiveSeed(p Params) uint64 {
	if v := p.GetInt("seed", 0); v > 0 {
		return uint64(v)
	}
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return uint64(time.Now().UnixNano()) % (1 << 31)
	}
	seed := binary.LittleEndian.Uint64(b[:]) % (1 << 31)
	if seed == 0 {
		seed = 1
	}
	p["seed"] = int(seed)
	return seed
}

func (s *Service) meta(spec TemplateSpec, p Params, child Child, source string) Meta {
	now := s.now()
	meta := Meta{
		ChildName:   child.Nickname,
		ChildStage:  child.StageCode,
		DateLabel:   now.Format("2006-01-02"),
		Paper:       p.GetEnum("paper", []string{"A4", "A5"}, spec.PaperSize),
		Copies:      p.GetInt("copies", 1),
		GeneratedAt: now.Format("2006-01-02 15:04"),
		Source:      source,
	}
	if p.GetBool("watermark", true) {
		meta.Watermark = "KidStudy · 生成于 " + meta.GeneratedAt
	}
	return meta
}

func (s *Service) newPayload(spec TemplateSpec, p Params, meta Meta, title, subtitle string) Payload {
	return Payload{
		TemplateCode: spec.Code,
		TemplateName: spec.Name,
		RenderVer:    RenderVersion,
		Title:        title,
		Subtitle:     subtitle,
		Meta:         meta,
		Options:      s.optionsOf(p),
		Footer:       "KidStudy 亲子学习",
	}
}

// pinyinText 把拼音数组拼成展示串（多音字用「/」分隔，与内容模块一致）。
func pinyinText(ps []string) string {
	if len(ps) == 0 {
		return ""
	}
	return strings.Join(ps, " / ")
}

// shuffleBySeed 用种子决定性地打乱切片：连线题右列必须打乱，但同一 seed 要能复现。
func shuffleBySeed[T any](xs []T, seed uint64) []T {
	out := make([]T, len(xs))
	copy(out, xs)
	if len(out) < 2 {
		return out
	}
	randx.Shuffle(randx.New(seed), out)
	return out
}

// mathConfigFor 取数学模板的生成配置（与屏幕练习同一张 math_templates 表）。
func (s *Service) mathConfigFor(ctx context.Context, code string) (mathgen.Config, error) {
	mat, err := s.content.GetMathTemplate(ctx, code)
	if err != nil {
		return mathgen.Config{}, apperr.BadRequest(fmt.Sprintf("数学题型模板不存在：%s", code))
	}
	cfg, err := mathgen.ParseConfig(mat.Generator)
	if err != nil {
		return mathgen.Config{}, apperr.Internal(err)
	}
	return cfg, nil
}

// ---------------------------------------------------------------- 范围解析

// resolveKPs 按 range 参数解析出知识点 id 列表。
//
// 四个范围与 §4.6 的「范围」下拉一致：近期新学 / 错题本 / 已掌握 / 指定阶段。
// want 是希望拿到的数量（用于组装文案），limit 是查询上限 —— 取比 want 更多的量，
// 因为后续还要按素材类型过滤（同阶段的 kp 里可能混着不是汉字/单词的条目）。
func (s *Service) resolveKPs(ctx context.Context, p Params, childID uuid.UUID, want, limit int) ([]uuid.UUID, string, error) {
	rangeKind := p.GetEnum("range", []string{"recent", "wrong", "mastered", "stage"}, "recent")
	subject := p.GetEnum("subject", []string{"chinese", "english", "math"}, "chinese")
	stage := p.Get("stage")
	days := p.GetInt("days", 7)
	if limit <= 0 {
		limit = want
	}
	if limit <= 0 {
		limit = 20
	}

	switch rangeKind {
	case "wrong":
		if childID == uuid.Nil {
			return nil, "", apperr.BadRequest("按错题本取内容需要先选择孩子")
		}
		kps, err := s.repo.KPsWrongBook(ctx, childID, limit)
		if err != nil {
			return nil, "", apperr.Internal(fmt.Errorf("读取错题本失败: %w", err))
		}
		return kps, "错题本", nil

	case "mastered":
		if childID == uuid.Nil {
			return nil, "", apperr.BadRequest("按已掌握取内容需要先选择孩子")
		}
		kps, err := s.repo.KPsMastered(ctx, childID, 3, limit)
		if err != nil {
			return nil, "", apperr.Internal(fmt.Errorf("读取掌握记录失败: %w", err))
		}
		return kps, "已掌握", nil

	case "stage":
		kps, err := s.repo.KPsByStage(ctx, subject, stage, limit)
		if err != nil {
			return nil, "", apperr.Internal(fmt.Errorf("按阶段取知识点失败: %w", err))
		}
		label := "指定阶段"
		if stage != "" {
			label = "阶段 " + stage
		}
		return kps, label, nil

	default:
		if childID == uuid.Nil {
			return nil, "", apperr.BadRequest("按近期新学取内容需要先选择孩子")
		}
		since := startOfDaysAgo(s.now(), days)
		kps, err := s.repo.KPsRecent(ctx, childID, since, limit)
		if err != nil {
			return nil, "", apperr.Internal(fmt.Errorf("读取近期学习记录失败: %w", err))
		}
		return kps, fmt.Sprintf("近 %d 天新学", days), nil
	}
}

// startOfDaysAgo 返回 days 天前的本地零点（与报表按本地日切分的口径一致）。
func startOfDaysAgo(now time.Time, days int) time.Time {
	if days < 1 {
		days = 1
	}
	d := now.AddDate(0, 0, -(days - 1))
	return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, d.Location())
}
