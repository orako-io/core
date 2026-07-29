/// <reference types="vite/client" />

interface ImportMetaEnv {
  readonly VITE_SUPABASE_URL?: string
  readonly VITE_SUPABASE_PUBLISHABLE_KEY?: string
  readonly VITE_ORAKO_AUTH_MODE?: string
}

interface ImportMeta {
  readonly env: ImportMetaEnv
}

declare const __SAAS__: boolean
