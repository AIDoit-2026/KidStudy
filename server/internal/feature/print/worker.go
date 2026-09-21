package print

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"kidstudy/internal/dbgen"
)

// PDFObjectPrefix 是 PDF 在对象存储里的前缀。
const PDFObjectPrefix = "print/"

// RenderBatch 是一次渲染批次的结果。
type RenderBatch struct {
	Claimed int `json:"claimed"`
	Ready   int `json:"ready"`
	Failed  int `json:"failed"`
}

// RenderPending 拉取并渲染待办任务。由 cmd/worker 调用，不在请求处理器里跑。
//
// 幂等与容错：
//   - 拉取用 FOR UPDATE SKIP LOCKED，多实例并行不会重复渲染同一个任务；
//   - 单个任务失败只标记它自己并继续，不拖垮整批；
//   - 进程崩溃留在 rendering 的任务由 RequeueStale 在下次启动时退回队列。
func (s *Service) RenderPending(ctx context.Context, limit int) (RenderBatch, error) {
	if s.renderer == nil {
		return RenderBatch{}, errors.New("未配置 PDF 渲染器")
	}
	if limit <= 0 {
		limit = 5
	}
	jobs, err := s.repo.Claim(ctx, limit)
	if err != nil {
		return RenderBatch{}, fmt.Errorf("拉取待渲染任务失败: %w", err)
	}

	batch := RenderBatch{Claimed: len(jobs)}
	for _, job := range jobs {
		if err := s.renderOne(ctx, job); err != nil {
			batch.Failed++
			s.log.Error("渲染打印任务失败", "job_id", job.ID, "template", job.TemplateCode, "error", err)
			// 失败原因单独下一次写：即使 ctx 已经取消也要留下痕迹，所以用独立 context
			markCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if merr := s.repo.MarkFailed(markCtx, job.ID, err.Error()); merr != nil {
				s.log.Error("记录渲染失败原因时出错", "job_id", job.ID, "error", merr)
			}
			cancel()
			continue
		}
		batch.Ready++
	}
	return batch, nil
}

func (s *Service) renderOne(ctx context.Context, job dbgen.ClaimPrintJobsRow) error {
	var payload Payload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return fmt.Errorf("解析题面数据失败: %w", err)
	}
	html, err := Render(payload)
	if err != nil {
		return err
	}
	pdf, err := s.renderer.Render(ctx, html)
	if err != nil {
		return err
	}

	rel := PDFObjectPrefix + job.ID.String() + ".pdf"
	if _, err := s.store.Put(ctx, rel, bytes.NewReader(pdf)); err != nil {
		return fmt.Errorf("保存 PDF 失败: %w", err)
	}

	// 页数取真实值；解析不出来才退回排版预估，不静默糊过去
	pages, ok := pdfPageCount(pdf)
	if !ok {
		pages = planPages(payload)
		if pages <= 0 {
			pages = 1
		}
		s.log.Warn("无法从 PDF 解析页数，退回排版预估", "job_id", job.ID, "planned_pages", pages)
	}
	if err := s.repo.MarkReady(ctx, job.ID, rel, pages); err != nil {
		// PDF 已经写进存储但状态没更新：这次白做，退回去由重试覆盖（Put 是幂等覆盖）
		return fmt.Errorf("更新任务状态失败: %w", err)
	}

	s.log.Info("打印任务渲染完成", "job_id", job.ID, "bytes", len(pdf), "pages", pages)
	return nil
}

// RequeueStale 把卡在 rendering 超过 stale 的任务退回队列。worker 启动时跑一次。
func (s *Service) RequeueStale(ctx context.Context, stale time.Duration) (int, error) {
	n, err := s.repo.RequeueStale(ctx, stale)
	if err != nil {
		return 0, err
	}
	if n > 0 {
		s.log.Warn("退回卡住的渲染任务", "count", n, "stale", stale.String())
	}
	return int(n), nil
}

// PurgeExpired 清理超过保留期的 PDF 文件。
//
// 只删文件、保留打印记录本身：payload 是不可变快照，家长日后仍能看到
// 「当时印过什么」，需要时重新排队即可再生成一份同样的 PDF。
func (s *Service) PurgeExpired(ctx context.Context, before time.Time, limit int) (int, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.repo.ExpiredPDFs(ctx, before, limit)
	if err != nil {
		return 0, fmt.Errorf("查询过期打印件失败: %w", err)
	}
	purged := 0
	for _, row := range rows {
		if !row.PdfPath.Valid {
			continue
		}
		if err := s.store.Delete(ctx, row.PdfPath.String); err != nil {
			s.log.Error("删除过期 PDF 失败", "job_id", row.ID, "path", row.PdfPath.String, "error", err)
			continue
		}
		if err := s.repo.ClearPDF(ctx, row.ID); err != nil {
			s.log.Error("清理打印件引用失败", "job_id", row.ID, "error", err)
			continue
		}
		purged++
	}
	if purged > 0 {
		s.log.Info("已清理过期打印件", "count", purged, "before", before.Format(time.RFC3339))
	}
	return purged, nil
}
