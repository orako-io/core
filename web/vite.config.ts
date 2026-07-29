import { fileURLToPath } from 'node:url'
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// Supabase auth is SaaS-only (VITE_ORAKO_AUTH_MODE=oidc). Every other build —
// dev and self-host `local` — swaps the SDK for a stub so it never enters the
// community bundle. tsc still resolves the real package, so types are unaffected.
const isSupabaseBuild = process.env.VITE_ORAKO_AUTH_MODE === 'oidc'

// https://vitejs.dev/config/
export default defineConfig({
  define: { __SAAS__: JSON.stringify(isSupabaseBuild) },
  plugins: [react()],
  resolve: {
    alias: isSupabaseBuild
      ? {}
      : {
          '@supabase/supabase-js': fileURLToPath(
            new URL('./src/lib/supabase-stub.ts', import.meta.url),
          ),
        },
  },
  server: {
    proxy: {
      '/orako.v1.OrakoService': process.env.ORAKO_API_URL ?? 'http://localhost:8080',
      // SSE stream — without this, the dev server answers with index.html and
      // the realtime client mistakes it for a (dying) stream.
      '/events': process.env.ORAKO_API_URL ?? 'http://localhost:8080',
    },
  },
  build: {
    outDir: '../internal/infra/webui/dist',
    emptyOutDir: true,
  },
})
