/** 家长设置：护眼（休息间隔）、时长、题量、家长确认开关等。 */
import { api } from '../client'
import type { ParentSettings, ParentSettingsPatch } from '../types'

export const getSettings = () => api.get<ParentSettings>('/parent/settings')

/**
 * 部分更新：只传要改的字段。
 * 后端用指针区分「未提供」与「零值」—— daily_limit_min: 0 是「不限」，不是「不改」。
 */
export const updateSettings = (patch: ParentSettingsPatch) =>
  api.put<ParentSettings>('/parent/settings', patch)
