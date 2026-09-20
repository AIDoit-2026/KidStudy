// Package children 管理孩子档案：一个家长独占自己的孩子，不跨家长共享。
package children

import (
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"kidstudy/internal/platform/apperr"
)

var birthYMRe = regexp.MustCompile(`^\d{6}$`)

// Child 是孩子档案领域模型。
type Child struct {
	ID        uuid.UUID
	ParentID  uuid.UUID
	Nickname  string
	AvatarID  string
	BirthYM   *string // YYYYMM，只到年月，符合儿童隐私最小化
	StageCode *string // 当前学习阶段，M2 导入 stages 后才有值
	Active    bool
	Settings  map[string]any
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ChildView 对外返回的孩子档案。
type ChildView struct {
	ID        string         `json:"id"`
	Nickname  string         `json:"nickname"`
	AvatarID  string         `json:"avatar_id"`
	BirthYM   *string        `json:"birth_ym,omitempty"`
	StageCode *string        `json:"stage_code,omitempty"`
	Settings  map[string]any `json:"settings,omitempty"`
	CreatedAt string         `json:"created_at"`
}

// ToView 转成对外视图。
func (c Child) ToView() ChildView {
	return ChildView{
		ID:        c.ID.String(),
		Nickname:  c.Nickname,
		AvatarID:  c.AvatarID,
		BirthYM:   c.BirthYM,
		StageCode: c.StageCode,
		Settings:  c.Settings,
		CreatedAt: c.CreatedAt.UTC().Format(time.RFC3339),
	}
}

// CreateRequest 新建档案请求。
type CreateRequest struct {
	Nickname  string         `json:"nickname"`
	AvatarID  string         `json:"avatar_id"`
	BirthYM   string         `json:"birth_ym"`
	StageCode string         `json:"stage_code"`
	Settings  map[string]any `json:"settings"`
}

// Validate 校验新建参数。
func (r CreateRequest) Validate() error {
	var details []map[string]string
	add := func(field, reason string) {
		details = append(details, map[string]string{"field": field, "reason": reason})
	}

	if len(strings.TrimSpace(r.Nickname)) == 0 {
		add("nickname", "昵称不能为空")
	}
	if len([]rune(strings.TrimSpace(r.Nickname))) > 20 {
		add("nickname", "昵称不超过 20 个字")
	}
	if ym := strings.TrimSpace(r.BirthYM); ym != "" && !birthYMRe.MatchString(ym) {
		add("birth_ym", "出生年月格式为 YYYYMM")
	}

	if len(details) > 0 {
		return apperr.ValidationFailed("档案信息有误", details)
	}
	return nil
}

// NicknameOrDefault 返回昵称或默认头像标识。
func NicknameOrDefault(nickname, avatar string) (string, string) {
	nickname = strings.TrimSpace(nickname)
	avatar = strings.TrimSpace(avatar)
	if avatar == "" {
		avatar = "panda"
	}
	return nickname, avatar
}

// UpdateRequest 修改档案请求。字段为指针以便区分「未传」与「传空值」。
type UpdateRequest struct {
	Nickname  *string        `json:"nickname"`
	AvatarID  *string        `json:"avatar_id"`
	BirthYM   *string        `json:"birth_ym"`
	StageCode *string        `json:"stage_code"`
	Active    *bool          `json:"active"`
	Settings  map[string]any `json:"settings"`
}

// Validate 校验修改参数：至少改一个字段，昵称非空，年月格式正确。
func (r UpdateRequest) Validate() error {
	if r.Nickname == nil && r.AvatarID == nil && r.BirthYM == nil &&
		r.StageCode == nil && r.Active == nil && r.Settings == nil {
		return apperr.ValidationFailed("没有需要修改的内容", []map[string]string{
			{"field": "body", "reason": "至少提供一个待修改字段"},
		})
	}

	var details []map[string]string
	if r.Nickname != nil {
		n := strings.TrimSpace(*r.Nickname)
		if n == "" {
			details = append(details, map[string]string{"field": "nickname", "reason": "昵称不能为空"})
		} else if len([]rune(n)) > 20 {
			details = append(details, map[string]string{"field": "nickname", "reason": "昵称不超过 20 个字"})
		}
	}
	if r.BirthYM != nil {
		ym := strings.TrimSpace(*r.BirthYM)
		if ym != "" && !birthYMRe.MatchString(ym) {
			details = append(details, map[string]string{"field": "birth_ym", "reason": "出生年月格式为 YYYYMM"})
		}
	}

	if len(details) > 0 {
		return apperr.ValidationFailed("档案信息有误", details)
	}
	return nil
}
