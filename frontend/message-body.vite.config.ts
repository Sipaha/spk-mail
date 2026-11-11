// Test-only server: the production entry point never imports this harness.
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import { resolve } from 'node:path'
import type { ServerResponse } from 'node:http'

export default defineConfig({
  root: resolve(import.meta.dirname, '..'),
  cacheDir: resolve(import.meta.dirname, 'node_modules/.vite-message-body'),
  plugins: [react(), {
    name: 'controlled-message-resources',
    configureServer(server) {
      const images: ServerResponse[] = []
      const checkpoints: ServerResponse[] = []
      let report: unknown = null
      let released = false
      const respond = (image: ServerResponse) => {
        image.setHeader('Content-Type', 'image/svg+xml')
        image.setHeader('Cache-Control', 'no-store')
        image.end('<svg xmlns="http://www.w3.org/2000/svg" width="10" height="180"/>')
      }
      server.middlewares.use((req, res, next) => {
        if (req.url?.startsWith('/probe.img')) {
          if (released) { respond(res); return }
          images.push(res)
          res.on('close', () => { const i = images.indexOf(res); if (i >= 0) images.splice(i, 1) })
          return
        }
        if (req.url === '/release') {
          res.setHeader('Access-Control-Allow-Origin', '*')
          released = true
          for (const image of images.splice(0)) respond(image)
          res.end('released')
          return
        }
        if (req.url === '/report') {
          // Reports use localhost, a separate connection pool from the seven
          // intentionally stalled 127.0.0.1 image requests. No operator data.
          res.setHeader('Access-Control-Allow-Origin', '*')
          if (req.method === 'POST') {
            let body = ''
            req.on('data', chunk => { body += chunk })
            req.on('end', () => { report = JSON.parse(body); checkpoints.push(res) })
          } else {
            res.setHeader('Content-Type', 'application/json')
            res.end(JSON.stringify(report))
          }
          return
        }
        if (req.url === '/ack') {
          for (const checkpoint of checkpoints.splice(0)) checkpoint.end('ok')
          res.end('ok')
          return
        }
        next()
      })
    },
  }],
  css: { postcss: import.meta.dirname },
  resolve: {
    dedupe: ['react', 'react-dom'],
    alias: {
      react: resolve(import.meta.dirname, 'node_modules/react'),
      'react-dom': resolve(import.meta.dirname, 'node_modules/react-dom'),
    },
  },
  server: { host: '127.0.0.1', port: 5186, strictPort: true },
})
