/**
 * 与后端 DTO 逐字对齐的类型（设计文档 §2.3：初期手抄，后端接口稳定后可切 OpenAPI 生成）。
 *
 * 手抄的唯一纪律：**字段名必须与后端 json tag 一字不差**。
 * TS 结构类型不会在运行时校验，写错一个字母就是静默的 undefined，
 * 所以改动这里时请对照 server/internal/feature/<模块>/model.go。
 */

/** 列表接口的分页元信息（后端 meta.page）。 */
export type PageMeta = { offset: number; limit: number; total: number }

// ---------------------------------------------------------------- 认证

export type ParentView = {
  id: string
  email?: string
  phone?: string
  display_name: string
  /** 是否已设置家长 PIN（护眼锁屏解锁、切换孩子都要它） */
  has_pin: boolean
  created_at: string
}

export type Session = {
  access_token: string
  expires_in: number
  parent: ParentView
}

export type RegisterRequest = {
  email?: string
  phone?: string
  password: string
  display_name: string
  invite_code: string
}

/**
 * 登录请求。注意后端登录只认**一个** `account` 字段（邮箱或手机号都塞这里），
 * 不是 email/phone 两个字段 —— 这点与注册不同：
 * 注册要在库里分开存 email 与 phone，登录只需按账号查。
 */
export type LoginRequest = {
  account: string
  password: string
}

/** PIN 校验成功后的短期解锁令牌（typ=unlock，与 access 用途隔离）。 */
export type UnlockResult = {
  unlock_token: string
  expires_in: number
}

/** 扫码登录：桌面端拿到的二维码内容。 */
export type QRStartResult = {
  qr_token: string
  deep_link: string
  expires_in: number
}

export type QRScannedPayload = { status: string; device_hint: string; ip_prefix: string }
export type QRConfirmedPayload = { status: string; exchange_code: string; expires_in: number }
export type QRExpiredPayload = { status: string; reason?: string }

/** 扫码事件流的状态机（§六 /qr-login 四态 UI）。 */
export type QRPhase = 'loading' | 'waiting' | 'scanned' | 'confirmed' | 'expired' | 'failed'

// ---------------------------------------------------------------- 孩子档案

export type Child = {
  id: string
  nickname: string
  avatar_id: string
  birth_ym?: string
  stage_code?: string
  settings?: Record<string, unknown>
  created_at: string
}

export type ChildUpsert = {
  nickname: string
  avatar_id: string
  birth_ym?: string
  stage_code?: string
}

// ---------------------------------------------------------------- 练习

export type PracticeOption = {
  id: string
  label: string
  image?: string
}

export type Question = {
  type: string
  subject_code: string
  prompt: string
  prompt_sub?: string
  /** choice_audio / say 题的朗读内容 */
  audio_text?: string
  options?: PracticeOption[]
  match_left?: string[]
  match_right?: string[]
  order_items?: string[]
  layout?: string
  extra?: Record<string, unknown>
}

export type PlanItem = {
  kp_id: string
  subject_code: string
  kind: string
  code: string
  name: string
  reason: string
  difficulty: number
  planned_day?: number
  question_type?: string
}

/** 今日任务（§4.2 编排结果）。remaining_minutes 以服务端为准，前端倒计时只做体验。 */
export type TodayPlan = {
  child_id: string
  generated_at: string
  remaining_minutes: number
  daily_limit_min: number
  used_minutes: number
  backlog: boolean
  suggest_review: boolean
  due_count: number
  subjects: string[]
  items: PlanItem[]
  message?: string
}

export type ItemView = {
  id: string
  seq: number
  kp_id: string
  subject_code: string
  question_type: string
  /** pending | answered | skipped */
  state: string
  question: Question
  is_correct?: boolean
  /** 仅在作答后回传（答错看正确答案） */
  correct_answer?: string
  explain?: string
  used_hint: boolean
  elapsed_ms?: number
}

export type SessionView = {
  id: string
  child_id: string
  status: string
  started_at: string
  ended_at?: string
  item_count: number
  items: ItemView[]
}

export type MasteryBrief = {
  level: number
  next_review_in_minutes: number
  mastered: boolean
  entered_wrong_book: boolean
  cleared_wrong_book: boolean
  difficulty_changed: number
}

export type AnswerResult = {
  item_id: string
  /** null = 该题型不判分（描红 / 跟读） */
  is_correct: boolean | null
  correct_text?: string
  explain?: string
  state: string
  mastery?: MasteryBrief
  star_delta: number
}

export type SessionSummary = {
  session_id: string
  status: string
  duration_sec: number
  question_count: number
  answered_count: number
  correct_count: number
  skipped_count: number
  accuracy: number
  star_count: number
  completed_by: string
  passed: boolean
  message: string
}

