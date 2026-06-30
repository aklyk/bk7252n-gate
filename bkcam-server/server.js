const fs = require('fs')
const http = require('http')
const path = require('path')
const { URL } = require('url')

const PPPP = require('../PPPP/pppp')

const ROOT = __dirname
const CONFIG_PATH = process.env.BKCAM_CONFIG || path.join(ROOT, 'config.json')
const config = JSON.parse(fs.readFileSync(CONFIG_PATH, 'utf8'))
const serverConfig = config.server || {}
const PUBLIC_HOST = serverConfig.host || '127.0.0.1'
const PORT = Number(process.env.PORT || serverConfig.port || 8088)
const BIND = serverConfig.bind || '0.0.0.0'
const CAMERA_ID_RE = /^[A-Za-z0-9_-]+$/
const AUDIO_SAMPLE_RATE = 8000
const AUDIO_CHANNELS = 1
const AUDIO_BYTES_PER_SAMPLE = 2
const AUDIO_SILENCE_CHUNK = Buffer.alloc(AUDIO_SAMPLE_RATE * AUDIO_CHANNELS * AUDIO_BYTES_PER_SAMPLE)
const EMPTY_CLIENT_TTL_MS = 15000

function nowIso() {
  return new Date().toISOString()
}

function escapeHtml(s) {
  return String(s).replace(/[&<>"']/g, (c) => ({
    '&': '&amp;',
    '<': '&lt;',
    '>': '&gt;',
    '"': '&quot;',
    "'": '&#39;',
  }[c]))
}

function writeJson(res, status, data) {
  const body = Buffer.from(JSON.stringify(data, null, 2))
  res.writeHead(status, {
    'content-type': 'application/json; charset=utf-8',
    'cache-control': 'no-store',
    'content-length': body.length,
  })
  res.end(body)
}

function writeText(res, status, text, type = 'text/plain; charset=utf-8') {
  const body = Buffer.from(text)
  res.writeHead(status, {
    'content-type': type,
    'cache-control': 'no-store',
    'content-length': body.length,
  })
  res.end(body)
}

function writeWavHeader(res) {
  const h = Buffer.alloc(44)
  h.write('RIFF', 0)
  h.writeUInt32LE(0xffffffff, 4)
  h.write('WAVE', 8)
  h.write('fmt ', 12)
  h.writeUInt32LE(16, 16)
  h.writeUInt16LE(1, 20)
  h.writeUInt16LE(AUDIO_CHANNELS, 22)
  h.writeUInt32LE(AUDIO_SAMPLE_RATE, 24)
  h.writeUInt32LE(AUDIO_SAMPLE_RATE * AUDIO_CHANNELS * AUDIO_BYTES_PER_SAMPLE, 28)
  h.writeUInt16LE(AUDIO_CHANNELS * AUDIO_BYTES_PER_SAMPLE, 32)
  h.writeUInt16LE(16, 34)
  h.write('data', 36)
  h.writeUInt32LE(0xffffffff, 40)
  res.write(h)
}

class CameraRuntime {
  constructor(camera) {
    this.camera = camera
    this.id = camera.id
    this.pppp = null
    this.videoClients = new Set()
    this.audioClients = new Set()
    this.latestFrame = null
    this.lastVideoAt = null
    this.lastAudioAt = null
    this.connectedAt = null
    this.startedAt = null
    this.restartCount = 0
    this.videoFrames = 0
    this.audioFrames = 0
    this.lastError = null
    this.lastCommand = null
    this.monitor = null
    this.streamMaintainer = null
  }

