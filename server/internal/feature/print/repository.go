package print

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"kidstudy/internal/dbgen"
)

// Repository 是打印任务与取数的数据访问层。
//
// 只做查询与持久化，不做决策：范围要不要限制、题量够不够、能不能补录，
// 都在 service 层判断。
type Repository struct {
	pool *pgxpool.Pool
	q    *dbgen.Queries
}

// NewRepository 用任意 DBTX（*pgxpool.Pool 或 pgx.Tx）构造。
func NewRepository(db dbgen.DBTX) *Repository {
	r := &Repository{q: dbgen.New(db)}
	if pool, ok := db.(*pgxpool.Pool); ok {
		r.pool = pool
	}
	return r
}

// WithTx 返回绑定到事务的副本，让「标记完成 + 建补录会话 + 写掌握度」在同一事务里。
func (r *Repository) WithTx(tx pgx.Tx) *Repository {
	return &Repository{pool: r.pool, q: dbgen.New(tx)}
}

// Begin 开一个事务，调用方负责 Commit/Rollback。
func (r *Repository) Begin(ctx context.Context) (pgx.Tx, error) {
	if r.pool == nil {
		return nil, errors.New("仓储未绑定连接池，无法开启事务")
	}
	return r.pool.Begin(ctx)
}

func isNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

// ---------------------------------------------------------------- 打印任务

// CreateJob 落一条打印任务，payload 为渲染数据快照。
func (r *Repository) CreateJob(ctx context.Context, parentID, childID uuid.UUID,
	templateCode string, params, payload []byte, pageCount int) (dbgen.PrintJob, error) {
	return r.q.CreatePrintJob(ctx, dbgen.CreatePrintJobParams{
		ParentID:     parentID,
		ChildID:      childID,
		TemplateCode: templateCode,
		Params:       params,
		Payload:      payload,
		PageCount:    int32(pageCount),
	})
}

// GetJob 按 id + parent_id 取任务；不属于该家长时返回 notFound=true。
func (r *Repository) GetJob(ctx context.Context, parentID, id uuid.UUID) (dbgen.PrintJob, bool, error) {
	row, err := r.q.GetPrintJob(ctx, dbgen.GetPrintJobParams{ID: id, ParentID: parentID})
	if err != nil {
		if isNoRows(err) {
			return dbgen.PrintJob{}, true, nil
		}
		return dbgen.PrintJob{}, false, err
	}
	return row, false, nil
}

// ListJobs 列某家长的打印任务；childID 为零值时不过滤孩子。
func (r *Repository) ListJobs(ctx context.Context, parentID, childID uuid.UUID, limit, offset int) ([]dbgen.PrintJob, error) {
	return r.q.ListPrintJobs(ctx, dbgen.ListPrintJobsParams{
		ParentID: parentID,
		ChildID:  childID,
		Lim:      int32(limit),
		Off:      int32(offset),
	})
}

// CountJobs 统计条数（与 ListJobs 同一过滤条件）。
func (r *Repository) CountJobs(ctx context.Context, parentID, childID uuid.UUID) (int64, error) {
	return r.q.CountPrintJobs(ctx, dbgen.CountPrintJobsParams{ParentID: parentID, ChildID: childID})
}

// MarkQueued 把任务排进渲染队列（幂等：ready 的任务不会被退回）。
func (r *Repository) MarkQueued(ctx context.Context, parentID, id uuid.UUID) (dbgen.MarkPrintJobQueuedRow, bool, error) {
	row, err := r.q.MarkPrintJobQueued(ctx, dbgen.MarkPrintJobQueuedParams{ID: id, ParentID: parentID})
	if err != nil {
		if isNoRows(err) {
			return dbgen.MarkPrintJobQueuedRow{}, true, nil
		}
		return dbgen.MarkPrintJobQueuedRow{}, false, err
	}
	return row, false, nil
}

// Claim 拉取待渲染任务并置为 rendering（FOR UPDATE SKIP LOCKED，多实例安全）。
func (r *Repository) Claim(ctx context.Context, limit int) ([]dbgen.ClaimPrintJobsRow, error) {
	return r.q.ClaimPrintJobs(ctx, int32(limit))
}

