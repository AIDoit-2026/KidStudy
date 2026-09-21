/// <reference types="vite/client" />

interface ImportMetaEnv {
  /** 后端 API 基地址（含 /api/v1 前缀），来自 VITE_API_BASE_URL */
  readonly VITE_API_BASE_URL: string
}

interface ImportMeta {
  readonly env: ImportMetaEnv
}