  start() {
    if (this.pppp || this.camera.enabled === false) return
    this.startedAt = Date.now()
    this.lastError = null

    this.pppp = new PPPP({
      broadcastip: this.camera.discovery || this.camera.ip,
      thisip: this.camera.localAddress,
      psk: this.camera.psk || 'SHIX',
      username: this.camera.username || 'admin',
      password: this.camera.password || '6666',
      verbose: Boolean(this.camera.verbose),
    })

    this.pppp.on('connected', (peer) => {
      this.connectedAt = Date.now()
      this.lastError = null
      console.log(`${nowIso()} camera ${this.id} connected ${peer.address}:${peer.port}`)
      setTimeout(() => this.requestStreams(true), 200)
      setTimeout(() => this.safeCall('sendCMDgetParams'), 800)
    })

    this.pppp.on('videoFrame', ({ frame }) => {
      this.latestFrame = frame
      this.lastVideoAt = Date.now()
      this.videoFrames += 1
      this.broadcastVideoFrame(frame)
    })

    this.pppp.on('audioFrame', ({ frame }) => {
      this.lastAudioAt = Date.now()
      this.audioFrames += 1
      this.broadcastAudioFrame(frame)
    })

    this.pppp.on('cmd', (cmd) => {
      this.lastCommand = String(cmd).trim()
    })

    this.pppp.on('log', (line) => {
      if (this.camera.verbose) console.log(`${nowIso()} camera ${this.id}: ${line}`)
    })

    this.pppp.on('socketError', (err) => {
      this.lastError = err.message
      this.restart(`socket error: ${err.message}`)
    })

    this.monitor = setInterval(() => this.checkHealth(), 5000)
    this.streamMaintainer = setInterval(() => this.maintainStreams(), 1000)
  }

  safeCall(method) {
    try {
      if (method.startsWith('sendCMD') && !this.connectedAt) return false
      if (!this.pppp || typeof this.pppp[method] !== 'function') return false
      this.pppp[method]()
      return true
    } catch (err) {
      this.lastError = err.message
      console.error(`${nowIso()} camera ${this.id} ${method} failed: ${err.stack || err}`)
      return false
    }
  }

  checkHealth() {
    const now = Date.now()
    if (!this.pppp) return
    if (!this.connectedAt && this.startedAt && now - this.startedAt > 20000) {
      this.restart('connect timeout')
    }
  }

  maintainStreams() {
    this.sweepClients()
    if (!this.pppp || !this.connectedAt) return
    const now = Date.now()
    const hasVideoDemand = this.videoClients.size > 0 || !this.latestFrame
    const hasAudioDemand = this.audioClients.size > 0
    const staleVideo = hasVideoDemand && (!this.lastVideoAt || now - this.lastVideoAt > 5000)
    const staleAudio = hasAudioDemand && (!this.lastAudioAt || now - this.lastAudioAt > 2500)

    if (staleVideo || staleAudio) this.requestStreams(false)
    if (this.videoClients.size > 0 && this.latestFrame && (!this.lastVideoAt || now - this.lastVideoAt > 3000)) {
      this.broadcastVideoFrame(this.latestFrame)
    }
    if (hasAudioDemand && (!this.lastAudioAt || now - this.lastAudioAt > 1000)) {
      this.broadcastAudioFrame(AUDIO_SILENCE_CHUNK)
    }
  }

  requestStreams(forceVideo = false) {
    const wantsVideo = forceVideo || this.videoClients.size > 0 || !this.latestFrame
    const wantsAudio = this.audioClients.size > 0

    if (wantsVideo && wantsAudio && this.safeCall('sendCMDrequestAv')) return
    if (wantsVideo) this.safeCall('sendCMDrequestVideo1')
    if (wantsAudio) this.safeCall('sendCMDrequestAudio')
  }

  restart(reason) {
    this.lastError = reason
    this.restartCount += 1
    console.warn(`${nowIso()} camera ${this.id} restarting: ${reason}`)
    this.stop(false)
    setTimeout(() => this.start(), 1500)
  }

  stop(clearClients = true) {
    if (this.monitor) {
      clearInterval(this.monitor)
      this.monitor = null
    }
    if (this.streamMaintainer) {
      clearInterval(this.streamMaintainer)
      this.streamMaintainer = null
    }
    if (this.pppp) {
      try { this.pppp.destroy() } catch (err) {}
      this.pppp = null
    }
    this.connectedAt = null
    this.startedAt = null
    if (clearClients) {
      for (const client of Array.from(this.videoClients)) client.cleanup()
      for (const client of Array.from(this.audioClients)) client.cleanup()
      this.videoClients.clear()
      this.audioClients.clear()
    }
  }

