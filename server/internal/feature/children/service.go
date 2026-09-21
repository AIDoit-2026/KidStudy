package children

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"kidstudy/internal/platform/apperr"
)

// Service 承载孩子档案的业务规则。
type Service struct {
	repo *Repository
}

// NewService 构造 children 服务。
func NewService(repo *Repository) *Service { return &Service{repo: repo} }

// List 列出当前家长的档案（默认不含已归档）。
func (s *Service) List(ctx context.Context, parentID uuid.UUID, includeArchived bool) ([]ChildView, error) {
	list, err := s.repo.ListByParent(ctx, parentID, !includeArchived)
	if err != nil {
		return nil, apperr.Internal(err)
	}

	views := make([]ChildView, 0, len(list))
	for _, c := range list {
		views = append(views, c.ToView())
	}
	return views, nil
}

// Get 取单个档案，越权与不存在都表现为 404。
func (s *Service) Get(ctx context.Context, parentID, childID uuid.UUID) (ChildView, error) {
	c, err := s.EnsureOwned(ctx, parentID, childID)
	if err != nil {
		return ChildView{}, err
	}
	return c.ToView(), nil
}

// EnsureOwned 校验「这个孩子属于这个家长」并返回领域对象。
//
// mastery / practice 需要在写掌握度与会话前先做归属校验，它们拿领域对象（而不是
// 对外视图）才能取到 uuid 形态的 ID 与阶段码；越权一律 404，不泄露 ID 是否存在。
func (s *Service) EnsureOwned(ctx context.Context, parentID, childID uuid.UUID) (Child, error) {
	c, err := s.repo.Get(ctx, parentID, childID)
	if err != nil {
		if isNoRows(err) {
			return Child{}, errNotFound
		}
		return Child{}, apperr.Internal(err)
	}
	return c, nil
}

// Create 新建档案。stage_code 给了就必须真实存在——M2 导入阶段字典前会一直返回 422。
func (s *Service) Create(ctx context.Context, parentID uuid.UUID, req CreateRequest) (ChildView, error) {
	if err := req.Validate(); err != nil {
		return ChildView{}, err
	}

	nickname, avatar := NicknameOrDefault(req.Nickname, req.AvatarID)

	var birthYM, stageCode *string
	if ym := strings.TrimSpace(req.BirthYM); ym != "" {
		birthYM = &ym
	}
	if code := strings.TrimSpace(req.StageCode); code != "" {
		ok, err := s.repo.StageExists(ctx, code)
		if err != nil {
			return ChildView{}, apperr.Internal(err)
		}
		if !ok {
			return ChildView{}, apperr.ValidationFailed("学习阶段不存在", []map[string]string{
				{"field": "stage_code", "reason": "阶段 " + code + " 尚未导入"},
			})
		}
		stageCode = &code
	}

	c, err := s.repo.Create(ctx, Child{
		ParentID:  parentID,
		Nickname:  nickname,
		AvatarID:  avatar,
		BirthYM:   birthYM,
		StageCode: stageCode,
		Active:    true,
		Settings:  req.Settings,
	})
	if err != nil {
		return ChildView{}, apperr.From(err)
	}
	return c.ToView(), nil
}

// Update 局部修改档案。
func (s *Service) Update(ctx context.Context, parentID, childID uuid.UUID, req UpdateRequest) (ChildView, error) {
	if err := req.Validate(); err != nil {
		return ChildView{}, err
	}

	// 指针字段需要归一化：空串表示「清空该可选项」，但昵称不允许清空
	patch := UpdateRequest{
		Nickname:  trimPtr(req.Nickname),
		AvatarID:  trimPtr(req.AvatarID),
		BirthYM:   trimPtr(req.BirthYM),
		StageCode: trimPtr(req.StageCode),
		Active:    req.Active,
		Settings:  req.Settings,
	}
	if patch.AvatarID != nil && *patch.AvatarID == "" {
		def := "panda"
		patch.AvatarID = &def
	}
	if patch.StageCode != nil && *patch.StageCode != "" {
		ok, err := s.repo.StageExists(ctx, *patch.StageCode)
		if err != nil {
			return ChildView{}, apperr.Internal(err)
		}
		if !ok {
			return ChildView{}, apperr.ValidationFailed("学习阶段不存在", []map[string]string{
				{"field": "stage_code", "reason": "阶段 " + *patch.StageCode + " 尚未导入"},
			})
		}
	}

	c, err := s.repo.Update(ctx, parentID, childID, patch)
	if err != nil {
		if isNoRows(err) {
			return ChildView{}, errNotFound
		}
		return ChildView{}, apperr.Internal(err)
	}
	return c.ToView(), nil
}

// Archive 归档档案（对孩子端不可见，学习数据保留）。
func (s *Service) Archive(ctx context.Context, parentID, childID uuid.UUID) error {
	if err := s.repo.Archive(ctx, parentID, childID); err != nil {
		if isNoRows(err) {
			return errNotFound
		}
		return apperr.Internal(err)
	}
	return nil
}

func isNoRows(err error) bool {
	return err == pgx.ErrNoRows || strings.Contains(err.Error(), "no rows in result set")
}

// trimPtr 去掉首尾空白；原指针为 nil 时仍返回 nil，保持「未传」语义。
func trimPtr(p *string) *string {
	if p == nil {
		return nil
	}
	v := strings.TrimSpace(*p)
	return &v
}
