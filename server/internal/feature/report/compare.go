package report

import (
	"context"
	"sort"
	"time"

	"github.com/google/uuid"
)

// compareMaxChildren 一次最多对比几个孩子：再多曲线就叠成一团了。
const compareMaxChildren = 4

// CompareCalendarCap 自然日对齐时最多回看多少天，防止孩子都很久没学时拉一条长尾。
const CompareCalendarCap = 366

// Compare 多孩对比（§4.10）。
//
// 对齐方式是关键：默认 align=session（学习日序号，各自从第一次学习起算），
// 比的是学习速度而不是入学早晚；align=calendar 才看真实时间轴。
// 呈现纪律：只并列、不排名、不给胜负结论 —— note 里明确写出来，前端不要再自己解读。
func (s *Service) Compare(ctx context.Context, parentID uuid.UUID, childIDs []uuid.UUID, align, subject string) (CompareView, error) {
	if align != "calendar" {
		align = "session"
	}
	if subject != "" && !validSubject(subject) {
		return CompareView{}, errBadSubject
	}
	if len(childIDs) == 0 {
		return CompareView{}, errNoChild
	}
	if len(childIDs) > compareMaxChildren {
		return CompareView{}, errTooManyChildren
	}

	infos, err := s.repo.ChildrenInfo(ctx, parentID, childIDs)
	if err != nil {
		return CompareView{}, err
	}
	if len(infos) != len(dedupe(childIDs)) {
		// 有 ID 不属于当前家长或不存在 —— 一律 404，不区分是哪种
		return CompareView{}, ErrNotFound
	}

	type childSeries struct {
		info           childInfo
		masteredByDate map[string]int64
		attemptsByDate map[string]int64
		correctByDate  map[string]int64
		durationByDate map[string]int64
		activeDates    []string
		metrics        CompareMetrics
	}
	all := make([]*childSeries, 0, len(infos))
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	origin := ""
	var originDate time.Time

	for _, info := range infos {
		cs := &childSeries{
			info:           childInfo{ID: info.ID, Nickname: info.Nickname, AvatarID: info.AvatarID},
			masteredByDate: map[string]int64{},
			attemptsByDate: map[string]int64{},
			correctByDate:  map[string]int64{},
			durationByDate: map[string]int64{},
		}
		md, err := s.repo.DailyMastered(ctx, info.ID, subject)
		if err != nil {
			return CompareView{}, err
		}
		for _, r := range md {
			cs.masteredByDate[fmtDate(r.StatDate)] = r.MasteredCount
		}
		da, err := s.repo.DailyAnswers(ctx, info.ID, subject)
		if err != nil {
			return CompareView{}, err
		}
		for _, r := range da {
			key := fmtDate(r.StatDate)
			cs.attemptsByDate[key] += r.Attempts
			cs.correctByDate[key] += r.CorrectCount
		}
		dd, err := s.repo.DailyDuration(ctx, info.ID)
		if err != nil {
			return CompareView{}, err
		}
		for _, r := range dd {
			cs.durationByDate[fmtDate(r.StatDate)] = r.DurationSec
		}

		// 活跃日 = 有掌握或有作答的日期
		dateSet := map[string]struct{}{}
		for d := range cs.masteredByDate {
			dateSet[d] = struct{}{}
		}
		for d := range cs.attemptsByDate {
			dateSet[d] = struct{}{}
		}
		for d := range dateSet {
			cs.activeDates = append(cs.activeDates, d)
		}
		sort.Strings(cs.activeDates)

		if fd, err := s.repo.FirstStudyDate(ctx, info.ID); err == nil && fd.Valid {
			cs.info.FirstDate = fd.Time.Format("2006-01-02")
			if origin == "" || cs.info.FirstDate < origin {
				origin = cs.info.FirstDate
				originDate = time.Date(fd.Time.Year(), fd.Time.Month(), fd.Time.Day(), 0, 0, 0, 0, today.Location())
			}
		}

		var masteredTotal, attemptsTotal, correctTotal, durationTotal int64
		for _, v := range cs.masteredByDate {
			masteredTotal += v
		}
		for _, v := range cs.attemptsByDate {
			attemptsTotal += v
		}
		for _, v := range cs.correctByDate {
			correctTotal += v
		}
		for _, v := range cs.durationByDate {
			durationTotal += v
		}
		eff, err := s.repo.EfficiencyTotal(ctx, info.ID, subject)
		if err != nil {
			return CompareView{}, err
		}
		prog, err := s.repo.SubjectProgress(ctx, info.ID, now)
		if err != nil {
			return CompareView{}, err
		}
		var planned int64
		dev := 0.0
		if subject == "" {
			for _, p := range prog {
				planned += p.PlannedTotal
			}
			dev, err = s.overallDeviation(ctx, info.ID, now)
			if err != nil {
				return CompareView{}, err
			}
		} else {
			for _, p := range prog {
				if p.SubjectCode == subject {
					planned = p.PlannedTotal
				}
			}
			dev, err = s.subjectDeviation(ctx, info.ID, subject, now)
			if err != nil {
				return CompareView{}, err
			}
		}

		activeDays := int64(len(cs.activeDates))
		avgNew := 0.0
		if activeDays > 0 {
			avgNew = float64(masteredTotal) / float64(activeDays)
		}
		cs.metrics = CompareMetrics{
			MasteredTotal:      masteredTotal,
			PlannedTotal:       planned,
			ActiveDays:         activeDays,
			AvgNewPerDay:       round4(avgNew),
			AttemptsPerMastery: round4(eff.AttemptsPerMastery),
			CorrectRate:        ratio(correctTotal, attemptsTotal),
			DurationSec:        durationTotal,
			DeviationDays:      dev,
		}
		all = append(all, cs)
	}

	// 组装曲线
	children := make([]CompareChild, 0, len(all))
	if align == "calendar" && originDate.IsZero() {
		align = "session" // 谁都没学过，退回按学习日（结果都是空曲线）
	}
	for _, cs := range all {
		cc := CompareChild{
			ChildID: cs.info.ID.String(), Nickname: cs.info.Nickname,
			AvatarID: cs.info.AvatarID, FirstDate: cs.info.FirstDate,
			Metrics: cs.metrics,
		}
		if align == "session" {
			cc.Points = sessionAlignedPoints(cs.masteredByDate, cs.attemptsByDate, cs.correctByDate, cs.activeDates)
		} else {
			cc.Points = calendarAlignedPoints(cs.masteredByDate, cs.attemptsByDate, cs.correctByDate, originDate, today)
		}
		children = append(children, cc)
	}

	return CompareView{
		Align:       align,
		SubjectCode: subject,
		Children:    children,
		Note: "这里只做并列展示，不做排名，也不分高下：每个孩子的起点、性格、兴趣都不一样，" +
			"曲线叠在一起是为了看各自的节奏，不是为了比谁快。家长可在设置里关闭对比模块。",
	}, nil
}