  registerClient(set, req, res, kind) {
    const client = {
      kind,
      req,
      res,
      socket: req.socket,
      startedAt: Date.now(),
      lastWriteAt: null,
      bytesWritten: 0,
      closed: false,
      cleanup: null,
    }

    const cleanup = () => {
      if (client.closed) return
      client.closed = true
      set.delete(client)
      req.off('aborted', cleanup)
      req.off('error', cleanup)
      res.off('close', cleanup)
      res.off('error', cleanup)
      client.socket.off('close', cleanup)
      client.socket.off('error', cleanup)
      if (!res.destroyed && !res.writableEnded) {
        try { res.end() } catch (err) {}
      }
    }

    client.cleanup = cleanup
    set.add(client)
    req.on('aborted', cleanup)
    req.on('error', cleanup)
    res.on('close', cleanup)
    res.on('error', cleanup)
    client.socket.on('close', cleanup)
    client.socket.on('error', cleanup)
    return client
  }

  writeToClient(client, set, chunks, maxBufferedBytes) {
    const res = client.res
    if (client.closed || res.destroyed || res.writableEnded || client.socket.destroyed) {
      client.cleanup()
      return false
    }

    if (res.writableLength > maxBufferedBytes) {
      client.cleanup()
      return false
    }

    try {
      for (const chunk of chunks) {
        client.bytesWritten += chunk.length || Buffer.byteLength(String(chunk))
        res.write(chunk)
      }
      client.lastWriteAt = Date.now()
      return true
    } catch (err) {
      client.cleanup()
      return false
    }
  }

  sweepClients() {
    const now = Date.now()
    for (const client of Array.from(this.videoClients)) {
      if (
        client.closed ||
        client.res.destroyed ||
        client.res.writableEnded ||
        client.socket.destroyed ||
        (client.bytesWritten === 0 && now - client.startedAt > EMPTY_CLIENT_TTL_MS)
      ) {
        client.cleanup()
      }
    }
    for (const client of Array.from(this.audioClients)) {
      if (
        client.closed ||
        client.res.destroyed ||
        client.res.writableEnded ||
        client.socket.destroyed ||
        (client.bytesWritten === 0 && now - client.startedAt > EMPTY_CLIENT_TTL_MS)
      ) {
        client.cleanup()
      }
    }
  }

  broadcastVideoFrame(frame) {
    const header = Buffer.from(
      `--frame\r\nContent-Type: image/jpeg\r\nContent-Length: ${frame.length}\r\n\r\n`
    )
    for (const client of Array.from(this.videoClients)) {
      this.writeToClient(client, this.videoClients, [header, frame, Buffer.from('\r\n')], 2 * 1024 * 1024)
    }
  }

  broadcastAudioFrame(frame) {
    for (const client of Array.from(this.audioClients)) {
      this.writeToClient(client, this.audioClients, [frame], 512 * 1024)
    }
  }

  addVideoClient(req, res) {
    this.start()
    res.writeHead(200, {
      'content-type': 'multipart/x-mixed-replace; boundary=frame',
      'cache-control': 'no-store, no-cache, must-revalidate, private',
      'pragma': 'no-cache',
      'connection': 'close',
      'x-accel-buffering': 'no',
    })
    const client = this.registerClient(this.videoClients, req, res, 'video')
    if (this.latestFrame) {
      const frame = this.latestFrame
      this.writeToClient(client, this.videoClients, [
        Buffer.from(`--frame\r\nContent-Type: image/jpeg\r\nContent-Length: ${frame.length}\r\n\r\n`),
        frame,
        Buffer.from('\r\n'),
      ], 2 * 1024 * 1024)
    }
    this.requestStreams(false)
  }

