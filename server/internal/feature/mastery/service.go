package mastery

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// 难度自适应的阈值（§4.2）：连 3 次正确率不足 60% 降档；连 5 次全对且够快则升档。
const (
	AdaptWindowShort    = 3
	AdaptWindowLong     = 5
	AdaptFailRatio      = 0.6
	FastAnswerThreshold = 8000 // 毫秒
	MinDifficulty       = 1
	MaxDifficulty       = 5

	DefaultReviewLimit = 20
	MaxReviewLimit     = 100
)

// Service 承载掌握度状态机与错题本规则。不 import net/http，也不感知 JSON。
type Service struct {
	repo *Repository
	log  *slog.Logger
}

// NewService 构造掌握度服务。
func NewService(repo *Repository, log *slog.Logger) *Service {
	return &Service{repo: repo, log: log}
}

// WithTx 返回绑定到事务的服务副本，供 practice 把整次作答放进同一个事务。
func (s *Service) WithTx(tx pgx.Tx) *Service {
	return &Service{repo: s.repo.WithTx(tx), log: s.log}
}

// ApplyResult 是简化 SM-2 的核心：一次作答 → 更新掌握度 + 错题本 + 难度档。
//
// 调用方必须先把 answer_logs 写进去（同一事务内），难度自适应要读最近 N 次作答。
func (s *Service) ApplyResult(ctx context.Context, childID, kpID uuid.UUID, correct, usedHint bool, elapsedMS int) (Result, error) {
	now := time.Now()

	rec, found, err := s.repo.Get(ctx, childID, kpID)
	if err != nil {
		return Result{}, err
	}
	if !found {
		rec = NewRecord(now)
		rec.KPID = kpID
		rec.FirstLearnedAt = &now
	}

	rec.Attempts++
	// repeat_days 按「学习日」去重：同一天反复练同一个 kp 只算一天
	if rec.LastReviewDate == nil || !sameDay(*rec.LastReviewDate, now) {
		rec.RepeatDays++
		rec.LastReviewDate = &now
	}

	mastered := false
	if correct {
		rec.CorrectCount++
		rec.Streak++
		rec.WrongStreak = 0
		rec.LastResult = "correct"
		// 用提示也算对，但因子小幅下调 —— 否则「全都靠提示」也能把间隔拉长
		if usedHint {
			rec.Ease = math.Max(MinEase, rec.Ease-EasePenaltyHint)
		}
		if rec.Streak >= 2 && rec.Level < MaxLevel {
			rec.Level++
			rec.Streak = 0
		}
		rec.IntervalHours = nextIntervalHours(rec)
	} else {
		rec.WrongCount++
		rec.WrongStreak++
		rec.Streak = 0
		rec.LastResult = "wrong"
		rec.Ease = math.Max(MinEase, rec.Ease-EasePenaltyWrong)
		rec.Level = max(MinLevel, rec.Level-1)
		rec.IntervalHours = WrongIntervalHours
	}
	if rec.Level >= MasteredLevel && rec.MasteredAt == nil {
		rec.MasteredAt = &now
		attempts := rec.Attempts
		rec.AttemptsToMaster = &attempts
		mastered = true
	}

	// 难度自适应：先按最近作答算，再落库
	delta := 0
	if newLevel, changed, err := s.adaptDifficulty(ctx, childID, kpID, rec.Difficulty); err != nil {
		// 自适应失败不该让整次作答失败，记日志后沿用原档位
		s.log.Warn("难度自适应失败，沿用原档位", "child_id", childID, "kp_id", kpID, "error", err)
	} else {
		rec.Difficulty, delta = newLevel, changed
	}
	// 复习时刻按「答对用阶梯间隔、答错固定 10 分钟」算，展示用的分钟数再从实际时长反推。
	// 不用 interval_hours * 60 反算：0.17 小时其实是 10.2 分钟，取整后会显示成 11 分钟。
	var until time.Duration
	if correct {
		until = time.Duration(rec.IntervalHours * float64(time.Hour))
	} else {
		until = WrongReappearAfter
	}
	rec.NextReviewAt = now.Add(until)

	if _, err := s.repo.Upsert(ctx, childID, rec); err != nil {
		return Result{}, err
	}

	entered, cleared, err := s.applyWrongBook(ctx, childID, kpID, correct, now)
	if err != nil {
		return Result{}, err
	}

	return Result{
		KPID:                kpID,
		Level:               rec.Level,
		Ease:                round2(rec.Ease),
		IntervalHours:       round2(rec.IntervalHours),
		NextReviewAt:        rec.NextReviewAt.UTC().Format(time.RFC3339),
		NextReviewInMinutes: int(math.Ceil(until.Minutes())),
		Difficulty:          rec.Difficulty,
		Streak:              rec.Streak,
		WrongStreak:         rec.WrongStreak,
		Mastered:            mastered,
		EnteredWrongBook:    entered,
		ClearedWrongBook:    cleared,
		DifficultyChanged:   delta,
	}, nil
}