// MarkReady 标记 PDF 已生成。
func (r *Repository) MarkReady(ctx context.Context, id uuid.UUID, pdfPath string, pageCount int) error {
	_, err := r.q.MarkPrintJobReady(ctx, dbgen.MarkPrintJobReadyParams{
		ID:        id,
		PdfPath:   pgText(pdfPath),
		PageCount: int32(pageCount),
	})
	return err
}

// MarkFailed 记录渲染失败原因。
func (r *Repository) MarkFailed(ctx context.Context, id uuid.UUID, msg string) error {
	if len(msg) > 500 {
		msg = msg[:500]
	}
	return r.q.MarkPrintJobFailed(ctx, dbgen.MarkPrintJobFailedParams{ID: id, ErrorMessage: msg})
}

// RequeueStale 把卡在 rendering 超过 stale 的任务退回队列（进程崩溃遗留）。
func (r *Repository) RequeueStale(ctx context.Context, stale time.Duration) (int64, error) {
	return 0, r.q.RequeueStaleRendering(ctx, stale.Seconds())
}

// MarkDone 标记纸质已做完；已标过时返回 notMarked=true（用于幂等）。
func (r *Repository) MarkDone(ctx context.Context, parentID, id uuid.UUID) (dbgen.MarkPrintJobDoneRow, bool, error) {
	row, err := r.q.MarkPrintJobDone(ctx, dbgen.MarkPrintJobDoneParams{ID: id, ParentID: parentID})
	if err != nil {
		if isNoRows(err) {
			return dbgen.MarkPrintJobDoneRow{}, true, nil
		}
		return dbgen.MarkPrintJobDoneRow{}, false, err
	}
	return row, false, nil
}

// InsertSession 建一条补录学习会话（completed_by=parent，回指 print_job）。
func (r *Repository) InsertSession(ctx context.Context, childID uuid.UUID, jobID uuid.UUID,
	questionCount, answeredCount, correctCount int, note string) (uuid.UUID, error) {
	return r.q.InsertPrintSession(ctx, dbgen.InsertPrintSessionParams{
		ChildID:       childID,
		QuestionCount: int32(questionCount),
		AnsweredCount: int32(answeredCount),
		CorrectCount:  int32(correctCount),
		ParentNote:    pgText(note),
		PrintJobID:    pgUUID(jobID),
	})
}

// MarkAssignmentsDone 把 payload 里涉及的知识点从专项指派里标掉。
func (r *Repository) MarkAssignmentsDone(ctx context.Context, childID uuid.UUID, kpIDs []uuid.UUID) error {
	if len(kpIDs) == 0 {
		return nil
	}
	return r.q.MarkAssignmentDone(ctx, dbgen.MarkAssignmentDoneParams{ChildID: childID, KpIds: kpIDs})
}

// ExpiredPDFs 列出超过保留期、仍带 PDF 的任务。
func (r *Repository) ExpiredPDFs(ctx context.Context, before time.Time, limit int) ([]dbgen.ListExpiredPrintJobsRow, error) {
	return r.q.ListExpiredPrintJobs(ctx, dbgen.ListExpiredPrintJobsParams{Before: pgTS(before), Lim: int32(limit)})
}

// ClearPDF 清掉任务的 PDF 引用（文件由调用方删除）。
func (r *Repository) ClearPDF(ctx context.Context, id uuid.UUID) error {
	return r.q.ClearPrintJobPDF(ctx, id)
}

// ---------------------------------------------------------------- 取数

// Child 是打印用到的孩子基本信息。
type Child struct {
	ID        uuid.UUID
	Nickname  string
	StageCode string
}

// ChildBasic 取孩子基本信息；不属于该家长或已归档时返回 notFound=true。
func (r *Repository) ChildBasic(ctx context.Context, parentID, childID uuid.UUID) (Child, bool, error) {
	row, err := r.q.PrintChildBasic(ctx, dbgen.PrintChildBasicParams{ID: childID, ParentID: parentID})
	if err != nil {
		if isNoRows(err) {
			return Child{}, true, nil
		}
		return Child{}, false, err
	}
	return Child{ID: row.ID, Nickname: row.Nickname, StageCode: textOf(row.StageCode)}, false, nil
}

