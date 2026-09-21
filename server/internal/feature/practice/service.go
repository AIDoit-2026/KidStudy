package practice

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"kidstudy/internal/dbgen"
	"kidstudy/internal/feature/content"
	"kidstudy/internal/feature/mastery"
	"kidstudy/internal/pkg/randx"
	"kidstudy/internal/platform/apperr"
)

// 编排默认值：家长没改设置时用这套。
const (
	DefaultDailyLimitMin = 60
	MaxAssigned          = 10
	MaxSessionItems      = 60
)

// ContentProvider 是 practice 需要的组卷素材来源，由 content.Service 实现。
type ContentProvider interface {
	LoadMaterials(ctx context.Context, kpIDs []uuid.UUID) (content.Materials, error)
	GetMathTemplate(ctx context.Context, code string) (content.MathMaterial, error)
	ListMathTemplates(ctx context.Context) ([]content.MathTemplateView, error)
}

// BadgeAwarder 在会话结算后评测成就，由 report.Service 实现。
//
// 只约定「回授予数量」这一个最小契约：practice 不需要知道徽章长什么样，
// 也就不必 import report，跨模块依赖保持单向（report 不认识 practice）。
type BadgeAwarder interface {
	AwardChild(ctx context.Context, childID uuid.UUID) (int, error)
}

// Service 承载今日编排、组卷、判分与结算。不 import net/http。
type Service struct {
	repo    *Repository
	mastery *mastery.Service
	content ContentProvider
	awarder BadgeAwarder
	log     *slog.Logger
}

// NewService 构造练习服务。awarder 可以为 nil（不评测成就）。
func NewService(repo *Repository, masterySvc *mastery.Service, contentSvc ContentProvider, awarder BadgeAwarder, log *slog.Logger) *Service {
	return &Service{repo: repo, mastery: masterySvc, content: contentSvc, awarder: awarder, log: log}
}

// ------------------------------------------------------------------ 今日编排

// Plan 算出今日任务卡（不建会话，不写库）。
//
// 顺序按 §4.2：错题置顶 → 到期复习 → 新学 → 家长专项。同一 kp 只出现一次，
// 按「错题 > 专项 > 复习 > 新学」的优先级保留最先出现的那条。
func (s *Service) Plan(ctx context.Context, parentID, childID uuid.UUID, stageCode string, wantSubjects []string) (TodayPlan, error) {
	now := Now()
	dayStart := startOfDay(now)

	settings, err := s.repo.GetParentSettings(ctx, parentID)
	if err != nil {
		return TodayPlan{}, apperr.Internal(err)
	}
	dailyLimit := settings.DailyLimitMin
	if dailyLimit <= 0 {
		dailyLimit = DefaultDailyLimitMin
	}
	usedSec, err := s.repo.TodayUsedSeconds(ctx, childID, dayStart, now)
	if err != nil {
		return TodayPlan{}, apperr.Internal(err)
	}
	usedMin := usedSec / 60
	remaining := dailyLimit - usedMin
	if remaining < 0 {
		remaining = 0
	}

	subjects := enabledSubjects(settings.SubjectSwitches, wantSubjects)

	queue, err := s.mastery.ReviewQueue(ctx, childID, "", DefaultReviewMax)
	if err != nil {
		return TodayPlan{}, apperr.Internal(err)
	}
	wrongPage, err := s.mastery.WrongBook(ctx, childID, true, 0, MaxWrongFirst)
	if err != nil {
		return TodayPlan{}, apperr.Internal(err)
	}
	assigned, err := s.repo.ListPendingAssignments(ctx, childID, MaxAssigned)
	if err != nil {
		return TodayPlan{}, apperr.Internal(err)
	}

	items := make([]PlanItem, 0, len(wrongPage.Items)+len(queue.Items)+MaxNewPerSubject*len(subjects)+len(assigned))
	seen := map[uuid.UUID]bool{}

	appendItems := func(list []PlanItem, reason string) {
		for _, it := range list {
			if seen[it.KPID] {
				continue
			}
			if !subjectAllowed(subjects, it.SubjectCode) {
				continue
			}
			seen[it.KPID] = true
			it.Reason = reason
			items = append(items, it)
		}
	}

	// 1. 错题置顶（最多 5 条）
	appendItems(wrongItems(wrongPage.Items), ReasonWrong)
	// 2. 家长专项
	appendItems(assigned, ReasonAssigned)
	// 3. 到期复习
	appendItems(reviewItems(queue.Items), ReasonReview)
	// 4. 新学：复习积压时暂停新学，先把欠账补上（§4.4 复习积压保护）
	if !queue.Backlog {
		for _, subject := range subjects {
			if len(items) >= MaxSessionItems {
				break
			}
			limit := newLimitFor(subject)
			if remaining <= 0 {
				break
			}
			newItems, err := s.newKPs(ctx, childID, stageCode, subject, limit)
			if err != nil {
				return TodayPlan{}, apperr.Internal(err)
			}
			appendItems(newItems, ReasonNew)
		}
	}
	if len(items) > MaxSessionItems {
		items = items[:MaxSessionItems]
	}

	plan := TodayPlan{
		ChildID:          childID,
		GeneratedAt:      now.UTC().Format(time.RFC3339),
		RemainingMinutes: remaining,
		DailyLimitMin:    dailyLimit,
		UsedMinutes:      usedMin,
		Backlog:          queue.Backlog,
		SuggestReview:    queue.SuggestReview,
		DueCount:         int(queue.DueCount),
		Subjects:         subjects,
		Items:            items,
	}
	plan.Message = planMessage(plan)
	return plan, nil
}

