import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import { fileURLToPath, URL } from 'node:url'
export default defineConfig({plugins:[vue()],base:'/app/fnmusic-go/',build:{outDir:fileURLToPath(new URL('../internal/admin/assets',import.meta.url)),emptyOutDir:true}})