// sessionAlignedPoints 按「学习日序号」出点：只统计活跃日，各自从第 1 个学习日起算。
func sessionAlignedPoints(mastered, attempts, correct map[string]int64, activeDates []string) []ComparePoint {
	points := make([]ComparePoint, 0, len(activeDates))
	var cum int64
	for i, d := range activeDates {
		cum += mastered[d]
		points = append(points, ComparePoint{
			Index:       i + 1,
			CumMastered: cum,
			NewMastered: mastered[d],
			Attempts:    attempts[d],
			CorrectRate: ratio(correct[d], attempts[d]),
		})
	}
	return points
}

// calendarAlignedPoints 按自然日对齐：X 轴是距最早首日的天数，所有孩子共用同一条轴，
// 未学习的日子累计值保持不变，这样两条曲线的时间意义才是可比的。
func calendarAlignedPoints(mastered, attempts, correct map[string]int64, origin, today time.Time) []ComparePoint {
	var points []ComparePoint
	var cum int64
	day := origin
	for i := 0; !day.After(today) && i < CompareCalendarCap; i++ {
		key := day.Format("2006-01-02")
		cum += mastered[key]
		points = append(points, ComparePoint{
			Index:       i,
			Date:        key,
			CumMastered: cum,
			NewMastered: mastered[key],
			Attempts:    attempts[key],
			CorrectRate: ratio(correct[key], attempts[key]),
		})
		day = day.AddDate(0, 0, 1)
	}
	return points
}

// subjectDeviation 单学科偏差。
func (s *Service) subjectDeviation(ctx context.Context, childID uuid.UUID, subject string, now time.Time) (float64, error) {
	prog, err := s.repo.SubjectProgress(ctx, childID, now)
	if err != nil {
		return 0, err
	}
	for _, p := range prog {
		if p.SubjectCode != subject {
			continue
		}
		dev, err := s.repo.plannedDeviation(ctx, childID, subject, p.Mastered, now)
		if err != nil {
			return 0, err
		}
		if dev == nil {
			return 0, nil
		}
		return *dev, nil
	}
	return 0, nil
}

type childInfo struct {
	ID        uuid.UUID
	Nickname  string
	AvatarID  string
	FirstDate string
}

func dedupe(ids []uuid.UUID) []uuid.UUID {
	seen := map[uuid.UUID]struct{}{}
	out := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}