// nextIntervalHours 按 level 阶梯取间隔，再用 ease 做微调。
//
// 直接乘 ease 会让阶梯漂移（1h * 2.5 = 2.5h 而不是设计的 4h），所以这里以阶梯为基准，
// 只让 ease 在 ±30% 内调节：ease 高的人间隔更宽，但仍落在设计给出量级上。
func nextIntervalHours(rec Record) float64 {
	base, ok := LevelIntervalsHours[rec.Level]
	if !ok {
		base = MinIntervalHours
	}
	factor := rec.Ease / DefaultEase
	if factor < 0.7 {
		factor = 0.7
	}
	if factor > 1.3 {
		factor = 1.3
	}
	hours := base * factor
	if hours < MinIntervalHours {
		hours = MinIntervalHours
	}
	return math.Ceil(hours*100) / 100
}

// applyWrongBook 维护错题本：答错进本（连对清零），答对累计连对，够了就移出。
//
// 返回 (本次进本, 本次移出, error)。「本次移出」只在原本开着、这次关掉时才为真，
// 免得把「早就移出了」误报成这次的战果。
func (s *Service) applyWrongBook(ctx context.Context, childID, kpID uuid.UUID, correct bool, now time.Time) (bool, bool, error) {
	entry, has, err := s.repo.GetWrongEntry(ctx, childID, kpID)
	if err != nil {
		return false, false, err
	}
	wasOpen := has && entry.ClearedAt == nil

	if !correct {
		// 答错：进本或重新打开，连对计数清零
		return true, false, s.repo.UpsertWrongEntry(ctx, childID, kpID, nil, 0)
	}
	if !wasOpen {
		return false, false, nil
	}
	consecutive := entry.ConsecutiveCorrect + 1
	if consecutive >= WrongBookClearStreak {
		t := now
		return false, true, s.repo.UpsertWrongEntry(ctx, childID, kpID, &t, consecutive)
	}
	return false, false, s.repo.UpsertWrongEntry(ctx, childID, kpID, nil, consecutive)
}

// adaptDifficulty 返回（新档位, 变化量）。
func (s *Service) adaptDifficulty(ctx context.Context, childID, kpID uuid.UUID, current int) (int, int, error) {
	correct, elapsed, err := s.repo.RecentAnswers(ctx, childID, kpID, AdaptWindowLong)
	if err != nil {
		return current, 0, err
	}
	if len(correct) == 0 {
		return current, 0, nil
	}

	// 先看最近 3 次：正确率不足 60% 就降档
	short := correct
	if len(short) > AdaptWindowShort {
		short = short[:AdaptWindowShort]
	}
	hits := 0
	for _, c := range short {
		if c {
			hits++
		}
	}
	if len(short) >= AdaptWindowShort && float64(hits)/float64(len(short)) < AdaptFailRatio {
		if current > MinDifficulty {
			return current - 1, -1, nil
		}
		return current, 0, nil
	}

	// 再看最近 5 次：全对且平均用时够快才升档
	if len(correct) < AdaptWindowLong {
		return current, 0, nil
	}
	allCorrect := true
	sum := 0
	for i, c := range correct {
		if !c {
			allCorrect = false
			break
		}
		sum += elapsed[i]
	}
	if allCorrect && sum/len(correct) < FastAnswerThreshold && current < MaxDifficulty {
		return current + 1, 1, nil
	}
	return current, 0, nil
}

