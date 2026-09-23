package practice

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"kidstudy/internal/dbgen"
)

// ErrNotFound 由 service 转成 404。
var ErrNotFound = errors.New("会话或题目不存在")

// ErrAlreadyAnswered 表示这道题答过了，service 转成 409。
var ErrAlreadyAnswered = errors.New("这道题已经作答过了")

// Repository 只做会话与题目快照的读写，编排与判分规则全在 service。
type Repository struct {
	pool *pgxpool.Pool
	q    *dbgen.Queries
}

// NewRepository 用连接池构造。
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool, q: dbgen.New(pool)}
}

// WithTx 返回绑定到事务的副本。
func (r *Repository) WithTx(tx pgx.Tx) *Repository {
	return &Repository{pool: r.pool, q: dbgen.New(tx)}
}

// Begin 开一个事务。调用方负责 Rollback/Commit。
func (r *Repository) Begin(ctx context.Context) (pgx.Tx, error) { return r.pool.Begin(ctx) }

// ------------------------------------------------------------------ 会话

func (r *Repository) InsertSession(ctx context.Context, childID uuid.UUID, deviceType string) (Session, error) {
	row, err := r.q.InsertSession(ctx, dbgen.InsertSessionParams{ChildID: childID, DeviceType: deviceType})
	if err != nil {
		return Session{}, err
	}
	return sessionFrom(dbgen.GetSessionRow(row)), nil
}

func (r *Repository) GetSession(ctx context.Context, sessionID, childID uuid.UUID) (Session, error) {
	row, err := r.q.GetSession(ctx, dbgen.GetSessionParams{ID: sessionID, ChildID: childID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Session{}, ErrNotFound
		}
		return Session{}, err
	}
	return sessionFrom(dbgen.GetSessionRow(row)), nil
}

func (r *Repository) FinishSession(ctx context.Context, p dbgen.FinishSessionParams) (Session, error) {
	row, err := r.q.FinishSession(ctx, p)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Session{}, ErrNotFound
		}
		return Session{}, err
	}
	return sessionFrom(dbgen.GetSessionRow(row)), nil
}

func (r *Repository) MarkSessionConfirmed(ctx context.Context, sessionID, childID uuid.UUID, score *int, note, completedBy string, now time.Time) (Session, error) {
	row, err := r.q.ConfirmSession(ctx, dbgen.ConfirmSessionParams{
		ID:          sessionID,
		ChildID:     childID,
		ParentScore: int16Ptr(score),
		ParentNote:  pgtype.Text{String: note, Valid: note != ""},
		CompletedBy: pgtype.Text{String: completedBy, Valid: completedBy != ""},
		UpdatedAt:   ts(now),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Session{}, ErrNotFound
		}
		return Session{}, err
	}
	return sessionFrom(dbgen.GetSessionRow(row)), nil
}

// TodayUsedSeconds 统计今日已用秒数（未结束的会话按「到现在」计）。
func (r *Repository) TodayUsedSeconds(ctx context.Context, childID uuid.UUID, dayStart, now time.Time) (int, error) {
	sec, err := r.q.TodayUsedSeconds(ctx, dbgen.TodayUsedSecondsParams{
		ChildID: childID, NowTs: ts(now), DayStart: ts(dayStart),
	})
	if err != nil {
		return 0, err
	}
	return int(sec), nil
}

// ------------------------------------------------------------------ 题目

// InsertItem 写入一道题的快照（题面与答案分开存）。
func (r *Repository) InsertItem(ctx context.Context, sessionID, kpID uuid.UUID, seq int, q BuiltQuestion) (Item, error) {
	snapshot, err := json.Marshal(q.Snapshot)
	if err != nil {
		return Item{}, err
	}
	key, err := json.Marshal(q.Key)
	if err != nil {
		return Item{}, err
	}
	row, err := r.q.InsertSessionItem(ctx, dbgen.InsertSessionItemParams{
		SessionID:        sessionID,
		Seq:              int32(seq),
		KpID:             kpID,
		SubjectCode:      q.SubjectCode,
		StageCode:        pgtype.Text{String: q.StageCode, Valid: q.StageCode != ""},
		QuestionType:     q.QuestionType,
		Difficulty:       int16(q.Difficulty),
		QuestionSnapshot: snapshot,
		AnswerKey:        key,
	})
	if err != nil {
		return Item{}, err
	}
	return itemFrom(row)
}