// newKPs 取新学知识点：优先按标准节奏基准线的 Day 序号，没铺线时按阶段兜底。
func (s *Service) newKPs(ctx context.Context, childID uuid.UUID, stageCode, subject string, limit int) ([]PlanItem, error) {
	if limit <= 0 {
		limit = newLimitFor(subject)
	}
	if limit > MaxNewPerSubject {
		limit = MaxNewPerSubject
	}
	kind := kindOfSubject(subject)
	items, err := s.repo.ListNewKPsByPlan(ctx, childID, subject, kind, limit)
	if err != nil {
		return nil, err
	}
	if len(items) > 0 {
		return items, nil
	}
	return s.repo.ListNewKPsByStage(ctx, childID, subject, stageFor(subject, stageCode), kind, limit)
}

// ------------------------------------------------------------------ 会话

// StartSession 组卷并建会话：写 learning_sessions + session_items(题面快照 + 答案)。
func (s *Service) StartSession(ctx context.Context, childID uuid.UUID, stageCode, deviceType, subject string) (SessionView, error) {
	// 编排需要 parentID 读设置；这里只按学科过滤，额度校验在 Plan 里已经体现，
	// 家长强制开始的会话不受额度限制（家长有权决定）。
	plan, err := s.Plan(ctx, uuid.Nil, childID, stageCode, splitSubjects(subject))
	if err != nil {
		return SessionView{}, err
	}
	if len(plan.Items) == 0 {
		return SessionView{}, apperr.BadRequest("今天没有可练的内容，先去内容库看看或者把到期复习清一清")
	}

	kpIDs := make([]uuid.UUID, 0, len(plan.Items))
	for _, it := range plan.Items {
		kpIDs = append(kpIDs, it.KPID)
	}
	materials, err := s.content.LoadMaterials(ctx, kpIDs)
	if err != nil {
		return SessionView{}, apperr.Internal(err)
	}

	seed := dailySeed(childID, startOfDay(Now()))
	questions := BuildQuestions(plan.Items, materials, seed)
	if len(questions) == 0 {
		return SessionView{}, apperr.BadRequest("这些知识点暂时组不出题目，换一批试试")
	}

	if deviceType == "" {
		deviceType = "unknown"
	}

	tx, err := s.repo.Begin(ctx)
	if err != nil {
		return SessionView{}, apperr.Internal(err)
	}
	defer tx.Rollback(ctx)

	repoTx := s.repo.WithTx(tx)
	session, err := repoTx.InsertSession(ctx, childID, deviceType)
	if err != nil {
		return SessionView{}, apperr.Internal(err)
	}
	items := make([]Item, 0, len(questions))
	for i, q := range questions {
		item, err := repoTx.InsertItem(ctx, session.ID, q.KPID, i+1, q)
		if err != nil {
			return SessionView{}, apperr.Internal(err)
		}
		items = append(items, item)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE learning_sessions SET question_count = $2 WHERE id = $1`, session.ID, len(items)); err != nil {
		return SessionView{}, apperr.Internal(err)
	}
	// 专项指派用过即结，免得天天顶在最前面
	if err := repoTx.MarkAssignmentsDone(ctx, childID, kpIDs); err != nil {
		return SessionView{}, apperr.Internal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return SessionView{}, apperr.Internal(err)
	}

	session.QuestionCount = len(items)
	return sessionView(session, items), nil
}

// GetSession 取会话与题目（断点续练用），不含答案。
func (s *Service) GetSession(ctx context.Context, childID, sessionID uuid.UUID) (SessionView, error) {
	session, err := s.repo.GetSession(ctx, sessionID, childID)
	if err != nil {
		return SessionView{}, mapRepoErr(err)
	}
	items, err := s.repo.ListItems(ctx, sessionID)
	if err != nil {
		return SessionView{}, apperr.Internal(err)
	}
	return sessionView(session, items), nil
}

// ------------------------------------------------------------------ 判分

// Grade 服务端判分：比对答案 → 写题目与流水 → 调掌握度状态机。全部在一个事务里。
func (s *Service) Grade(ctx context.Context, childID, sessionID, itemID uuid.UUID, answer []string, usedHint bool, elapsedMS int) (AnswerResult, error) {
	if elapsedMS < 0 {
		elapsedMS = 0
	}
	now := Now()

	tx, err := s.repo.Begin(ctx)
	if err != nil {
		return AnswerResult{}, apperr.Internal(err)
	}
	defer tx.Rollback(ctx)

	repoTx := s.repo.WithTx(tx)
	session, err := repoTx.GetSession(ctx, sessionID, childID)
	if err != nil {
		return AnswerResult{}, mapRepoErr(err)
	}
	if session.Status != "active" {
		return AnswerResult{}, apperr.Conflict("这个会话已经结束了")
	}
	item, err := repoTx.GetItem(ctx, itemID, sessionID)
	if err != nil {
		return AnswerResult{}, mapRepoErr(err)
	}
	if item.State != "pending" {
		return AnswerResult{}, apperr.Conflict("这道题已经作答过了")
	}

	correct := Grade(item.Key, answer)
	if err := repoTx.UpdateItemAnswer(ctx, itemID, sessionID, "answered", correct, usedHint, elapsedMS, now); err != nil {
		return AnswerResult{}, apperr.Internal(err)
	}

	result := AnswerResult{
		ItemID:      itemID.String(),
		IsCorrect:   correct,
		State:       "answered",
		CorrectText: item.Key.Text,
		Explain:     item.Key.Explain,
	}

	// 主观项（描红/跟读）不判分、不写流水、不动掌握度 —— 由家长确认
	if correct == nil {
		if err := tx.Commit(ctx); err != nil {
			return AnswerResult{}, apperr.Internal(err)
		}
		return result, nil
	}

	if err := repoTx.InsertAnswerLog(ctx, childID, sessionID, itemID, item.KPID,
		item.SubjectCode, item.QuestionType, *correct, usedHint, elapsedMS); err != nil {
		return AnswerResult{}, apperr.Internal(err)
	}

	mres, err := s.mastery.WithTx(tx).ApplyResult(ctx, childID, item.KPID, *correct, usedHint, elapsedMS)
	if err != nil {
		return AnswerResult{}, apperr.Internal(err)
	}
	result.Mastery = &MasteryBrief{
		Level:             mres.Level,
		NextReviewMinutes: mres.NextReviewInMinutes,
		Mastered:          mres.Mastered,
		EnteredWrongBook:  mres.EnteredWrongBook,
		ClearedWrongBook:  mres.ClearedWrongBook,
		DifficultyChanged: mres.DifficultyChanged,
	}
	// 连对 3 题加一星（§4.3 反馈原则）
	_, _, correctCount, _, _, cerr := repoTx.SessionCounts(ctx, sessionID)
	if cerr == nil && *correct && correctCount > 0 && correctCount%3 == 0 {
		result.StarDelta = 1
	}

	if err := tx.Commit(ctx); err != nil {
		return AnswerResult{}, apperr.Internal(err)
	}
	return result, nil
}

// Skip 跳过一题：不计正确率，但计入完成度（§4.11）。
func (s *Service) Skip(ctx context.Context, childID, sessionID, itemID uuid.UUID) (AnswerResult, error) {
	item, err := s.repo.GetItem(ctx, itemID, sessionID)
	if err != nil {
		return AnswerResult{}, mapRepoErr(err)
	}
	if item.State != "pending" {
		return AnswerResult{}, apperr.Conflict("这道题已经作答过了")
	}
	if err := s.repo.UpdateItemAnswer(ctx, itemID, sessionID, "skipped", nil, false, 0, Now()); err != nil {
		return AnswerResult{}, apperr.Internal(err)
	}
	return AnswerResult{ItemID: itemID.String(), State: "skipped", Explain: "跳过啦，下次再来试试"}, nil
}

// ------------------------------------------------------------------ 结算

// Finish 结束会话：算时长、正确率、星级，并增量更新当日汇总。
//
// 正确率只统计客观题（is_correct 非 NULL）；描红/跟读计入完成度但不出正确率。
func (s *Service) Finish(ctx context.Context, childID, sessionID uuid.UUID) (SessionSummary, error) {
	now := Now()
	tx, err := s.repo.Begin(ctx)
	if err != nil {
		return SessionSummary{}, apperr.Internal(err)
	}
	defer tx.Rollback(ctx)

	repoTx := s.repo.WithTx(tx)
	session, err := repoTx.GetSession(ctx, sessionID, childID)
	if err != nil {
		return SessionSummary{}, mapRepoErr(err)
	}
	if session.Status != "active" {
		// 已结束的会话重复结算：直接回上次的结果，不报错（前端可能重发）
		return summaryOf(session, 0), nil
	}

	items, err := repoTx.ListItems(ctx, sessionID)
	if err != nil {
		return SessionSummary{}, apperr.Internal(err)
	}
	total, answered, correct, skipped, hinted := 0, 0, 0, 0, false
	perSubject := map[string][3]int{} // subject → {题量, 答对, 新学}
	kpIDs := make([]uuid.UUID, 0, len(items))
	for _, it := range items {
		total++
		if it.State == "skipped" {
			skipped++
		}
		if it.State == "answered" {
			answered++
		}
		if it.IsCorrect != nil && *it.IsCorrect {
			correct++
		}
		if it.UsedHint {
			hinted = true
		}
		kpIDs = append(kpIDs, it.KPID)
		agg := perSubject[it.SubjectCode]
		agg[0]++
		if it.IsCorrect != nil && *it.IsCorrect {
			agg[1]++
		}
		perSubject[it.SubjectCode] = agg
	}

	accuracy := 0.0
	if answered > 0 {
		accuracy = float64(correct) / float64(answered)
	}
	stars := ComputeStars(accuracy, hinted)
	duration := int(now.Sub(session.StartedAt).Seconds())
	if duration < 0 {
		duration = 0
	}

	// 客观题答过就由程序判定完成（§4.11）；主观项留给家长确认
	completedBy := ""
	if answered > 0 {
		completedBy = "auto"
	}

	updated, err := repoTx.FinishSession(ctx, dbgen.FinishSessionParams{
		ID:            sessionID,
		ChildID:       childID,
		NowTs:         ts(now),
		DurationSec:   int32(duration),
		QuestionCount: int32(total),
		AnsweredCount: int32(answered),
		CorrectCount:  int32(correct),
		SkippedCount:  int32(skipped),
		StarCount:     int32(stars),
		CompletedBy:   pgtype.Text{String: completedBy, Valid: completedBy != ""},
	})
	if err != nil {
		return SessionSummary{}, mapRepoErr(err)
	}

	// 日汇总按学科拆开累加（daily_stats 的唯一键是 child+date+subject）
	dayStart := startOfDay(now)
	newToday, err := repoTx.CountNewLearnedToday(ctx, childID, kpIDs, dayStart)
	if err != nil {
		return SessionSummary{}, apperr.Internal(err)
	}
	for subject, agg := range perSubject {
		share := agg[0]
		if total > 0 {
			// 时长按题量占比摊到各学科
			share = duration * agg[0] / total
		}
		actualNew := 0
		if agg[0] == total && total > 0 {
			actualNew = newToday
		} else if total > 0 {
			actualNew = newToday * agg[0] / total
		}
		if err := repoTx.UpsertDailyStat(ctx, childID, dayStart, subject,
			share, agg[0], agg[1], 0, stars*agg[0]/maxInt(total, 1), actualNew); err != nil {
			return SessionSummary{}, apperr.Internal(err)
		}
	}
	// 合计行（subject_code=''）始终写：M4 的报表趋势读的就是它，
	// 只在「没有分学科」时才写会让大多数日子的合计行缺失。
	if total > 0 {
		if err := repoTx.UpsertDailyStat(ctx, childID, dayStart, "", duration, total, correct, 0, stars, newToday); err != nil {
			return SessionSummary{}, apperr.Internal(err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return SessionSummary{}, apperr.Internal(err)
	}

	// 结算之后评测成就：孩子刚拿到的徽章要立刻能看到，不必等夜里的 worker。
	// 失败只记日志 —— 徽章是激励，不该因为一次评测异常把整次结算判成失败。
	if s.awarder != nil {
		if n, err := s.awarder.AwardChild(ctx, childID); err != nil {
			s.log.Warn("成就评测失败，不影响结算", "child_id", childID, "error", err)
		} else if n > 0 {
			s.log.Info("会话结算后授予成就", "child_id", childID, "granted", n)
		}
	}
	return summaryOf(updated, accuracy), nil
}

// Confirm 家长确认/补录主观项。只写 parent_score / parent_note / completed_by，
// 客观题数与正确率一个都不动 —— 家长打分不能污染客观正确率（§4.11）。
func (s *Service) Confirm(ctx context.Context, childID, sessionID uuid.UUID, req ConfirmRequest) (SessionView, error) {
	if req.ParentScore != nil && (*req.ParentScore < 1 || *req.ParentScore > 5) {
		return SessionView{}, apperr.BadRequest("家长评分需在 1 到 5 之间")
	}
	existing, err := s.repo.GetSession(ctx, sessionID, childID)
	if err != nil {
		return SessionView{}, mapRepoErr(err)
	}

	completedBy := ""
	if req.MarkDone {
		switch existing.CompletedBy {
		case "auto":
			completedBy = "mixed"
		case "":
			completedBy = "parent"
		default:
			completedBy = existing.CompletedBy
		}
	}

	session, err := s.repo.MarkSessionConfirmed(ctx, sessionID, childID, req.ParentScore, req.ParentNote, completedBy, Now())
	if err != nil {
		return SessionView{}, mapRepoErr(err)
	}
	return sessionView(session, nil), nil
}

// ------------------------------------------------------------------ 数学预览

// MathPreview 参数化预览，与打印中心共用同一个生成器（§4.2「seed 可复现」）。
func (s *Service) MathPreview(ctx context.Context, templateCode string, count int, seed uint64) ([]MathQuestion, error) {
	if count <= 0 {
		count = 10
	}
	if count > 200 {
		count = 200
	}
	mat, err := s.content.GetMathTemplate(ctx, templateCode)
	if err != nil {
		return nil, apperr.NotFound("数学模板不存在或未发布")
	}
	cfg, err := ParseMathConfig(mat.Generator)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	return GenerateMath(cfg, seed, count), nil
}

// MathTemplates 列出可用模板。
func (s *Service) MathTemplates(ctx context.Context) ([]content.MathTemplateView, error) {
	return s.content.ListMathTemplates(ctx)
}

// ------------------------------------------------------------------ 内部

func mapRepoErr(err error) error {
	switch {
	case err == nil:
		return nil
	case err == ErrNotFound:
		return apperr.NotFound("会话或题目不存在")
	case err == ErrAlreadyAnswered:
		return apperr.Conflict("这道题已经作答过了")
	default:
		return apperr.Internal(err)
	}
}

func summaryOf(s Session, accuracy float64) SessionSummary {
	msg := "今天学完了，很棒！"
	switch {
	case s.AnsweredCount == 0:
		msg = "今天还没答题，明天继续"
	case accuracy >= PassAccuracy:
		msg = "今天的任务完成啦"
	default:
		msg = fmt.Sprintf("今天学了 %d 分钟，明天继续", s.DurationSec/60+1)
	}
	return SessionSummary{
		SessionID:     s.ID.String(),
		Status:        s.Status,
		DurationSec:   s.DurationSec,
		QuestionCount: s.QuestionCount,
		AnsweredCount: s.AnsweredCount,
		CorrectCount:  s.CorrectCount,
		SkippedCount:  s.SkippedCount,
		Accuracy:      round2(accuracy),
		StarCount:     s.StarCount,
		CompletedBy:   s.CompletedBy,
		Passed:        s.AnsweredCount > 0 && accuracy >= PassAccuracy,
		Message:       msg,
	}
}

func sessionView(s Session, items []Item) SessionView {
	view := SessionView{
		ID:        s.ID.String(),
		ChildID:   s.ChildID.String(),
		Status:    s.Status,
		StartedAt: s.StartedAt.UTC().Format(time.RFC3339),
		ItemCount: len(items),
		Items:     make([]ItemView, 0, len(items)),
	}
	if s.EndedAt != nil {
		ended := s.EndedAt.UTC().Format(time.RFC3339)
		view.EndedAt = &ended
	}
	for _, it := range items {
		iv := ItemView{
			ID:           it.ID.String(),
			Seq:          it.Seq,
			KPID:         it.KPID.String(),
			SubjectCode:  it.SubjectCode,
			QuestionType: it.QuestionType,
			State:        it.State,
			Question:     it.Snapshot,
			IsCorrect:    it.IsCorrect,
			UsedHint:     it.UsedHint,
			ElapsedMS:    it.ElapsedMS,
		}
		// 答案只在作答后回传，供「答错看正确答案」；未作答的题目绝不带答案
		if it.State != "pending" {
			text := it.Key.Text
			iv.CorrectAnswer = &text
			iv.Explain = it.Key.Explain
		}
		view.Items = append(view.Items, iv)
	}
	return view
}

func wrongItems(entries []mastery.WrongEntry) []PlanItem {
	out := make([]PlanItem, 0, len(entries))
	for _, e := range entries {
		out = append(out, PlanItem{
			KPID: e.KPID, SubjectCode: e.SubjectCode, Kind: e.Kind,
			Code: e.Code, Name: e.Name, Difficulty: 1,
		})
	}
	return out
}

func reviewItems(entries []mastery.ReviewItem) []PlanItem {
	out := make([]PlanItem, 0, len(entries))
	for _, e := range entries {
		out = append(out, PlanItem{
			KPID: e.KPID, SubjectCode: e.SubjectCode, Kind: e.Kind,
			Code: e.Code, Name: e.Name, Difficulty: e.Difficulty,
		})
	}
	return out
}

func kindOfSubject(subject string) string {
	switch subject {
	case "english":
		return "word"
	case "math":
		return "math_skill"
	default:
		return "hanzi"
	}
}

// stageFor 决定新学候选要不要按阶段过滤。
//
// 孩子的 stage_code 是语文阶段（S1/G3 之类），英语与数学各有自己的阶段体系
// （E1–E10 / M1–M5），拿语文阶段去过滤数学知识点会一条都选不出来。
func stageFor(subject, stageCode string) string {
	if subject == "chinese" {
		return stageCode
	}
	return ""
}

func newLimitFor(subject string) int {
	switch subject {
	case "english":
		return DefaultNewEnglish
	case "math":
		return DefaultNewMath
	default:
		return DefaultNewChinese
	}
}

// enabledSubjects 学科开关为空表示三科全开；wantSubjects 是本次请求指定的学科。
func enabledSubjects(switches map[string]bool, want []string) []string {
	all := []string{"chinese", "english", "math"}
	out := make([]string, 0, len(all))
	for _, s := range all {
		if len(switches) > 0 {
			if on, ok := switches[s]; ok && !on {
				continue
			}
		}
		if len(want) > 0 && !contains(want, s) {
			continue
		}
		out = append(out, s)
	}
	return out
}

func subjectAllowed(subjects []string, subject string) bool {
	if subject == "" {
		return true
	}
	return contains(subjects, subject)
}

func splitSubjects(v string) []string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// dailySeed 让同一个孩子在同一天拿到同一批题：重开会话题目不变，
// 换一天自然换题。打印中心要固定题目时也用这个 seed。
func dailySeed(childID uuid.UUID, day time.Time) uint64 {
	return randx.FNV64a([]byte(childID.String() + "|" + day.Format("2006-01-02")))
}

func startOfDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

func planMessage(p TodayPlan) string {
	switch {
	case p.Backlog:
		return "到期复习有点多，今天先专心复习吧"
	case p.RemainingMinutes <= 0:
		return "今天的练习时间用完啦，明天再来"
	case len(p.Items) == 0:
		return "今天没有新的内容，可以去复习或者读故事"
	default:
		return ""
	}
}

func round2(v float64) float64 {
	return float64(int(v*100+0.5)) / 100
}

// ParseAnswer 把前端传来的答案统一成字符串切片：单值、数组都收。
func ParseAnswer(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var one string
	if err := json.Unmarshal(raw, &one); err == nil {
		if strings.TrimSpace(one) == "" {
			return nil, nil
		}
		return []string{one}, nil
	}
	var many []string
	if err := json.Unmarshal(raw, &many); err != nil {
		return nil, apperr.BadRequest("answer 需要是字符串或字符串数组")
	}
	return many, nil
}