// ---------------------------------------------------------------- 家长设置

export type ParentSettings = {
  daily_limit_min: number
  session_limit_min: number
  rest_interval_min: number
  require_parent_confirm: boolean
  pace_mode: string
  compare_children: boolean
  updated_at: string
}

/** 部分更新：只传要改的字段（后端用指针区分「未提供」与「零值」）。 */
export type ParentSettingsPatch = Partial<Omit<ParentSettings, 'updated_at'>>

// ---------------------------------------------------------------- 打印中心

export type PrintParamSpec = {
  name: string
  label: string
  type: 'int' | 'bool' | 'enum' | 'text'
  default: unknown
  min?: number
  max?: number
  options?: string[]
  help?: string
}

export type PrintTemplate = {
  code: string
  name: string
  category: string
  description: string
  paper_size: string
  double_sided: boolean
  /** 能否把纸上的作答反向写回掌握度（闪卡等纯教具为 false） */
  answerable: boolean
  needs_child: boolean
  params: PrintParamSpec[]
}

export type PrintJob = {
  id: string
  child_id: string
  template_code: string
  template_name: string
  title: string
  params: Record<string, unknown>
  /** created | queued | rendering | ready | failed */
  status: string
  /** 排版预估页数（PDF 就绪前展示用） */
  planned_pages: number
  /** PDF 渲染后的真实页数 */
  page_count: number
  pdf_ready: boolean
  marked_done: boolean
  marked_done_at?: string
  error_message?: string
  preview_url: string
  data_url: string
  pdf_url: string
  created_at: string
}

export type PrintItem = {
  seq: number
  main: string
  sub?: string
  answer?: string
  hint?: string
  extra?: string[]
  kp_id?: string
  style?: string
}

export type PrintSheet = {
  title: string
  note?: string
  head: string[]
  rows: string[][]
}

export type PrintPayload = {
  template_code: string
  template_name: string
  render_version: number
  title: string
  subtitle: string
  meta: {
    child_name: string
    child_stage: string
    date_label: string
    watermark?: string
    paper: string
    copies: number
    generated_at: string
    source: string
  }
  options: {
    font_size: number
    with_pinyin: boolean
    with_answer: boolean
    per_page: number
    columns: number
    blank_lines: boolean
    show_stroke: boolean
  }
  items: PrintItem[]
  answer_items?: PrintItem[]
  sheets?: PrintSheet[]
  footer: string
  warnings?: string[]
}

// ---------------------------------------------------------------- 报表（§4.7 / §4.9）

export type SubjectProgress = {
  subject_code: string
  subject_name: string
  mastered: number
  planned_total: number
  due: number
}

export type OverviewTotals = {
  duration_sec: number
  question_count: number
  correct_count: number
  accuracy: number
  star_count: number
  active_days: number
  session_count: number
  avg_accuracy: number
}

export type ReportOverview = {
  child_id: string
  mastered_total: number
  subjects: SubjectProgress[]
  totals: OverviewTotals
  due_total: number
  streak_days: number
  badges_earned: number
  badges_total: number
  deviation_days: number
  attempts_per_mastery: number
  generated_at: string
}

export type TrendPoint = {
  date: string
  duration_sec: number
  question_count: number
  correct_count: number
  accuracy: number
  new_mastered: number
  star_count: number
  planned_new: number
  actual_new: number
  repeat_count: number
  cum_planned: number
  cum_actual: number
  deviation_days: number
  passed: boolean
  parent_confirmed: boolean
}

export type ReportTrend = {
  days: number
  from: string
  to: string
  points: TrendPoint[]
  note?: string
}

/** 建议附带的动作类型（与后端 report/actions.go 的常量一一对应）。 */
export type SuggestionActionType =
  | 'review_day'
  | 'tune_quota'
  | 'assign_practice'
  | 'lower_difficulty'
  | 'balance_subjects'

export type SuggestionAction = {
  type: SuggestionActionType | string
  label: string
}

export type ReportSuggestion = {
  code: string
  /** info | notice */
  severity: string
  subject?: string
  title: string
  detail: string
  data?: Record<string, unknown>
  actions: SuggestionAction[]
}

export type ReportSuggestions = {
  child_id: string
  suggestions: ReportSuggestion[]
  generated_at: string
}

export type SuggestionActionRequest = {
  type: string
  kp_id?: string
  subject?: string
}

export type SuggestionActionResult = {
  type: string
  applied: boolean
  detail: string
  data?: Record<string, unknown>
}

/** PDF 导出结果：复用打印任务，前端轮询 job_id 到 ready 再下载。 */
export type ReportExportPdfResult = {
  job_id: string
  status: string
  pdf_url: string
  data_url: string
}