func (r *Repository) GetItem(ctx context.Context, itemID, sessionID uuid.UUID) (Item, error) {
	row, err := r.q.GetSessionItem(ctx, dbgen.GetSessionItemParams{ID: itemID, SessionID: sessionID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Item{}, ErrNotFound
		}
		return Item{}, err
	}
	return itemFrom(row)
}

func (r *Repository) ListItems(ctx context.Context, sessionID uuid.UUID) ([]Item, error) {
	rows, err := r.q.ListSessionItems(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	out := make([]Item, 0, len(rows))
	for _, row := range rows {
		it, err := itemFrom(row)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, nil
}

// UpdateItemAnswer 只认 pending → 重复提交同一题不会二次计分。
func (r *Repository) UpdateItemAnswer(ctx context.Context, itemID, sessionID uuid.UUID, state string, correct *bool, usedHint bool, elapsedMS int, now time.Time) error {
	var isCorrect pgtype.Bool
	if correct != nil {
		isCorrect = pgtype.Bool{Bool: *correct, Valid: true}
	}
	return r.q.UpdateItemAnswer(ctx, dbgen.UpdateItemAnswerParams{
		ID:        itemID,
		SessionID: sessionID,
		State:     state,
		IsCorrect: isCorrect,
		UsedHint:  usedHint,
		ElapsedMs: pgtype.Int4{Int32: int32(elapsedMS), Valid: elapsedMS > 0},
		NowTs:     ts(now),
	})
}

// SessionCounts 统计会话内各状态题目数。
func (r *Repository) SessionCounts(ctx context.Context, sessionID uuid.UUID) (total, answered, correct, skipped, hinted int, err error) {
	row, err := r.q.SessionCounts(ctx, sessionID)
	if err != nil {
		return 0, 0, 0, 0, 0, err
	}
	return int(row.Total), int(row.Answered), int(row.Correct), int(row.Skipped), int(row.UsedHint), nil
}

// ------------------------------------------------------------------ 流水与汇总

func (r *Repository) InsertAnswerLog(ctx context.Context, childID, sessionID, itemID, kpID uuid.UUID, subjectCode, questionType string, correct bool, usedHint bool, elapsedMS int) error {
	return r.q.InsertAnswerLog(ctx, dbgen.InsertAnswerLogParams{
		ChildID:      childID,
		SessionID:    uuidPtr(sessionID),
		ItemID:       uuidPtr(itemID),
		KpID:         kpID,
		SubjectCode:  subjectCode,
		QuestionType: questionType,
		IsCorrect:    correct,
		UsedHint:     usedHint,
		ElapsedMs:    int32(elapsedMS),
	})
}

func (r *Repository) UpsertDailyStat(ctx context.Context, childID uuid.UUID, day time.Time, subjectCode string, durationSec, questionCount, correctCount, newMastered, starCount, actualNew int) error {
	_, err := r.q.UpsertDailyStat(ctx, dbgen.UpsertDailyStatParams{
		ChildID:       childID,
		StatDate:      pgtype.Date{Time: day, Valid: true},
		SubjectCode:   subjectCode,
		DurationSec:   int32(durationSec),
		QuestionCount: int32(questionCount),
		CorrectCount:  int32(correctCount),
		NewMastered:   int32(newMastered),
		StarCount:     int32(starCount),
		ActualNew:     int32(actualNew),
		RepeatCount:   0,
	})
	return err
}

// CountNewLearnedToday 数一下这批 kp 里有多少是今天刚第一次学的。
func (r *Repository) CountNewLearnedToday(ctx context.Context, childID uuid.UUID, kpIDs []uuid.UUID, dayStart time.Time) (int, error) {
	n, err := r.q.CountNewLearnedToday(ctx, dbgen.CountNewLearnedTodayParams{
		ChildID: childID, KpIds: kpIDs, DayStart: ts(dayStart),
	})
	if err != nil {
		return 0, err
	}
	return int(n), nil
}

// ------------------------------------------------------------------ 编排素材

// ListNewKPsByPlan 按标准节奏基准线取未学知识点。
func (r *Repository) ListNewKPsByPlan(ctx context.Context, childID uuid.UUID, subject, kind string, limit int) ([]PlanItem, error) {
	rows, err := r.q.ListNewKPsByPlan(ctx, dbgen.ListNewKPsByPlanParams{
		ChildID: childID, SubjectCode: subject, Kind: kind, Lim: int32(limit),
	})
	if err != nil {
		return nil, err
	}
	out := make([]PlanItem, 0, len(rows))
	for _, row := range rows {
		out = append(out, PlanItem{
			KPID: row.ID, SubjectCode: row.SubjectCode, Kind: row.Kind, Code: row.Code,
			Name: row.Name, Difficulty: int(row.Difficulty), PlannedDay: int(row.PlannedDayIndex),
		})
	}
	return out, nil
}

// ListNewKPsByStage 基准线缺失时按阶段顺序兜底。
func (r *Repository) ListNewKPsByStage(ctx context.Context, childID uuid.UUID, subject, stageCode, kind string, limit int) ([]PlanItem, error) {
	rows, err := r.q.ListNewKPsByStage(ctx, dbgen.ListNewKPsByStageParams{
		ChildID: childID, SubjectCode: subject, StageCode: stageCode, Kind: kind, Lim: int32(limit),
	})
	if err != nil {
		return nil, err
	}
	out := make([]PlanItem, 0, len(rows))
	for _, row := range rows {
		out = append(out, PlanItem{
			KPID: row.ID, SubjectCode: row.SubjectCode, Kind: row.Kind, Code: row.Code,
			Name: row.Name, Difficulty: int(row.Difficulty),
		})
	}
	return out, nil
}

// ListKPs 批量取知识点基本信息。
func (r *Repository) ListKPs(ctx context.Context, kpIDs []uuid.UUID) ([]PlanItem, error) {
	if len(kpIDs) == 0 {
		return nil, nil
	}
	rows, err := r.q.ListKPsByIDs(ctx, kpIDs)
	if err != nil {
		return nil, err
	}
	out := make([]PlanItem, 0, len(rows))
	for _, row := range rows {
		out = append(out, PlanItem{
			KPID: row.ID, SubjectCode: row.SubjectCode, Kind: row.Kind, Code: row.Code,
			Name: row.Name, Difficulty: int(row.Difficulty),
		})
	}
	return out, nil
}

// ListPendingAssignments 取家长指派的专项知识点。
func (r *Repository) ListPendingAssignments(ctx context.Context, childID uuid.UUID, limit int) ([]PlanItem, error) {
	rows, err := r.q.ListPendingAssignments(ctx, dbgen.ListPendingAssignmentsParams{
		ChildID: childID, Lim: int32(limit),
	})
	if err != nil {
		return nil, err
	}
	out := make([]PlanItem, 0, len(rows))
	for _, row := range rows {
		out = append(out, PlanItem{
			KPID: row.KpID, SubjectCode: row.SubjectCode, Kind: row.Kind, Code: row.Code,
			Name: row.Name, Reason: ReasonAssigned,
		})
	}
	return out, nil
}

func (r *Repository) MarkAssignmentsDone(ctx context.Context, childID uuid.UUID, kpIDs []uuid.UUID) error {
	if len(kpIDs) == 0 {
		return nil
	}
	return r.q.MarkAssignmentDone(ctx, dbgen.MarkAssignmentDoneParams{ChildID: childID, KpIds: kpIDs})
}

// UpsertAssignment 把知识点加入孩子的专项指派。数据库侧按 (child_id, kp_id) 唯一，
// 已存在的会被重置回 pending（重复指派同一个 kp 不会堆重复行）。
func (r *Repository) UpsertAssignment(ctx context.Context, childID, kpID, parentID uuid.UUID, reason string) error {
	_, err := r.q.UpsertAssignment(ctx, dbgen.UpsertAssignmentParams{
		ChildID: childID, KpID: kpID, ParentID: parentID, Reason: reason,
	})
	return err
}

// GetParentSettings 取家长控制项，查不到返回零值（service 侧兜默认值）。
func (r *Repository) GetParentSettings(ctx context.Context, parentID uuid.UUID) (ParentSettings, error) {
	row, err := r.q.GetParentSettings(ctx, parentID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ParentSettings{}, nil
		}
		return ParentSettings{}, err
	}
	settings := ParentSettings{DailyLimitMin: int(row.DailyLimitMin), PaceMode: row.PaceMode}
	if len(row.SubjectSwitches) > 0 {
		var m map[string]bool
		if err := json.Unmarshal(row.SubjectSwitches, &m); err == nil {
			settings.SubjectSwitches = m
		}
	}
	settings.RequireParentConfirm = row.RequireParentConfirm
	return settings, nil
}

// ------------------------------------------------------------------ 转换

// sessionFrom 把生成行转成领域会话。三种行结构字段完全一致，
// Go 1.8 起结构体转换忽略 tag，所以直接转即可，不用写三份。
func sessionFrom(row dbgen.GetSessionRow) Session {
	return Session{
		ID: row.ID, ChildID: r0(row).ChildID, DeviceType: row.DeviceType,
		StartedAt: tstz(row.StartedAt), EndedAt: tstzPtr(row.EndedAt),
		DurationSec: int(row.DurationSec), QuestionCount: int(row.QuestionCount),
		AnsweredCount: int(row.AnsweredCount), CorrectCount: int(row.CorrectCount),
		SkippedCount: int(row.SkippedCount), StarCount: int(row.StarCount),
		CompletedBy: row.CompletedBy.String, ParentScore: int16PtrFrom(row.ParentScore),
		ParentNote: row.ParentNote.String, Status: row.Status,
	}
}

func r0(row dbgen.GetSessionRow) dbgen.GetSessionRow { return row }

func itemFrom(row dbgen.SessionItem) (Item, error) {
	var snapshot Question
	if len(row.QuestionSnapshot) > 0 {
		if err := json.Unmarshal(row.QuestionSnapshot, &snapshot); err != nil {
			return Item{}, err
		}
	}
	var key AnswerKey
	if len(row.AnswerKey) > 0 {
		if err := json.Unmarshal(row.AnswerKey, &key); err != nil {
			return Item{}, err
		}
	}
	return Item{
		ID: row.ID, SessionID: row.SessionID, Seq: int(row.Seq), KPID: row.KpID,
		SubjectCode: row.SubjectCode, StageCode: row.StageCode.String,
		QuestionType: row.QuestionType, Difficulty: int(row.Difficulty),
		Snapshot: snapshot, Key: key, State: row.State,
		IsCorrect: boolPtr(row.IsCorrect), UsedHint: row.UsedHint,
		ElapsedMS: int(row.ElapsedMs.Int32), AnsweredAt: tstzPtr(row.AnsweredAt),
	}, nil
}

func ts(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: !t.IsZero()}
}

func tstz(v pgtype.Timestamptz) time.Time {
	if !v.Valid {
		return time.Time{}
	}
	return v.Time
}

func tstzPtr(v pgtype.Timestamptz) *time.Time {
	if !v.Valid {
		return nil
	}
	t := v.Time
	return &t
}

func boolPtr(v pgtype.Bool) *bool {
	if !v.Valid {
		return nil
	}
	b := v.Bool
	return &b
}

func int16Ptr(v *int) pgtype.Int2 {
	if v == nil {
		return pgtype.Int2{}
	}
	return pgtype.Int2{Int16: int16(*v), Valid: true}
}

func int16PtrFrom(v pgtype.Int2) *int {
	if !v.Valid {
		return nil
	}
	n := int(v.Int16)
	return &n
}

func uuidPtr(v uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: v, Valid: true}
}