  addAudioClient(req, res, format = 'wav') {
    this.start()
    const isRaw = format === 'raw'
    res.writeHead(200, {
      'content-type': isRaw ? 'application/octet-stream' : 'audio/wav',
      'cache-control': 'no-store, no-cache, must-revalidate, private',
      'connection': 'close',
      'x-accel-buffering': 'no',
      'x-audio-format': 'pcm_s16le',
      'x-audio-sample-rate': String(AUDIO_SAMPLE_RATE),
      'x-audio-channels': String(AUDIO_CHANNELS),
    })
    if (!isRaw) writeWavHeader(res)
    this.registerClient(this.audioClients, req, res, isRaw ? 'audio.raw' : 'audio.wav')
    this.requestStreams(false)
  }

  writeSnapshot(res) {
    this.start()
    if (!this.latestFrame) {
      writeJson(res, 503, { error: 'snapshot not ready', camera: this.id })
      return
    }
    res.writeHead(200, {
      'content-type': 'image/jpeg',
      'cache-control': 'no-store',
      'content-length': this.latestFrame.length,
    })
    res.end(this.latestFrame)
  }

  status(baseUrl) {
    return {
      id: this.id,
      name: this.camera.name || this.id,
      enabled: this.camera.enabled !== false,
      ip: this.camera.ip,
      connected: Boolean(this.connectedAt),
      connectedAt: this.connectedAt ? new Date(this.connectedAt).toISOString() : null,
      lastVideoAt: this.lastVideoAt ? new Date(this.lastVideoAt).toISOString() : null,
      lastAudioAt: this.lastAudioAt ? new Date(this.lastAudioAt).toISOString() : null,
      lastVideoAgeMs: this.lastVideoAt ? Date.now() - this.lastVideoAt : null,
      lastAudioAgeMs: this.lastAudioAt ? Date.now() - this.lastAudioAt : null,
      videoFrames: this.videoFrames,
      audioFrames: this.audioFrames,
      videoClients: this.videoClients.size,
      audioClients: this.audioClients.size,
      restartCount: this.restartCount,
      lastError: this.lastError,
      lastCommand: this.lastCommand,
      urls: {
        page: `${baseUrl}/cam/${this.id}`,
        video: `${baseUrl}/cam/${this.id}/video.mjpg`,
        audio: `${baseUrl}/cam/${this.id}/audio.wav`,
        audioRaw: `${baseUrl}/cam/${this.id}/audio.raw`,
        snapshot: `${baseUrl}/cam/${this.id}/snapshot.jpg`,
      },
    }
  }
}

const runtimes = new Map()
for (const camera of config.cameras || []) {
  if (!camera.id) throw new Error('camera.id is required')
  if (!CAMERA_ID_RE.test(camera.id)) throw new Error(`camera.id must match ${CAMERA_ID_RE}: ${camera.id}`)
  if (runtimes.has(camera.id)) throw new Error(`duplicate camera.id: ${camera.id}`)
  const runtime = new CameraRuntime(camera)
  runtimes.set(camera.id, runtime)
  if (camera.enabled !== false) runtime.start()
}

function baseUrl(req) {
  const proto = req.headers['x-forwarded-proto'] || 'http'
  const host = req.headers.host || `${PUBLIC_HOST}:${PORT}`
  return `${proto}://${host}`
}

function allStatuses(req) {
  const base = baseUrl(req)
  return Array.from(runtimes.values()).map((r) => r.status(base))
}

function renderPage(req, cameraId = null) {
  const statuses = allStatuses(req)
  const cameras = cameraId ? statuses.filter((c) => c.id === cameraId) : statuses
  const cards = cameras.map((c) => `
    <section class="camera" data-camera-id="${escapeHtml(c.id)}">
      <header class="camera-head">
        <div>
          <h2>${escapeHtml(c.name)}</h2>
          <p class="meta">${escapeHtml(c.ip || '')} · ${escapeHtml(c.id)}</p>
        </div>
        <span class="state ${c.connected ? 'ok' : 'warn'}">${c.connected ? 'online' : 'connecting'}</span>
      </header>
      <div class="media">
        <img src="/cam/${encodeURIComponent(c.id)}/video.mjpg" alt="${escapeHtml(c.name)} live video">
      </div>
      <div class="toolbar">
        <button data-audio="${escapeHtml(c.id)}">Audio</button>
        <a href="/cam/${encodeURIComponent(c.id)}/snapshot.jpg" target="_blank">Snapshot</a>
        <a href="/cam/${encodeURIComponent(c.id)}/video.mjpg" target="_blank">MJPEG</a>
        <a href="/cam/${encodeURIComponent(c.id)}/audio.wav" target="_blank">WAV</a>
      </div>
      <audio id="audio-${escapeHtml(c.id)}" controls preload="none" hidden></audio>
      <dl class="stats">
        <div><dt>Video</dt><dd data-field="${escapeHtml(c.id)}:videoFrames">${c.videoFrames}</dd></div>
        <div><dt>Audio</dt><dd data-field="${escapeHtml(c.id)}:audioFrames">${c.audioFrames}</dd></div>
        <div><dt>Clients</dt><dd data-field="${escapeHtml(c.id)}:clients">${c.videoClients}/${c.audioClients}</dd></div>
        <div><dt>Restarts</dt><dd data-field="${escapeHtml(c.id)}:restartCount">${c.restartCount}</dd></div>
      </dl>
    </section>
  `).join('')

  return `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>BKCam</title>
  <style>
    :root { color-scheme: light dark; --bg: #f5f6f8; --fg: #15171a; --muted: #667085; --line: #d8dde5; --panel: #fff; --ok: #12805c; --warn: #a15c00; }
    @media (prefers-color-scheme: dark) { :root { --bg: #101214; --fg: #eef1f4; --muted: #98a2b3; --line: #2b3038; --panel: #181b20; --ok: #35b27f; --warn: #d99a36; } }
    * { box-sizing: border-box; }
    body { margin: 0; background: var(--bg); color: var(--fg); font: 14px/1.4 system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; }
    header.top { height: 56px; display: flex; align-items: center; justify-content: space-between; padding: 0 20px; border-bottom: 1px solid var(--line); background: var(--panel); position: sticky; top: 0; z-index: 2; }
    h1 { font-size: 18px; margin: 0; font-weight: 650; }
    nav { display: flex; gap: 10px; align-items: center; }
    a, button { color: inherit; }
    nav a, .toolbar a, button { border: 1px solid var(--line); background: var(--panel); text-decoration: none; padding: 7px 10px; border-radius: 6px; font: inherit; cursor: pointer; }
    main { width: min(1440px, 100%); margin: 0 auto; padding: 16px; }
    .grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(360px, 1fr)); gap: 16px; }
    .camera { background: var(--panel); border: 1px solid var(--line); border-radius: 8px; overflow: hidden; }
    .camera-head { display: flex; align-items: center; justify-content: space-between; gap: 12px; padding: 12px 14px; border-bottom: 1px solid var(--line); }
    h2 { font-size: 16px; margin: 0 0 2px; font-weight: 650; }
    .meta { margin: 0; color: var(--muted); font-size: 12px; }
    .state { font-size: 12px; text-transform: uppercase; letter-spacing: .04em; font-weight: 700; }
    .state.ok { color: var(--ok); }
    .state.warn { color: var(--warn); }
    .media { aspect-ratio: 4 / 3; background: #050505; display: grid; place-items: center; }
    .media img { width: 100%; height: 100%; object-fit: contain; display: block; }
    .toolbar { display: flex; flex-wrap: wrap; gap: 8px; padding: 10px 12px; border-top: 1px solid var(--line); }
    audio:not([hidden]) { display: block; width: calc(100% - 24px); margin: 0 12px 12px; height: 36px; }
    .stats { margin: 0; padding: 10px 12px 12px; display: grid; grid-template-columns: repeat(4, 1fr); gap: 8px; border-top: 1px solid var(--line); }
    .stats div { min-width: 0; }
    dt { color: var(--muted); font-size: 11px; margin-bottom: 2px; }
    dd { margin: 0; font-variant-numeric: tabular-nums; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
    @media (max-width: 520px) { main { padding: 10px; } .grid { grid-template-columns: 1fr; } header.top { padding: 0 12px; } nav a { display: none; } .stats { grid-template-columns: repeat(2, 1fr); } }
  </style>
</head>
<body>
  <header class="top">
    <h1>BKCam</h1>
    <nav>
      <a href="/api/status">Status</a>
      <a href="/frigate.yml">Frigate</a>
      <a href="/go2rtc.yml">go2rtc</a>
    </nav>
  </header>
  <main><div class="grid">${cards}</div></main>
  <script>
    const audioPlayers = new Map()

    function joinBytes(a, b) {
      if (!a || a.length === 0) return b
      const out = new Uint8Array(a.length + b.length)
      out.set(a, 0)
      out.set(b, a.length)
      return out
    }

    function schedulePcm(state, bytes) {
      const samples = Math.floor(bytes.byteLength / 2)
      if (!samples) return
      const ctx = state.ctx
      const buffer = ctx.createBuffer(1, samples, ${AUDIO_SAMPLE_RATE})
      const out = buffer.getChannelData(0)
      const view = new DataView(bytes.buffer, bytes.byteOffset, samples * 2)
      for (let i = 0; i < samples; i += 1) {
        out[i] = Math.max(-1, view.getInt16(i * 2, true) / 32768)
      }
      const source = ctx.createBufferSource()
      source.buffer = buffer
      source.connect(ctx.destination)
      const floor = ctx.currentTime + 0.06
      if (state.nextTime < floor) state.nextTime = floor
      source.start(state.nextTime)
      state.nextTime += buffer.duration
    }

    function stopAudio(id) {
      const state = audioPlayers.get(id)
      if (!state) return
      audioPlayers.delete(id)
      state.active = false
      state.controller.abort()
      state.ctx.close().catch(() => {})
      if (state.button) state.button.textContent = 'Audio'
    }

    async function startAudio(id, button) {
      if (audioPlayers.has(id)) {
        stopAudio(id)
        return
      }

      const AudioContextCtor = window.AudioContext || window.webkitAudioContext
      if (!AudioContextCtor) {
        const audio = document.getElementById('audio-' + id)
        audio.hidden = false
        if (!audio.src) audio.src = '/cam/' + encodeURIComponent(id) + '/audio.wav'
        await audio.play()
        return
      }

      const ctx = new AudioContextCtor()
      await ctx.resume()
      const controller = new AbortController()
      const state = { active: true, button, controller, ctx, nextTime: ctx.currentTime + 0.12 }
      audioPlayers.set(id, state)
      button.textContent = 'Stop'

      try {
        const res = await fetch('/cam/' + encodeURIComponent(id) + '/audio.raw', {
          cache: 'no-store',
          signal: controller.signal
        })
        if (!res.ok || !res.body) throw new Error('audio stream unavailable')
        const reader = res.body.getReader()
        let pending = new Uint8Array(0)
        while (state.active) {
          const chunk = await reader.read()
          if (chunk.done) break
          const merged = joinBytes(pending, chunk.value)
          const alignedLength = merged.length - (merged.length % 2)
          if (alignedLength > 0) schedulePcm(state, merged.subarray(0, alignedLength))
          pending = merged.subarray(alignedLength)
        }
      } catch (err) {
        if (state.active && err.name !== 'AbortError') {
          const audio = document.getElementById('audio-' + id)
          audio.hidden = false
          if (!audio.src) audio.src = '/cam/' + encodeURIComponent(id) + '/audio.wav'
          audio.play().catch(() => {})
        }
      } finally {
        if (audioPlayers.get(id) === state) stopAudio(id)
      }
    }

    document.addEventListener('click', (ev) => {
      const id = ev.target && ev.target.dataset && ev.target.dataset.audio
      if (!id) return
      startAudio(id, ev.target).catch(() => {})
    })
    async function poll() {
      try {
        const res = await fetch('/api/status', { cache: 'no-store' })
        const data = await res.json()
        for (const cam of data.cameras) {
          const el = document.querySelector('[data-camera-id="' + cam.id + '"] .state')
          if (el) { el.textContent = cam.connected ? 'online' : 'connecting'; el.className = 'state ' + (cam.connected ? 'ok' : 'warn') }
          const fields = {
            videoFrames: cam.videoFrames,
            audioFrames: cam.audioFrames,
            clients: cam.videoClients + '/' + cam.audioClients,
            restartCount: cam.restartCount
          }
          for (const [k, v] of Object.entries(fields)) {
            const f = document.querySelector('[data-field="' + cam.id + ':' + k + '"]')
            if (f) f.textContent = v
          }
        }
      } catch (_) {}
    }
    setInterval(poll, 3000)
  </script>
</body>
</html>`
}

