import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import { readFileSync } from 'node:fs'
import { fileURLToPath, URL } from 'node:url'
const manifest = readFileSync(new URL('../packaging/fpk/manifest.in', import.meta.url), 'utf8')
const version = manifest.match(/^version\s*=\s*(\S+)/m)?.[1]
if (!version) throw new Error('manifest version is missing')
export default defineConfig({define:{__APP_VERSION__:JSON.stringify(version)},plugins:[vue()],base:'/app/fnmusic-go/',build:{outDir:fileURLToPath(new URL('../internal/admin/assets',import.meta.url)),emptyOutDir:true}})