// KPsByStage 按学科 + 阶段取知识点 id（stage 为空表示不限阶段）。
func (r *Repository) KPsByStage(ctx context.Context, subject, stage string, limit int) ([]uuid.UUID, error) {
	rows, err := r.q.PrintKPsByStage(ctx, dbgen.PrintKPsByStageParams{
		SubjectCode: subject,
		StageCode:   stage,
		Lim:         int32(limit),
	})
	if err != nil {
		return nil, err
	}
	out := make([]uuid.UUID, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.ID)
	}
	return out, nil
}

// KPsRecent 取近期新学的知识点。
func (r *Repository) KPsRecent(ctx context.Context, childID uuid.UUID, since time.Time, limit int) ([]uuid.UUID, error) {
	return r.q.PrintKPsRecent(ctx, dbgen.PrintKPsRecentParams{
		ChildID: childID,
		Since:   pgTS(since),
		Lim:     int32(limit),
	})
}

// KPsWrongBook 取错题本里未移出的知识点。
func (r *Repository) KPsWrongBook(ctx context.Context, childID uuid.UUID, limit int) ([]uuid.UUID, error) {
	return r.q.PrintKPsWrongBook(ctx, dbgen.PrintKPsWrongBookParams{ChildID: childID, Lim: int32(limit)})
}

// KPsMastered 取已掌握的知识点。
func (r *Repository) KPsMastered(ctx context.Context, childID uuid.UUID, minLevel, limit int) ([]uuid.UUID, error) {
	return r.q.PrintKPsMastered(ctx, dbgen.PrintKPsMasteredParams{
		ChildID:  childID,
		MinLevel: int16(minLevel),
		Lim:      int32(limit),
	})
}

// MathKPByTemplate 取某个数学题型档对应的知识点 id；没有则返回 found=false。
func (r *Repository) MathKPByTemplate(ctx context.Context, templateCode string) (uuid.UUID, bool, error) {
	id, err := r.q.PrintMathKPByTemplate(ctx, templateCode)
	if err != nil {
		if isNoRows(err) {
			return uuid.Nil, false, nil
		}
		return uuid.Nil, false, err
	}
	return id, true, nil
}

// Story 是故事小册子需要的正文与互动题。
type Story struct {
	ID         uuid.UUID
	Title      string
	Lang       string
	LevelCode  string
	BodyMD     string
	Questions  []byte
	Discussion []byte
	CharCount  int
}

// GetStory 按 id 取一篇可打印的故事。
func (r *Repository) GetStory(ctx context.Context, id uuid.UUID) (Story, bool, error) {
	row, err := r.q.PrintGetStory(ctx, id)
	if err != nil {
		if isNoRows(err) {
			return Story{}, true, nil
		}
		return Story{}, false, err
	}
	return Story{
		ID: row.ID, Title: row.Title, Lang: row.Lang,
		LevelCode: textOf(row.LevelCode), BodyMD: row.BodyMd,
		Questions: row.Questions, Discussion: row.Discussion, CharCount: int(row.CharCount),
	}, false, nil
}

// PickStory 按级别挑一篇最短的已发布故事；级别为空表示不限。
func (r *Repository) PickStory(ctx context.Context, stageCode string) (Story, bool, error) {
	row, err := r.q.PrintPickStory(ctx, stageCode)
	if err != nil {
		if isNoRows(err) {
			return Story{}, true, nil
		}
		return Story{}, false, err
	}
	return Story{
		ID: row.ID, Title: row.Title, Lang: row.Lang,
		LevelCode: textOf(row.LevelCode), BodyMD: row.BodyMd,
		Questions: row.Questions, Discussion: row.Discussion, CharCount: int(row.CharCount),
	}, false, nil
}

// ---------------------------------------------------------------- pgtype 转换

// 可空列在生成代码里是 pgtype.*，这一层统一收口，业务代码只见 Go 原生类型。

func pgText(s string) pgtype.Text {
	if s == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: s, Valid: true}
}

func pgUUID(id uuid.UUID) pgtype.UUID {
	if id == uuid.Nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: id, Valid: true}
}

func pgTS(t time.Time) pgtype.Timestamptz {
	if t.IsZero() {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: t, Valid: true}
}

func textOf(v pgtype.Text) string {
	if !v.Valid {
		return ""
	}
	return v.String
}