function renderFrigate(base) {
  const enabled = Array.from(runtimes.values()).filter((r) => r.camera.enabled !== false)
  const streamLines = enabled.map((r) =>
    `    ${r.id}: ffmpeg:${base}/cam/${r.id}/video.mjpg#video=h264`
  ).join('\n')
  const cameraLines = enabled.map((r) => `  ${r.id}:
    ffmpeg:
      inputs:
        - path: rtsp://127.0.0.1:8554/${r.id}
          input_args: preset-rtsp-restream
          roles:
            - detect
            - record
    detect:
      width: ${r.camera.width || 640}
      height: ${r.camera.height || 480}`).join('\n')
  return `go2rtc:
  streams:
${streamLines}

cameras:
${cameraLines}
`
}

function renderGo2rtc(base) {
  const enabled = Array.from(runtimes.values()).filter((r) => r.camera.enabled !== false)
  return `streams:
${enabled.map((r) => `  ${r.id}: ffmpeg:${base}/cam/${r.id}/video.mjpg#video=h264`).join('\n')}
`
}

function route(req, res) {
  const parsed = new URL(req.url, `http://${req.headers.host || 'localhost'}`)
  const pathname = parsed.pathname

  if (pathname === '/') {
    writeText(res, 200, renderPage(req), 'text/html; charset=utf-8')
    return
  }
  if (pathname === '/api/status') {
    writeJson(res, 200, {
      server: { host: PUBLIC_HOST, port: PORT, bind: BIND },
      cameras: allStatuses(req),
    })
    return
  }
  if (pathname === '/frigate.yml') {
    writeText(res, 200, renderFrigate(baseUrl(req)), 'text/yaml; charset=utf-8')
    return
  }
  if (pathname === '/go2rtc.yml') {
    writeText(res, 200, renderGo2rtc(baseUrl(req)), 'text/yaml; charset=utf-8')
    return
  }

  const match = pathname.match(/^\/cam\/([^/]+)(?:\/([^/]+))?$/)
  if (!match) {
    writeJson(res, 404, { error: 'not found' })
    return
  }

  const id = decodeURIComponent(match[1])
  const action = match[2] || ''
  const runtime = runtimes.get(id)
  if (!runtime) {
    writeJson(res, 404, { error: 'unknown camera', id })
    return
  }

  if (!action) {
    writeText(res, 200, renderPage(req, id), 'text/html; charset=utf-8')
  } else if (action === 'video.mjpg') {
    runtime.addVideoClient(req, res)
  } else if (action === 'audio.wav') {
    runtime.addAudioClient(req, res, 'wav')
  } else if (action === 'audio.raw') {
    runtime.addAudioClient(req, res, 'raw')
  } else if (action === 'snapshot.jpg') {
    runtime.writeSnapshot(res)
  } else {
    writeJson(res, 404, { error: 'unknown camera endpoint', id, action })
  }
}

const server = http.createServer((req, res) => {
  try {
    route(req, res)
  } catch (err) {
    console.error(`${nowIso()} request failed: ${err.stack || err}`)
    if (!res.headersSent) writeJson(res, 500, { error: 'internal error' })
    else res.destroy()
  }
})

server.listen(PORT, BIND, () => {
  console.log(`${nowIso()} BKCam listening on http://${PUBLIC_HOST}:${PORT}`)
  console.log(`${nowIso()} config ${CONFIG_PATH}`)
})

function shutdown() {
  console.log(`${nowIso()} shutting down`)
  for (const runtime of runtimes.values()) runtime.stop()
  server.close(() => process.exit(0))
  setTimeout(() => process.exit(1), 3000).unref()
}

process.on('SIGINT', shutdown)
process.on('SIGTERM', shutdown)
