/** 孩子档案 CRUD（所有查询都由后端按 parent_id 过滤，越权返回 404）。 */
import { api } from '../client'
import type { Child, ChildUpsert } from '../types'

export const listChildren = () => api.get<Child[]>('/children')

export const createChild = (body: ChildUpsert) => api.post<Child>('/children', body)

export const getChild = (id: string) => api.get<Child>(`/children/${id}`)

export const updateChild = (id: string, body: Partial<ChildUpsert>) =>
  api.put<Child>(`/children/${id}`, body)

/** 归档（软删除，历史学习记录保留）。 */
export const archiveChild = (id: string) =>
  api.del<{ archived: boolean; id: string }>(`/children/${id}`)
