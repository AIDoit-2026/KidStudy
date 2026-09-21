package content

import (
	"context"
	"fmt"
	"time"
)

// dailyQuota 是标准节奏的每日新学量（设计文档 §4.9）。
//
// 它只用来铺基准线，不构成任何闸门：孩子一天学几课、提前学到哪，系统都不拦，
// 只把「实际进度 vs 计划进度」的差值算出来给家长看。
var dailyQuota = map[string]int{
	"chinese": 6,
	"english": 5,
	"math":    2,
}

// importPlans 为每个孩子铺标准节奏基准线。
//
// 幂等：unique(child_id, kp_id) + ON CONFLICT DO NOTHING。已铺过的知识点保持原有
// planned_day_index 不动 —— 这条线一旦生成就不再改，否则偏差就没法算了。
func (im *Importer) importPlans(ctx context.Context, rep *Report) error {
	children, err := im.repo.ListPlanChildren(ctx)
	if err != nil {
		return err
	}
	if len(children) == 0 {
		im.log.Info("尚无孩子档案，跳过基准线铺线")
		return nil
	}

	cols := []string{"child_id", "kp_id", "subject_code", "stage_code", "planned_day_index", "planned_date"}

	for _, child := range children {
		// 起点取档案创建日：基准线是「从建档那天开始的计划」，不是自然日
		anchor := child.CreatedAt.UTC().Truncate(24 * time.Hour)

		var rows [][]any
		for subject, quota := range dailyQuota {
			if quota <= 0 {
				continue
			}
			kps, err := im.repo.ListPlannedKPs(ctx, subject, child.StageCode)
			if err != nil {
				return err
			}
			for i, kp := range kps {
				day := i/quota + 1
				rows = append(rows, []any{
					child.ID.String(),
					kp.ID.String(),
					subject,
					nullIfEmpty(kp.StageCode),
					day,
					anchor.AddDate(0, 0, day-1).Format("2006-01-02"),
				})
			}
		}
		if len(rows) == 0 {
			continue
		}

		written, err := bulkUpsert(ctx, im.repo.Pool(), "curriculum_plan", cols, rows,
			[]string{"child_id", "kp_id"}, nil)
		if err != nil {
			return fmt.Errorf("为孩子 %s 铺基准线失败: %w", child.ID, err)
		}

		rep.PlanChildren++
		rep.PlanRows += written
		im.log.Info("基准线已铺开",
			"child_id", child.ID.String(),
			"stage", child.StageCode,
			"planned_total", len(rows),
			"newly_added", written,
		)
	}
	return nil
}
