import AutoImport from 'unplugin-auto-import/vite'
import Components from 'unplugin-vue-components/vite'
import { NaiveUiResolver } from 'unplugin-vue-components/resolvers'
import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import path from 'path'
import { execSync } from 'node:child_process'
import { existsSync, readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

const rootDir = path.dirname(fileURLToPath(import.meta.url))
const pkg = JSON.parse(readFileSync(path.join(rootDir, 'package.json'), 'utf8'))

// 构建版本 = package.json version + git 短 hash + 脏标记；git 不可用时退回 version。
// 工作区有未提交改动时必须标 -dirty：线上排查靠这行，不能把脏工作区冒充成某个 commit。
function gitOutput(args) {
  return execSync(`git ${args}`, {
    cwd: rootDir,
    stdio: ['ignore', 'pipe', 'ignore'],
    timeout: 2000
  }).toString().trim()
}

function buildVersion() {
  let commit
  try {
    commit = gitOutput('rev-parse --short HEAD')
  } catch (e) {
    console.warn('[vite] git hash unavailable, version falls back to package.json:', e.message)
    return pkg.version === '0.0.0' ? 'dev' : pkg.version
  }
  let dirty = ''
  try {
    // 只统计前端路径：前端构建的版本不该被 docs/ 或 server/ 的日常改动标脏
    if (gitOutput('status --porcelain -- .')) dirty = '-dirty'
  } catch (e) {
    // 判断不了干不干净就按脏算，宁可标脏也不谎报
    console.warn('[vite] git status unavailable, marking build as dirty:', e.message)
    dirty = '-dirty'
  }
  // 0.0.0 是占位版本，没有信息量，只留 hash
  const prefix = pkg.version === '0.0.0' ? '' : `v${pkg.version}+`
  return `${prefix}${commit}${dirty}`
}

// 手机验收要 https（getUserMedia / AudioWorklet 都要求安全上下文）。证书放 .certs/（已 gitignore），
// 有就用；没有就走 http（桌面连 localhost 本来就够）。
function devHttps() {
  const dir = path.join(rootDir, '.certs')
  const key = path.join(dir, 'dev-key.pem')
  const cert = path.join(dir, 'dev.pem')
  if (!existsSync(key) || !existsSync(cert)) return undefined
  console.log('[vite] https enabled (.certs/)')
  return { key: readFileSync(key), cert: readFileSync(cert) }
}

// https://vite.dev/config/
export default defineConfig({
  define: {
    // 仅构建期替换的裸全局（front-end 无 TS/ESLint）；运行时请当只读常量用
    __APP_VERSION__: JSON.stringify(buildVersion())
  },
  plugins: [
    vue(),
    AutoImport({
      imports: [
        'vue',
        {
          'naive-ui': [
            'useDialog',
            'useMessage',
            'useNotification',
            'useLoadingBar'
          ]
        }
      ]
    }),
    Components({
      resolvers: [NaiveUiResolver()]
    })
  ],
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src')
    }
  },
  optimizeDeps: {
    include: ['vue', 'vue-router', 'pinia', 'markdown-it']
  },
  server: {
    // 监听 0.0.0.0，手机同 Wi-Fi 直接开 http://<Mac 局域网 IP>:5173 验收
    // （语音输入要安全上下文，手机验收见 .certs/ + README 里 mkcert 那几句）
    host: true,
    https: devHttps(),
    proxy: {
      '/api': {
        target: 'http://localhost:8080',
        changeOrigin: true,
        // 语音 ASR 走 /api/voice/asr 的 WebSocket，Vite 不给 ws:true 就不转发 upgrade，会连不上
        ws: true
      },
      '/images': {
        target: 'http://localhost:8080',
        changeOrigin: true
      }
    }
  }
})