// ReviewQueue 取复习队列，并给出是否积压（积压时应暂停新学）。
func (s *Service) ReviewQueue(ctx context.Context, childID uuid.UUID, subject string, limit int) (ReviewQueue, error) {
	if limit <= 0 {
		limit = DefaultReviewLimit
	}
	if limit > MaxReviewLimit {
		limit = MaxReviewLimit
	}
	now := time.Now()
	items, err := s.repo.ListDue(ctx, childID, subject, now, limit)
	if err != nil {
		return ReviewQueue{}, err
	}
	due, err := s.repo.CountDue(ctx, childID, subject, now)
	if err != nil {
		return ReviewQueue{}, err
	}
	backlog := due > BacklogLimit
	return ReviewQueue{
		Items:         items,
		DueCount:      due,
		Backlog:       backlog,
		SuggestReview: backlog,
	}, nil
}

// WrongBook 分页取错题本。openOnly=true 只看未移出的。
func (s *Service) WrongBook(ctx context.Context, childID uuid.UUID, openOnly bool, offset, limit int) (WrongBookPage, error) {
	items, total, err := s.repo.ListWrongBook(ctx, childID, openOnly, offset, limit)
	if err != nil {
		return WrongBookPage{}, err
	}
	if items == nil {
		items = []WrongEntry{}
	}
	return WrongBookPage{Items: items, Total: total}, nil
}

// RemoveWrongEntry 家长手动移除一条错题。
func (s *Service) RemoveWrongEntry(ctx context.Context, childID, id uuid.UUID) error {
	return s.repo.DeleteWrongEntry(ctx, id, childID)
}

// Reset 重置单个知识点：清掌握度，同时把错题本里对应的条目一并清掉，
// 否则重置后它还会被「错题优先」顶到今日任务最前面。
func (s *Service) Reset(ctx context.Context, childID, kpID uuid.UUID) error {
	// 先确认知识点真的存在：否则「没学过」和「没这个知识点」都会静默成功，
	// 家长点了重置却毫无反应，最难排查。
	exists, err := s.repo.Exists(ctx, kpID)
	if err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	if err := s.repo.Delete(ctx, childID, kpID); err != nil {
		return fmt.Errorf("重置掌握度失败: %w", err)
	}
	entry, has, err := s.repo.GetWrongEntry(ctx, childID, kpID)
	if err != nil {
		return err
	}
	if !has {
		return nil
	}
	return s.repo.DeleteWrongEntry(ctx, entry.ID, childID)
}

// LowerDifficulty 把某知识点的难度手动降一档（下限 MinDifficulty），返回降档后的档位。
//
// 供报表建议「降一档」的一键动作调用（§4.7 规则 1），与 ApplyResult 里的自动降档同口径。
// 已在最低档时幂等返回不报错 —— 家长连点两次不该看到失败提示。
func (s *Service) LowerDifficulty(ctx context.Context, childID, kpID uuid.UUID) (int, error) {
	rec, ok, err := s.repo.Get(ctx, childID, kpID)
	if err != nil {
		return 0, err
	}
	if !ok {
		// 还没学过这个知识点：没有可降的档。顺带区分「知识点不存在」与「没学过」，
		// 否则家长点了没反应又查不出原因。
		exists, err := s.repo.Exists(ctx, kpID)
		if err != nil {
			return 0, err
		}
		if !exists {
			return 0, ErrNotFound
		}
		return MinDifficulty, nil
	}
	if rec.Difficulty <= MinDifficulty {
		return rec.Difficulty, nil
	}
	rec.Difficulty--
	if _, err := s.repo.Upsert(ctx, childID, rec); err != nil {
		return 0, fmt.Errorf("降档失败: %w", err)
	}
	return rec.Difficulty, nil
}

func sameDay(a, b time.Time) bool {
	ya, ma, da := a.Date()
	yb, mb, db := b.Date()
	return ya == yb && ma == mb && da == db
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }
