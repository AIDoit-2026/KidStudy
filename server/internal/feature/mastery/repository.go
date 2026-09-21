package mastery

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"kidstudy/internal/dbgen"
)

// ErrNotFound 由 service 转成 404。
var ErrNotFound = errors.New("记录不存在")

// Repository 只做掌握度与错题本的读写，不做任何调度决策。
type Repository struct {
	q *dbgen.Queries
}

// NewRepository 用任意 DBTX（*pgxpool.Pool 或 pgx.Tx）构造。
func NewRepository(db dbgen.DBTX) *Repository { return &Repository{q: dbgen.New(db)} }

// WithTx 返回一个绑定到事务的副本，让上层把「掌握度 + 错题本 + 会话」放进同一个事务。
func (r *Repository) WithTx(tx pgx.Tx) *Repository { return &Repository{q: dbgen.New(tx)} }

// ------------------------------------------------------------------ 掌握度

// Get 取一条掌握度记录，不存在返回 false（不是错误）。
func (r *Repository) Get(ctx context.Context, childID, kpID uuid.UUID) (Record, bool, error) {
	row, err := r.q.GetMastery(ctx, dbgen.GetMasteryParams{ChildID: childID, KpID: kpID})
	if err != nil {
		if isNoRows(err) {
			return Record{}, false, nil
		}
		return Record{}, false, err
	}
	return recordFrom(row), true, nil
}

// Upsert 写回掌握度记录。
func (r *Repository) Upsert(ctx context.Context, childID uuid.UUID, rec Record) (Record, error) {
	row, err := r.q.UpsertMastery(ctx, dbgen.UpsertMasteryParams{
		ChildID:          childID,
		KpID:             rec.KPID,
		Level:            int16(rec.Level),
		Ease:             rec.Ease,
		IntervalHours:    rec.IntervalHours,
		NextReviewAt:     ts(rec.NextReviewAt),
		Difficulty:       int16(rec.Difficulty),
		CorrectCount:     int32(rec.CorrectCount),
		WrongCount:       int32(rec.WrongCount),
		Streak:           int32(rec.Streak),
		WrongStreak:      int32(rec.WrongStreak),
		LastResult:       text(rec.LastResult),
		FirstLearnedAt:   tsPtr(rec.FirstLearnedAt),
		MasteredAt:       tsPtr(rec.MasteredAt),
		Attempts:         int32(rec.Attempts),
		AttemptsToMaster: int32Ptr(rec.AttemptsToMaster),
		RepeatDays:       int32(rec.RepeatDays),
		LastReviewDate:   datePtr(rec.LastReviewDate),
	})
	if err != nil {
		return Record{}, err
	}
	return Record{
		KPID:             row.KpID,
		Level:            int(row.Level),
		Ease:             row.Ease,
		IntervalHours:    row.IntervalHours,
		NextReviewAt:     tstz(row.NextReviewAt),
		Difficulty:       int(row.Difficulty),
		CorrectCount:     int(row.CorrectCount),
		WrongCount:       int(row.WrongCount),
		Streak:           int(row.Streak),
		WrongStreak:      int(row.WrongStreak),
		LastResult:       row.LastResult.String,
		FirstLearnedAt:   tstzPtr(row.FirstLearnedAt),
		MasteredAt:       tstzPtr(row.MasteredAt),
		Attempts:         int(row.Attempts),
		AttemptsToMaster: int32PtrFrom(row.AttemptsToMaster),
		RepeatDays:       int(row.RepeatDays),
		LastReviewDate:   dateToTimePtr(row.LastReviewDate),
	}, nil
}

// Delete 删除一条掌握度记录（家长重置用）。
func (r *Repository) Delete(ctx context.Context, childID, kpID uuid.UUID) error {
	return r.q.DeleteMastery(ctx, dbgen.DeleteMasteryParams{ChildID: childID, KpID: kpID})
}

// ListDue 取到期复习条目，逾期越久越靠前。
func (r *Repository) ListDue(ctx context.Context, childID uuid.UUID, subject string, now time.Time, limit int) ([]ReviewItem, error) {
	rows, err := r.q.ListDueReviews(ctx, dbgen.ListDueReviewsParams{
		ChildID:     childID,
		NowTs:       ts(now),
		SubjectCode: subject,
		Lim:         int32(limit),
	})
	if err != nil {
		return nil, err
	}
	out := make([]ReviewItem, 0, len(rows))
	for _, row := range rows {
		due := tstz(row.NextReviewAt)
		out = append(out, ReviewItem{
			KPID:         row.KpID,
			SubjectCode:  row.SubjectCode,
			Kind:         row.Kind,
			Code:         row.Code,
			Name:         row.Name,
			Level:        int(row.Level),
			Difficulty:   int(row.Difficulty),
			NextReviewAt: due.UTC().Format(time.RFC3339),
			OverdueHours: now.Sub(due).Hours(),
		})
	}
	return out, nil
}

// CountDue 数一下到期条目，用于复习积压保护。
func (r *Repository) CountDue(ctx context.Context, childID uuid.UUID, subject string, now time.Time) (int64, error) {
	return r.q.CountDueReviews(ctx, dbgen.CountDueReviewsParams{
		ChildID: childID, NowTs: ts(now), SubjectCode: subject,
	})
}

// RecentAnswers 取最近 n 次作答，供难度自适应统计。
func (r *Repository) RecentAnswers(ctx context.Context, childID, kpID uuid.UUID, n int) ([]bool, []int, error) {
	rows, err := r.q.RecentAnswers(ctx, dbgen.RecentAnswersParams{ChildID: childID, KpID: kpID, Limit: int32(n)})
	if err != nil {
		return nil, nil, err
	}
	correct := make([]bool, 0, len(rows))
	elapsed := make([]int, 0, len(rows))
	for _, row := range rows {
		correct = append(correct, row.IsCorrect)
		elapsed = append(elapsed, int(row.ElapsedMs))
	}
	return correct, elapsed, nil
}

// ------------------------------------------------------------------ 错题本

// GetWrongEntry 取一条错题记录，不存在返回 false。
func (r *Repository) GetWrongEntry(ctx context.Context, childID, kpID uuid.UUID) (WrongEntry, bool, error) {
	row, err := r.q.GetWrongEntry(ctx, dbgen.GetWrongEntryParams{ChildID: childID, KpID: kpID})
	if err != nil {
		if isNoRows(err) {
			return WrongEntry{}, false, nil
		}
		return WrongEntry{}, false, err
	}
	return WrongEntry{
		ID:                 row.ID,
		KPID:               row.KpID,
		WrongCount:         int(row.WrongCount),
		ConsecutiveCorrect: int(row.ConsecutiveCorrect),
		AddedAt:            tstz(row.AddedAt).UTC().Format(time.RFC3339),
		ClearedAt:          timePtrStr(tstzPtr(row.ClearedAt)),
	}, true, nil
}

// UpsertWrongEntry 写入或更新错题条目。clearedAt 非 nil 表示移出。
func (r *Repository) UpsertWrongEntry(ctx context.Context, childID, kpID uuid.UUID, clearedAt *time.Time, consecutive int) error {
	_, err := r.q.UpsertWrongEntry(ctx, dbgen.UpsertWrongEntryParams{
		ChildID:            childID,
		KpID:               kpID,
		ClearedAt:          tsPtr(clearedAt),
		ConsecutiveCorrect: int32(consecutive),
		WrongCount:         1,
	})
	return err
}

// ListWrongBook 分页取错题本。
func (r *Repository) ListWrongBook(ctx context.Context, childID uuid.UUID, openOnly bool, offset, limit int) ([]WrongEntry, int64, error) {
	rows, err := r.q.ListWrongBook(ctx, dbgen.ListWrongBookParams{
		ChildID:  childID,
		OpenOnly: openOnly,
		Lim:      int32(limit),
		Off:      int32(offset),
	})
	if err != nil {
		return nil, 0, err
	}
	out := make([]WrongEntry, 0, len(rows))
	for _, row := range rows {
		out = append(out, WrongEntry{
			ID:                 row.ID,
			KPID:               row.KpID,
			SubjectCode:        row.SubjectCode,
			Code:               row.Code,
			Name:               row.Name,
			WrongCount:         int(row.WrongCount),
			ConsecutiveCorrect: int(row.ConsecutiveCorrect),
			AddedAt:            tstz(row.AddedAt).UTC().Format(time.RFC3339),
			ClearedAt:          timePtrStr(tstzPtr(row.ClearedAt)),
		})
	}
	total, err := r.q.CountWrongBook(ctx, dbgen.CountWrongBookParams{ChildID: childID, OpenOnly: openOnly})
	if err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

// DeleteWrongEntry 家长手动移除一条。
func (r *Repository) DeleteWrongEntry(ctx context.Context, id, childID uuid.UUID) error {
	return r.q.DeleteWrongEntry(ctx, dbgen.DeleteWrongEntryParams{ID: id, ChildID: childID})
}

// ------------------------------------------------------------------ 内部转换

func recordFrom(row dbgen.GetMasteryRow) Record {
	return Record{
		KPID:             row.KpID,
		Level:            int(row.Level),
		Ease:             row.Ease,
		IntervalHours:    row.IntervalHours,
		NextReviewAt:     tstz(row.NextReviewAt),
		Difficulty:       int(row.Difficulty),
		CorrectCount:     int(row.CorrectCount),
		WrongCount:       int(row.WrongCount),
		Streak:           int(row.Streak),
		WrongStreak:      int(row.WrongStreak),
		LastResult:       row.LastResult.String,
		FirstLearnedAt:   tstzPtr(row.FirstLearnedAt),
		MasteredAt:       tstzPtr(row.MasteredAt),
		Attempts:         int(row.Attempts),
		AttemptsToMaster: int32PtrFrom(row.AttemptsToMaster),
		RepeatDays:       int(row.RepeatDays),
		LastReviewDate:   dateToTimePtr(row.LastReviewDate),
	}
}

func isNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

func ts(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: !t.IsZero()}
}

func tsPtr(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *t, Valid: true}
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

func datePtr(t *time.Time) pgtype.Date {
	if t == nil {
		return pgtype.Date{}
	}
	return pgtype.Date{Time: *t, Valid: true}
}

func dateToTimePtr(d pgtype.Date) *time.Time {
	if !d.Valid {
		return nil
	}
	t := d.Time
	return &t
}

func text(s string) pgtype.Text {
	if s == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: s, Valid: true}
}

func int32Ptr(v *int) pgtype.Int4 {
	if v == nil {
		return pgtype.Int4{}
	}
	return pgtype.Int4{Int32: int32(*v), Valid: true}
}

func int32PtrFrom(v pgtype.Int4) *int {
	if !v.Valid {
		return nil
	}
	n := int(v.Int32)
	return &n
}

func timePtrStr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.UTC().Format(time.RFC3339)
	return &s
}
