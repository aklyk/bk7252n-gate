const fs = require('fs')
const http = require('http')
const path = require('path')
const { URL } = require('url')

const PPPP = require('../PPPP/pppp')

const ROOT = __dirname
const CONFIG_PATH = process.env.BKCAM_CONFIG || path.join(ROOT, 'config.json')
const EXAMPLE_CONFIG_PATH = path.join(ROOT, 'config.example.json')
let config = loadConfig()
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
const METRIC_WINDOW_MS = 5000
const ONLINE_TRAFFIC_MS = 15000
const STALE_TRAFFIC_MS = 45000
const TRAFFIC_RESTART_MS = 60000

function loadConfig() {
  const fallback = { server: { host: '127.0.0.1', port: 8088, bind: '0.0.0.0' }, cameras: [] }
  const source = fs.existsSync(CONFIG_PATH)
    ? CONFIG_PATH
    : (fs.existsSync(EXAMPLE_CONFIG_PATH) ? EXAMPLE_CONFIG_PATH : null)
  if (!source) return fallback
  return { ...fallback, ...JSON.parse(fs.readFileSync(source, 'utf8')) }
}

function saveConfig() {
  const tmp = `${CONFIG_PATH}.${process.pid}.tmp`
  fs.writeFileSync(tmp, `${JSON.stringify(config, null, 2)}\n`)
  fs.renameSync(tmp, CONFIG_PATH)
}

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

function yamlQuote(value) {
  return `'${String(value).replace(/'/g, "''")}'`
}

function readJsonBody(req, maxBytes = 128 * 1024) {
  return new Promise((resolve, reject) => {
    const chunks = []
    let total = 0
    req.on('data', (chunk) => {
      total += chunk.length
      if (total > maxBytes) {
        reject(Object.assign(new Error('request body too large'), { statusCode: 413 }))
        req.destroy()
        return
      }
      chunks.push(chunk)
    })
    req.on('end', () => {
      if (!chunks.length) {
        resolve({})
        return
      }
      try {
        resolve(JSON.parse(Buffer.concat(chunks).toString('utf8')))
      } catch (err) {
        reject(Object.assign(new Error('invalid JSON body'), { statusCode: 400 }))
      }
    })
    req.on('error', reject)
  })
}

function asString(value, fallback = '') {
  if (value === undefined || value === null) return fallback
  return String(value).trim()
}

function asBool(value, fallback = false) {
  if (value === undefined || value === null || value === '') return fallback
  if (typeof value === 'boolean') return value
  return ['1', 'true', 'yes', 'on', 'enabled'].includes(String(value).toLowerCase())
}

function asInt(value, fallback) {
  const n = Number(value)
  return Number.isFinite(n) ? Math.round(n) : fallback
}

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms))
}

function defaultLanDiscovery() {
  const match = String(PUBLIC_HOST || '').match(/^(\d+)\.(\d+)\.(\d+)\.\d+$/)
  return match ? `${match[1]}.${match[2]}.${match[3]}.255` : '255.255.255.255'
}

function isUnicastIPv4(value) {
  const parts = String(value || '').split('.')
  if (parts.length !== 4) return false
  const octets = parts.map((part) => Number(part))
  if (!octets.every((octet) => Number.isInteger(octet) && octet >= 0 && octet <= 255)) return false
  if (octets[0] === 0 || octets[0] >= 224) return false
  if (octets.every((octet) => octet === 255)) return false
  if (octets[3] === 0 || octets[3] === 255) return false
  return true
}

function expectedCameraAddress(camera) {
  if (isUnicastIPv4(camera.ip)) return camera.ip
  if (isUnicastIPv4(camera.discovery)) return camera.discovery
  return ''
}

function normalizeCamera(input, existing = null) {
  const camera = existing ? { ...existing } : {}
  const id = asString(input.id, camera.id)
  if (!id) throw Object.assign(new Error('camera id is required'), { statusCode: 400 })
  if (!CAMERA_ID_RE.test(id)) throw Object.assign(new Error(`camera id must match ${CAMERA_ID_RE}`), { statusCode: 400 })
  camera.id = id
  camera.name = asString(input.name, camera.name || id)
  camera.ip = asString(input.ip, camera.ip)
  camera.discovery = asString(input.discovery, camera.discovery || camera.ip || '255.255.255.255')
  camera.localAddress = asString(input.localAddress, camera.localAddress)
  camera.psk = asString(input.psk, camera.psk || 'SHIX')
  camera.username = asString(input.username, camera.username || 'admin')
  if (input.password !== undefined && input.password !== '') camera.password = String(input.password)
  else camera.password = camera.password || '6666'
  camera.width = asInt(input.width, camera.width || 640)
  camera.height = asInt(input.height, camera.height || 480)
  camera.enabled = asBool(input.enabled, camera.enabled !== false)
  camera.verbose = asBool(input.verbose, Boolean(camera.verbose))
  camera.avStream = asBool(input.avStream, camera.avStream !== false)
  camera.ackRepeats = Math.min(9, Math.max(1, asInt(input.ackRepeats, camera.ackRepeats || 3)))
  if (!camera.ip && !camera.discovery) {
    throw Object.assign(new Error('camera ip or discovery address is required'), { statusCode: 400 })
  }
  return camera
}

function storeCamera(camera) {
  const idx = cameraIndex(camera.id)
  if (idx === -1) config.cameras.push(camera)
  else config.cameras[idx] = camera
  saveConfig()
  return upsertRuntime(camera)
}

async function waitForSession(runtime, timeoutMs = 18000) {
  runtime.start()
  const deadline = Date.now() + timeoutMs
  while (Date.now() < deadline) {
    if (runtime.connectedAt) return true
    await sleep(250)
  }
  return Boolean(runtime.connectedAt)
}

function publicCameraConfig(camera) {
  return {
    id: camera.id,
    name: camera.name || camera.id,
    ip: camera.ip || '',
    discovery: camera.discovery || '',
    localAddress: camera.localAddress || '',
    psk: camera.psk || 'SHIX',
    username: camera.username || 'admin',
    hasPassword: Boolean(camera.password),
    width: camera.width || 640,
    height: camera.height || 480,
    enabled: camera.enabled !== false,
    verbose: Boolean(camera.verbose),
    avStream: camera.avStream !== false,
    ackRepeats: camera.ackRepeats || 3,
  }
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
    this.lastTrafficAt = null
    this.connectedAt = null
    this.peerAddress = null
    this.peerPort = null
    this.startedAt = null
    this.restartCount = 0
    this.videoFrames = 0
    this.audioFrames = 0
    this.lastError = null
    this.lastCommand = null
    this.monitor = null
    this.streamMaintainer = null
    this.videoMetric = []
    this.audioMetric = []
    this.lastFrameBytes = 0
    this.streamMode = 'idle'
    this.lastSnapshotDemandAt = null
  }

  start() {
    if (this.pppp || this.camera.enabled === false) return
    this.startedAt = Date.now()

    this.pppp = new PPPP({
      broadcastip: this.camera.discovery || this.camera.ip,
      thisip: this.camera.localAddress,
      psk: this.camera.psk || 'SHIX',
      username: this.camera.username || 'admin',
      password: this.camera.password || '6666',
      ackRepeats: this.camera.ackRepeats || 3,
      expectedAddress: expectedCameraAddress(this.camera),
      verbose: Boolean(this.camera.verbose),
    })

    this.pppp.on('connected', (peer) => {
      this.connectedAt = Date.now()
      this.peerAddress = peer.address
      this.peerPort = peer.port
      this.lastTrafficAt = Date.now()
      this.lastError = null
      console.log(`${nowIso()} camera ${this.id} connected ${peer.address}:${peer.port}`)
      setTimeout(() => this.requestStreams(true), 200)
      setTimeout(() => this.safeCall('sendCMDgetParams'), 800)
    })

    this.pppp.on('packet', () => {
      this.lastTrafficAt = Date.now()
    })

    this.pppp.on('videoFrame', ({ frame }) => {
      this.latestFrame = frame
      this.lastVideoAt = Date.now()
      this.lastTrafficAt = this.lastVideoAt
      this.videoFrames += 1
      this.lastFrameBytes = frame.length
      this.recordMetric(this.videoMetric, frame.length)
      this.broadcastVideoFrame(frame)
    })

    this.pppp.on('audioFrame', ({ frame }) => {
      this.lastAudioAt = Date.now()
      this.lastTrafficAt = this.lastAudioAt
      this.audioFrames += 1
      this.recordMetric(this.audioMetric, frame.length)
      this.broadcastAudioFrame(frame)
    })

    this.pppp.on('cmd', (cmd) => {
      this.lastTrafficAt = Date.now()
      this.lastCommand = String(cmd).trim()
    })

    this.pppp.on('log', (line) => {
      if (this.camera.verbose) console.log(`${nowIso()} camera ${this.id}: ${line}`)
    })

    this.pppp.on('ignoredPeer', (peer) => {
      if (this.camera.verbose) {
        const expected = peer.expectedPort ? `${peer.expectedAddress}:${peer.expectedPort}` : peer.expectedAddress
        console.log(`${nowIso()} camera ${this.id}: ignored peer ${peer.address}:${peer.port}, expected ${expected}`)
      }
    })

    this.pppp.on('socketError', (err) => {
      this.lastError = err.message
      this.restart(`socket error: ${err.message}`)
    })

    this.monitor = setInterval(() => this.checkHealth(), 5000)
    this.streamMaintainer = setInterval(() => this.maintainStreams(), 1000)
  }

  safeCall(method, ...args) {
    try {
      if (method.startsWith('sendCMD') && !this.connectedAt) return false
      if (!this.pppp || typeof this.pppp[method] !== 'function') return false
      this.pppp[method](...args)
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
      return
    }
    if (this.connectedAt && this.lastTrafficAt && now - this.lastTrafficAt > TRAFFIC_RESTART_MS) {
      this.restart('traffic timeout')
    }
  }

  maintainStreams() {
    this.sweepClients()
    if (!this.pppp || !this.connectedAt) return
    const now = Date.now()
    const hasSnapshotDemand = this.lastSnapshotDemandAt && now - this.lastSnapshotDemandAt < 10000
    const hasVideoDemand = this.videoClients.size > 0 || hasSnapshotDemand || !this.latestFrame
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
    const wantsAvTransport = this.camera.avStream !== false && wantsVideo

    if (wantsVideo && (wantsAudio || wantsAvTransport) && this.safeCall('sendCMDrequestAv')) {
      this.streamMode = 'audio+video'
      return
    }
    if (wantsVideo && this.safeCall('sendCMDrequestVideo1')) this.streamMode = 'video'
    if (wantsAudio && this.safeCall('sendCMDrequestAudio')) this.streamMode = wantsVideo ? 'audio+video' : 'audio'
  }

  recordMetric(bucket, bytes) {
    const now = Date.now()
    bucket.push({ at: now, bytes })
    while (bucket.length && now - bucket[0].at > METRIC_WINDOW_MS) bucket.shift()
  }

  metric(bucket) {
    const now = Date.now()
    while (bucket.length && now - bucket[0].at > METRIC_WINDOW_MS) bucket.shift()
    if (bucket.length < 2) {
      return { rate: 0, kbps: 0 }
    }
    const durationSec = Math.max(0.001, (bucket[bucket.length - 1].at - bucket[0].at) / 1000)
    const bytes = bucket.reduce((sum, point) => sum + point.bytes, 0)
    return {
      rate: Number((bucket.length / durationSec).toFixed(2)),
      kbps: Number(((bytes * 8) / durationSec / 1000).toFixed(1)),
    }
  }

  updateCamera(camera) {
    this.stop(true)
    this.camera = camera
    this.id = camera.id
    this.latestFrame = null
    this.lastVideoAt = null
    this.lastAudioAt = null
    this.lastTrafficAt = null
    this.peerAddress = null
    this.peerPort = null
    this.videoMetric = []
    this.audioMetric = []
    this.lastFrameBytes = 0
    this.streamMode = 'idle'
    this.lastSnapshotDemandAt = null
    if (camera.enabled !== false) this.start()
  }

  command(method, ...args) {
    const ok = this.safeCall(method, ...args)
    return ok ? { ok: true } : { ok: false, error: this.connectedAt ? 'command unavailable' : 'camera is not connected' }
  }

  setWifi(ssid, password) {
    if (!ssid || !password) return { ok: false, error: 'ssid and password are required' }
    return this.command('sendCMDsetWifi', ssid, password)
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
    this.peerAddress = null
    this.peerPort = null
    this.startedAt = null
    this.lastTrafficAt = null
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
    this.lastSnapshotDemandAt = Date.now()
    this.requestStreams(false)
    const health = this.health()
    if (!this.latestFrame || health.state === 'offline' || health.state === 'disabled') {
      writeJson(res, 503, { error: 'snapshot not ready', camera: this.id, healthState: health.state })
      return
    }
    res.writeHead(200, {
      'content-type': 'image/jpeg',
      'cache-control': 'no-store',
      'content-length': this.latestFrame.length,
    })
    res.end(this.latestFrame)
  }

  health() {
    if (this.camera.enabled === false) return { state: 'disabled', label: 'disabled' }
    if (!this.pppp || !this.connectedAt) {
      if (this.startedAt && this.restartCount === 0 && Date.now() - this.startedAt <= 20000) {
        return { state: 'connecting', label: 'connecting' }
      }
      return { state: 'offline', label: 'offline' }
    }
    if (!this.lastTrafficAt) return { state: 'connecting', label: 'connecting' }
    const age = Date.now() - this.lastTrafficAt
    if (age <= ONLINE_TRAFFIC_MS) return { state: 'online', label: 'online' }
    if (age <= STALE_TRAFFIC_MS) return { state: 'stale', label: 'stale' }
    return { state: 'offline', label: 'offline' }
  }

  status(baseUrl) {
    const videoMetric = this.metric(this.videoMetric)
    const audioMetric = this.metric(this.audioMetric)
    const health = this.health()
    return {
      id: this.id,
      name: this.camera.name || this.id,
      enabled: this.camera.enabled !== false,
      ip: this.camera.ip,
      discovery: this.camera.discovery || '',
      expectedAddress: expectedCameraAddress(this.camera),
      connected: health.state === 'online',
      transportConnected: Boolean(this.connectedAt),
      healthState: health.state,
      healthLabel: health.label,
      connectedAt: this.connectedAt ? new Date(this.connectedAt).toISOString() : null,
      peerAddress: this.peerAddress,
      peerPort: this.peerPort,
      lastTrafficAt: this.lastTrafficAt ? new Date(this.lastTrafficAt).toISOString() : null,
      lastVideoAt: this.lastVideoAt ? new Date(this.lastVideoAt).toISOString() : null,
      lastAudioAt: this.lastAudioAt ? new Date(this.lastAudioAt).toISOString() : null,
      lastTrafficAgeMs: this.lastTrafficAt ? Date.now() - this.lastTrafficAt : null,
      lastVideoAgeMs: this.lastVideoAt ? Date.now() - this.lastVideoAt : null,
      lastAudioAgeMs: this.lastAudioAt ? Date.now() - this.lastAudioAt : null,
      videoFrames: this.videoFrames,
      audioFrames: this.audioFrames,
      videoFps: videoMetric.rate,
      audioPacketsPerSecond: audioMetric.rate,
      videoKbps: videoMetric.kbps,
      audioKbps: audioMetric.kbps,
      lastFrameBytes: this.lastFrameBytes,
      streamMode: this.streamMode,
      avStream: this.camera.avStream !== false,
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

function cameraIndex(id) {
  return (config.cameras || []).findIndex((camera) => camera.id === id)
}

function upsertRuntime(camera) {
  const existing = runtimes.get(camera.id)
  if (existing) {
    existing.updateCamera(camera)
    return existing
  }
  const runtime = new CameraRuntime(camera)
  runtimes.set(camera.id, runtime)
  if (camera.enabled !== false) runtime.start()
  return runtime
}

function removeRuntime(id) {
  const runtime = runtimes.get(id)
  if (!runtime) return false
  runtime.stop(true)
  runtimes.delete(id)
  return true
}

config.cameras = (config.cameras || []).map((camera) => normalizeCamera(camera))
for (const camera of config.cameras) {
  if (runtimes.has(camera.id)) throw new Error(`duplicate camera.id: ${camera.id}`)
  upsertRuntime(camera)
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

function renderInput(name, label, value = '', attrs = '') {
  return `<label><span>${escapeHtml(label)}</span><input name="${escapeHtml(name)}" value="${escapeHtml(value)}" ${attrs}></label>`
}

function renderCameraConfigForm(camera) {
  if (!camera) return ''
  return `
    <form data-update-camera="${escapeHtml(camera.id)}" class="config-form">
      ${renderInput('name', 'Name', camera.name)}
      ${renderInput('ip', 'Camera IP', camera.ip, 'inputmode="numeric" autocomplete="off"')}
      ${renderInput('discovery', 'Discovery', camera.discovery, 'inputmode="numeric" autocomplete="off"')}
      ${renderInput('localAddress', 'Local bind', camera.localAddress, 'inputmode="numeric" autocomplete="off"')}
      ${renderInput('psk', 'PSK', camera.psk, 'autocomplete="off"')}
      ${renderInput('username', 'User', camera.username, 'autocomplete="off"')}
      ${renderInput('password', 'Password', '', 'type="password" placeholder="keep current" autocomplete="new-password"')}
      ${renderInput('ackRepeats', 'ACK repeats', camera.ackRepeats, 'type="number" min="1" max="9"')}
      ${renderInput('width', 'Width', camera.width, 'type="number" min="1"')}
      ${renderInput('height', 'Height', camera.height, 'type="number" min="1"')}
      <label class="check"><input name="avStream" type="checkbox" ${camera.avStream ? 'checked' : ''}><span>Request AV stream</span></label>
      <label class="check"><input name="enabled" type="checkbox" ${camera.enabled ? 'checked' : ''}><span>Enabled</span></label>
      <button type="submit">Save</button>
      <output></output>
    </form>`
}

function renderWizard() {
  const lanDiscovery = defaultLanDiscovery()
  return `
    <section class="wizard setup-wizard">
      <header class="section-head">
        <div>
          <h2>New camera setup</h2>
          <p class="meta">This wizard is fully local and keeps working when your computer is connected to a camera AP without internet.</p>
        </div>
      </header>
      <div class="steps">
        <section class="step">
          <strong>1. Prepare</strong>
          <p>Connect this computer to the camera AP or to the same LAN as the camera. Keep this page open from the local server; no internet is required.</p>
        </section>
        <section class="step">
          <strong>2. Write Wi-Fi and save camera</strong>
          <p>The server first reaches the camera at its current address, sends Wi-Fi settings, then stores the LAN address in the local config. If the final IP is unknown, leave it empty and use LAN broadcast discovery.</p>
          <form data-provision-camera class="config-form">
            <label><span>ID</span><input name="id" required pattern="[A-Za-z0-9_-]+" autocomplete="off" placeholder="a9_front"></label>
            <label><span>Name</span><input name="name" autocomplete="off" placeholder="Front door"></label>
            <label><span>Current IP</span><input name="setupIp" placeholder="192.168.4.1" inputmode="numeric" autocomplete="off"></label>
            <label><span>Current discovery</span><input name="setupDiscovery" placeholder="255.255.255.255" inputmode="numeric" autocomplete="off"></label>
            <label><span>Target SSID</span><input name="ssid" autocomplete="off"></label>
            <label><span>Target password</span><input name="wifiPassword" type="password" autocomplete="new-password"></label>
            <label><span>Final LAN IP</span><input name="finalIp" placeholder="optional" inputmode="numeric" autocomplete="off"></label>
            <label><span>Final discovery</span><input name="finalDiscovery" placeholder="${escapeHtml(lanDiscovery)}" inputmode="numeric" autocomplete="off"></label>
            <label><span>PSK</span><input name="psk" value="SHIX" autocomplete="off"></label>
            <label><span>User</span><input name="username" value="admin" autocomplete="off"></label>
            <label><span>Camera password</span><input name="password" type="password" value="6666" autocomplete="new-password"></label>
            <label><span>ACK</span><input name="ackRepeats" type="number" min="1" max="9" value="3"></label>
            <label class="check"><input name="reboot" type="checkbox" checked><span>Reboot</span></label>
            <button type="submit">Write and save</button>
            <output></output>
          </form>
        </section>
        <section class="step">
          <strong>3. Move back to LAN</strong>
          <p>After reboot, connect this computer back to the target Wi-Fi. The dashboard will use the saved LAN address for the stream.</p>
        </section>
        <section class="step">
          <strong>4. Existing cameras</strong>
          <p>For a camera already saved in the config, open its card and use Settings or Maintenance. This wizard is only for initial provisioning.</p>
        </section>
      </div>
    </section>`
}

async function provisionCamera(body, req) {
  const id = asString(body.id)
  if (!id) throw Object.assign(new Error('camera id is required'), { statusCode: 400 })
  if (!CAMERA_ID_RE.test(id)) throw Object.assign(new Error(`camera id must match ${CAMERA_ID_RE}`), { statusCode: 400 })

  const ssid = asString(body.ssid)
  const wifiPassword = body.wifiPassword === undefined ? '' : String(body.wifiPassword)
  if (!ssid) throw Object.assign(new Error('target SSID is required'), { statusCode: 400 })
  if (!wifiPassword) throw Object.assign(new Error('target Wi-Fi password is required'), { statusCode: 400 })

  const existingIdx = cameraIndex(id)
  const existing = existingIdx === -1 ? null : config.cameras[existingIdx]
  const setupIp = asString(body.setupIp)
  const setupDiscovery = asString(body.setupDiscovery, setupIp || '255.255.255.255')
  const finalIp = asString(body.finalIp)
  const finalDiscoveryInput = asString(body.finalDiscovery)
  const finalDiscovery = finalDiscoveryInput || finalIp || defaultLanDiscovery()

  const setupCamera = normalizeCamera({
    id,
    name: asString(body.name, existing?.name || id),
    ip: setupIp,
    discovery: setupDiscovery,
    localAddress: asString(body.localAddress, existing?.localAddress || ''),
    psk: asString(body.psk, existing?.psk || 'SHIX'),
    username: asString(body.username, existing?.username || 'admin'),
    password: body.password !== undefined && body.password !== '' ? body.password : (existing?.password || '6666'),
    width: asInt(body.width, existing?.width || 640),
    height: asInt(body.height, existing?.height || 480),
    ackRepeats: asInt(body.ackRepeats, existing?.ackRepeats || 3),
    enabled: true,
    avStream: asBool(body.avStream, existing?.avStream !== false),
    verbose: asBool(body.verbose, Boolean(existing?.verbose)),
  }, existing)

  const runtime = upsertRuntime(setupCamera)
  const connected = await waitForSession(runtime)
  if (!connected) {
    if (existing) upsertRuntime(existing)
    else removeRuntime(id)
    throw Object.assign(new Error(`camera did not answer at ${setupCamera.discovery || setupCamera.ip}`), { statusCode: 409 })
  }

  const result = runtime.setWifi(ssid, wifiPassword)
  if (!result.ok) {
    if (existing) upsertRuntime(existing)
    else removeRuntime(id)
    throw Object.assign(new Error(result.error || 'failed to send Wi-Fi settings'), { statusCode: 409 })
  }

  if (asBool(body.reboot, true)) {
    await sleep(1200)
    runtime.safeCall('sendCMDReboot')
  }

  const finalCamera = normalizeCamera({
    ...setupCamera,
    ip: finalIp,
    discovery: finalDiscovery,
    enabled: true,
  }, setupCamera)
  const finalRuntime = storeCamera(finalCamera)

  return {
    ok: true,
    message: 'Wi-Fi sent and camera config saved',
    camera: publicCameraConfig(finalCamera),
    status: finalRuntime.status(baseUrl(req)),
  }
}

async function handleApi(req, res, pathname) {
  const method = req.method || 'GET'

  if (pathname === '/api/status' && method === 'GET') {
    writeJson(res, 200, {
      server: { host: PUBLIC_HOST, port: PORT, bind: BIND },
      cameras: allStatuses(req),
    })
    return true
  }

  if (pathname === '/api/config' && method === 'GET') {
    writeJson(res, 200, {
      configPath: CONFIG_PATH,
      server: { host: PUBLIC_HOST, port: PORT, bind: BIND },
      cameras: config.cameras.map(publicCameraConfig),
    })
    return true
  }

  if (pathname === '/api/setup/provision' && method === 'POST') {
    const body = await readJsonBody(req)
    writeJson(res, 200, await provisionCamera(body, req))
    return true
  }

  if (pathname === '/api/cameras' && method === 'GET') {
    writeJson(res, 200, {
      cameras: config.cameras.map((camera) => ({
        ...publicCameraConfig(camera),
        status: runtimes.get(camera.id)?.status(baseUrl(req)) || null,
      })),
    })
    return true
  }

  if (pathname === '/api/cameras' && method === 'POST') {
    const body = await readJsonBody(req)
    const camera = normalizeCamera(body)
    if (cameraIndex(camera.id) !== -1) {
      writeJson(res, 409, { error: 'camera already exists', id: camera.id })
      return true
    }
    config.cameras.push(camera)
    saveConfig()
    const runtime = upsertRuntime(camera)
    writeJson(res, 201, {
      camera: publicCameraConfig(camera),
      status: runtime.status(baseUrl(req)),
    })
    return true
  }

  const match = pathname.match(/^\/api\/cameras\/([^/]+)(?:\/([^/]+))?$/)
  if (!match) return false

  const id = decodeURIComponent(match[1])
  const action = match[2] || ''
  const idx = cameraIndex(id)
  const camera = idx === -1 ? null : config.cameras[idx]
  const runtime = runtimes.get(id)

  if (!camera) {
    writeJson(res, 404, { error: 'unknown camera', id })
    return true
  }

  if (!action && method === 'GET') {
    writeJson(res, 200, {
      camera: publicCameraConfig(camera),
      status: runtime?.status(baseUrl(req)) || null,
    })
    return true
  }

  if (!action && (method === 'PATCH' || method === 'PUT')) {
    const body = await readJsonBody(req)
    if (body.id && body.id !== id) {
      writeJson(res, 400, { error: 'camera id cannot be changed' })
      return true
    }
    const next = normalizeCamera({ ...camera, ...body, id }, camera)
    config.cameras[idx] = next
    saveConfig()
    const nextRuntime = upsertRuntime(next)
    writeJson(res, 200, {
      camera: publicCameraConfig(next),
      status: nextRuntime.status(baseUrl(req)),
    })
    return true
  }

  if (!action && method === 'DELETE') {
    removeRuntime(id)
    config.cameras.splice(idx, 1)
    saveConfig()
    writeJson(res, 200, { ok: true, id })
    return true
  }

  if (method !== 'POST') {
    writeJson(res, 405, { error: 'method not allowed' })
    return true
  }

  const body = await readJsonBody(req)
  if (action === 'restart') {
    runtime.restart('manual restart')
    writeJson(res, 200, { ok: true, id })
    return true
  }
  if (action === 'wifi') {
    const result = runtime.setWifi(asString(body.ssid), String(body.password || ''))
    if (result.ok && asBool(body.reboot, false)) setTimeout(() => runtime.safeCall('sendCMDReboot'), 1500)
    writeJson(res, result.ok ? 200 : 409, result.ok ? { ok: true, id } : result)
    return true
  }
  if (action === 'scan-wifi') {
    const result = runtime.command('sendCMDscanWifi')
    writeJson(res, result.ok ? 200 : 409, result.ok ? { ok: true, id } : result)
    return true
  }
  if (action === 'params') {
    const result = runtime.command('sendCMDgetParams')
    writeJson(res, result.ok ? 200 : 409, result.ok ? { ok: true, id } : result)
    return true
  }
  if (action === 'reboot') {
    const result = runtime.command('sendCMDReboot')
    writeJson(res, result.ok ? 200 : 409, result.ok ? { ok: true, id } : result)
    return true
  }

  writeJson(res, 404, { error: 'unknown camera action', id, action })
  return true
}

function renderPage(req, cameraId = null, mode = 'dashboard') {
  const isSetup = mode === 'setup'
  const statuses = allStatuses(req)
  const cameras = isSetup ? [] : (cameraId ? statuses.filter((c) => c.id === cameraId) : statuses)
  const configs = new Map(config.cameras.map((camera) => [camera.id, publicCameraConfig(camera)]))
  const manager = isSetup
    ? renderWizard()
    : (cameraId ? '' : `
      <section class="overview-head">
        <div>
          <h2>Cameras</h2>
          <p class="meta">Snapshot previews keep the overview light; open a camera for live video and sound.</p>
        </div>
        <a class="primary" href="/setup">Set up new camera</a>
      </section>
    `)
  const cards = cameras.map((c) => `
    <section class="camera ${cameraId ? 'detail' : 'summary-card'}" data-camera-id="${escapeHtml(c.id)}">
      <header class="camera-head">
        <div>
          <h2>${escapeHtml(c.name)}</h2>
          <p class="meta">${escapeHtml(c.ip || '')} · ${escapeHtml(c.id)}</p>
        </div>
        <span class="state ${escapeHtml(c.healthState || 'offline')}">${escapeHtml(c.healthLabel || 'offline')}</span>
      </header>
      <div class="media">
        ${cameraId
          ? `<img data-live="${escapeHtml(c.id)}" src="/cam/${encodeURIComponent(c.id)}/video.mjpg?ts=${Date.now()}" alt="${escapeHtml(c.name)} live video">`
          : `<a href="/cam/${encodeURIComponent(c.id)}"><img data-preview="${escapeHtml(c.id)}" src="/cam/${encodeURIComponent(c.id)}/snapshot.jpg?ts=${Date.now()}" alt="${escapeHtml(c.name)} preview"></a>`}
      </div>
      <div class="toolbar">
        <a href="/cam/${encodeURIComponent(c.id)}">Open live</a>
        <button data-audio="${escapeHtml(c.id)}">Sound</button>
        ${cameraId ? `<button data-live-reconnect="${escapeHtml(c.id)}">Reconnect video</button>` : ''}
        <a href="/cam/${encodeURIComponent(c.id)}/snapshot.jpg" target="_blank">Snapshot</a>
      </div>
      <audio id="audio-${escapeHtml(c.id)}" controls preload="none" hidden></audio>
      <dl class="stats">
        <div><dt>Video</dt><dd data-field="${escapeHtml(c.id)}:videoFrames">${c.videoFrames}</dd></div>
        <div><dt>FPS</dt><dd data-field="${escapeHtml(c.id)}:videoFps">${c.videoFps || 0}</dd></div>
        <div><dt>Video kbps</dt><dd data-field="${escapeHtml(c.id)}:videoKbps">${c.videoKbps || 0}</dd></div>
        <div><dt>Audio</dt><dd data-field="${escapeHtml(c.id)}:audioFrames">${c.audioFrames}</dd></div>
        <div><dt>Status</dt><dd data-field="${escapeHtml(c.id)}:healthLabel">${escapeHtml(c.healthLabel || 'offline')}</dd></div>
        <div><dt>Clients</dt><dd data-field="${escapeHtml(c.id)}:clients">${c.videoClients}/${c.audioClients}</dd></div>
        <div><dt>Restarts</dt><dd data-field="${escapeHtml(c.id)}:restartCount">${c.restartCount}</dd></div>
        <div><dt>Mode</dt><dd data-field="${escapeHtml(c.id)}:streamMode">${escapeHtml(c.streamMode || 'idle')}</dd></div>
        <div><dt>Peer</dt><dd data-field="${escapeHtml(c.id)}:peer">${escapeHtml(c.peerAddress || c.expectedAddress || '')}</dd></div>
      </dl>
      <details>
        <summary>Settings</summary>
        ${renderCameraConfigForm(configs.get(c.id))}
      </details>
      <details>
        <summary>Maintenance</summary>
        <div class="toolbar maintenance">
          <button data-command="restart" data-id="${escapeHtml(c.id)}">Reconnect camera session</button>
          <button data-command="params" data-id="${escapeHtml(c.id)}">Refresh camera info</button>
          <button data-command="reboot" data-confirm="Restart camera hardware?" data-id="${escapeHtml(c.id)}">Restart camera hardware</button>
          <a href="/cam/${encodeURIComponent(c.id)}/video.mjpg" target="_blank">Raw MJPEG</a>
          <a href="/cam/${encodeURIComponent(c.id)}/audio.wav" target="_blank">Raw WAV</a>
        </div>
        <form data-wifi-camera="${escapeHtml(c.id)}" class="config-form compact">
          <label><span>Wi-Fi SSID</span><input name="ssid" autocomplete="off"></label>
          <label><span>Wi-Fi password</span><input name="password" type="password" autocomplete="new-password"></label>
          <label class="check"><input name="reboot" type="checkbox" checked><span>Reboot</span></label>
          <button type="submit">Set Wi-Fi</button>
          <output></output>
        </form>
      </details>
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
    nav a, .toolbar a, button, .primary { border: 1px solid var(--line); background: var(--panel); text-decoration: none; padding: 7px 10px; border-radius: 6px; font: inherit; cursor: pointer; }
    .primary { background: var(--fg); color: var(--bg); border-color: var(--fg); }
    main { width: min(1440px, 100%); margin: 0 auto; padding: 16px; }
    code { background: var(--bg); border: 1px solid var(--line); border-radius: 4px; padding: 1px 4px; }
    .overview-head { margin-bottom: 16px; display: flex; align-items: center; justify-content: space-between; gap: 12px; background: var(--panel); border: 1px solid var(--line); border-radius: 8px; padding: 12px 14px; }
    .wizard { margin-bottom: 16px; background: var(--panel); border: 1px solid var(--line); border-radius: 8px; }
    .section-head { padding: 12px 14px; border-bottom: 1px solid var(--line); }
    .steps { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 12px; padding: 12px; }
    .step { border: 1px solid var(--line); border-radius: 8px; padding: 10px; min-width: 0; }
    .step strong { display: block; margin-bottom: 6px; }
    .step p { margin: 0; color: var(--muted); }
    .grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(260px, 1fr)); gap: 14px; align-items: start; }
    .detail-grid { grid-template-columns: minmax(0, 980px); justify-content: center; }
    .camera { background: var(--panel); border: 1px solid var(--line); border-radius: 8px; overflow: hidden; }
    .camera-head { display: flex; align-items: center; justify-content: space-between; gap: 12px; padding: 12px 14px; border-bottom: 1px solid var(--line); }
    h2 { font-size: 16px; margin: 0 0 2px; font-weight: 650; }
    .meta { margin: 0; color: var(--muted); font-size: 12px; }
    .state { font-size: 12px; text-transform: uppercase; letter-spacing: .04em; font-weight: 700; }
    .state.online { color: var(--ok); }
    .state.stale, .state.connecting { color: var(--warn); }
    .state.offline, .state.disabled { color: var(--muted); }
    .media { aspect-ratio: 16 / 9; background: #050505; display: grid; place-items: center; }
    .detail .media { aspect-ratio: 4 / 3; }
    .media a { display: block; width: 100%; height: 100%; }
    .media img { width: 100%; height: 100%; object-fit: contain; display: block; }
    .toolbar { display: flex; flex-wrap: wrap; gap: 8px; padding: 10px 12px; border-top: 1px solid var(--line); }
    .maintenance { padding: 0 0 10px; border-top: 0; }
    audio:not([hidden]) { display: block; width: calc(100% - 24px); margin: 0 12px 12px; height: 36px; }
    .stats { margin: 0; padding: 10px 12px 12px; display: grid; grid-template-columns: repeat(4, 1fr); gap: 8px; border-top: 1px solid var(--line); }
    .stats div { min-width: 0; }
    dt { color: var(--muted); font-size: 11px; margin-bottom: 2px; }
    dd { margin: 0; font-variant-numeric: tabular-nums; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
    details { border-top: 1px solid var(--line); padding: 10px 12px 12px; }
    summary { cursor: pointer; color: var(--muted); font-weight: 650; margin-bottom: 10px; }
    .config-form { display: grid; grid-template-columns: repeat(4, minmax(120px, 1fr)); gap: 10px; align-items: end; }
    .config-form.compact { grid-template-columns: repeat(3, minmax(120px, 1fr)); margin-top: 10px; }
    label { display: grid; gap: 4px; min-width: 0; color: var(--muted); font-size: 11px; }
    label span { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
    input, select { width: 100%; min-width: 0; border: 1px solid var(--line); background: var(--bg); color: var(--fg); border-radius: 6px; padding: 7px 8px; font: inherit; }
    label.check { display: flex; align-items: center; gap: 8px; padding-bottom: 7px; }
    label.check input { width: auto; }
    output { color: var(--muted); min-height: 18px; align-self: center; }
    @media (max-width: 920px) { .steps { grid-template-columns: 1fr; } }
    @media (max-width: 760px) { .config-form, .config-form.compact { grid-template-columns: repeat(2, minmax(0, 1fr)); } }
    @media (max-width: 520px) { main { padding: 10px; } .grid { grid-template-columns: 1fr; } header.top { padding: 0 12px; } nav a { display: none; } .stats { grid-template-columns: repeat(2, 1fr); } .config-form, .config-form.compact { grid-template-columns: 1fr; } }
  </style>
</head>
<body>
  <header class="top">
    <h1>BKCam</h1>
    <nav>
      <a href="/">Dashboard</a>
      <a href="/setup">Setup</a>
      <a href="/api/status">Status</a>
      <a href="/frigate.yml">Frigate</a>
      <a href="/go2rtc.yml">go2rtc</a>
    </nav>
  </header>
  <main>${manager}<div class="grid ${cameraId ? 'detail-grid' : ''}">${cards}</div></main>
  <script>
    const audioPlayers = new Map()
    const liveReconnectAt = new Map()
    const LIVE_RECONNECT_MS = 25000
    const PREVIEW_REFRESH_MS = 2000

    function cameraPath(id, suffix) {
      return '/cam/' + encodeURIComponent(id) + suffix
    }

    function refreshPreviews() {
      for (const img of document.querySelectorAll('img[data-preview]')) {
        const id = img.dataset.preview
        img.src = cameraPath(id, '/snapshot.jpg?ts=') + Date.now()
      }
    }

    function reconnectLive(id, force) {
      const img = document.querySelector('img[data-live="' + id + '"]')
      if (!img) return
      const now = Date.now()
      if (!force && liveReconnectAt.has(id) && now - liveReconnectAt.get(id) < LIVE_RECONNECT_MS) return
      liveReconnectAt.set(id, now)
      img.src = cameraPath(id, '/video.mjpg?ts=') + now
    }

    function reconnectLiveStreams() {
      for (const img of document.querySelectorAll('img[data-live]')) reconnectLive(img.dataset.live, false)
    }

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

    async function sendJson(url, method, body) {
      const res = await fetch(url, {
        method,
        headers: { 'content-type': 'application/json' },
        body: body ? JSON.stringify(body) : undefined
      })
      const data = await res.json().catch(() => ({}))
      if (!res.ok) throw new Error(data.error || 'request failed')
      return data
    }

    function readForm(form) {
      const data = {}
      for (const el of Array.from(form.elements)) {
        if (!el.name) continue
        if (el.type === 'checkbox') data[el.name] = el.checked
        else if (el.value !== '') data[el.name] = el.value
      }
      return data
    }

    document.addEventListener('submit', async (ev) => {
      const form = ev.target
      const out = form.querySelector('output')
      try {
        if (form.dataset.addCamera !== undefined) {
          ev.preventDefault()
          await sendJson('/api/cameras', 'POST', readForm(form))
          location.reload()
        } else if (form.dataset.provisionCamera !== undefined) {
          ev.preventDefault()
          if (out) out.textContent = 'Writing...'
          await sendJson('/api/setup/provision', 'POST', readForm(form))
          if (out) out.textContent = 'Saved'
          setTimeout(() => { location.href = '/' }, 700)
        } else if (form.dataset.updateCamera) {
          ev.preventDefault()
          await sendJson('/api/cameras/' + encodeURIComponent(form.dataset.updateCamera), 'PATCH', readForm(form))
          if (out) out.textContent = 'Saved'
        } else if (form.dataset.wifiCamera) {
          ev.preventDefault()
          await sendJson('/api/cameras/' + encodeURIComponent(form.dataset.wifiCamera) + '/wifi', 'POST', readForm(form))
          if (out) out.textContent = 'Sent'
        } else if (form.dataset.wizardWifi !== undefined) {
          ev.preventDefault()
          const data = readForm(form)
          if (!data.id) throw new Error('camera is required')
          delete data.id
          await sendJson('/api/cameras/' + encodeURIComponent(form.elements.id.value) + '/wifi', 'POST', data)
          if (out) out.textContent = 'Sent'
        }
      } catch (err) {
        if (out) out.textContent = err.message
      }
    })

    document.addEventListener('click', async (ev) => {
      const reconnectId = ev.target && ev.target.dataset && ev.target.dataset.liveReconnect
      if (reconnectId) {
        ev.preventDefault()
        reconnectLive(reconnectId, true)
        return
      }

      const action = ev.target && ev.target.dataset && ev.target.dataset.command
      const id = ev.target && ev.target.dataset && ev.target.dataset.id
      if (!action || !id) return
      ev.preventDefault()
      if (ev.target.dataset.confirm && !window.confirm(ev.target.dataset.confirm)) return
      const old = ev.target.textContent
      try {
        ev.target.textContent = '...'
        await sendJson('/api/cameras/' + encodeURIComponent(id) + '/' + action, 'POST')
        ev.target.textContent = old
      } catch (_) {
        ev.target.textContent = old
      }
    })

    async function poll() {
      try {
        const res = await fetch('/api/status', { cache: 'no-store' })
        const data = await res.json()
        for (const cam of data.cameras) {
          const el = document.querySelector('[data-camera-id="' + cam.id + '"] .state')
          if (el) { el.textContent = cam.healthLabel || 'offline'; el.className = 'state ' + (cam.healthState || 'offline') }
          const fields = {
            videoFrames: cam.videoFrames,
            videoFps: cam.videoFps,
            videoKbps: cam.videoKbps,
            audioFrames: cam.audioFrames,
            healthLabel: cam.healthLabel,
            clients: cam.videoClients + '/' + cam.audioClients,
            restartCount: cam.restartCount,
            streamMode: cam.streamMode,
            peer: cam.peerAddress || cam.expectedAddress || ''
          }
          for (const [k, v] of Object.entries(fields)) {
            const f = document.querySelector('[data-field="' + cam.id + ':' + k + '"]')
            if (f) f.textContent = v
          }
        }
      } catch (_) {}
    }
    setInterval(poll, 3000)
    setInterval(refreshPreviews, PREVIEW_REFRESH_MS)
    setInterval(reconnectLiveStreams, 5000)
    document.addEventListener('visibilitychange', () => {
      if (!document.hidden) {
        refreshPreviews()
        for (const img of document.querySelectorAll('img[data-live]')) reconnectLive(img.dataset.live, true)
      }
    })
  </script>
</body>
</html>`
}

function renderFrigate(base) {
  const enabled = Array.from(runtimes.values()).filter((r) => r.camera.enabled !== false)
  const streamLines = enabled.map((r) => `    ${r.id}:
      - ${yamlQuote(`ffmpeg:${base}/cam/${r.id}/video.mjpg#video=h264`)}
      - ${yamlQuote(`ffmpeg:${base}/cam/${r.id}/audio.wav#audio=aac#audio=opus`)}`).join('\n')
  const cameraLines = enabled.map((r) => `  ${r.id}:
    ffmpeg:
      inputs:
        - path: ${yamlQuote(`rtsp://127.0.0.1:8554/${r.id}?video=h264&audio=aac`)}
          input_args: preset-rtsp-restream
          roles:
            - detect
            - record
    detect:
      width: ${r.camera.width || 640}
      height: ${r.camera.height || 480}`).join('\n')
  return `ffmpeg:
  output_args:
    record: preset-record-generic-audio-aac

go2rtc:
  streams:
${streamLines}

cameras:
${cameraLines}

# Optional audio events:
# Add role "audio" to the input roles and set "audio.enabled: true" per camera
# if you want Frigate to create events from sound levels.
`
}

function renderGo2rtc(base) {
  const enabled = Array.from(runtimes.values()).filter((r) => r.camera.enabled !== false)
  return `streams:
${enabled.map((r) => `  ${r.id}:
    - ${yamlQuote(`ffmpeg:${base}/cam/${r.id}/video.mjpg#video=h264`)}
    - ${yamlQuote(`ffmpeg:${base}/cam/${r.id}/audio.wav#audio=aac#audio=opus`)}`).join('\n')}
`
}

async function route(req, res) {
  const parsed = new URL(req.url, `http://${req.headers.host || 'localhost'}`)
  const pathname = parsed.pathname

  if (pathname.startsWith('/api/')) {
    if (await handleApi(req, res, pathname)) return
  }

  if (pathname === '/') {
    writeText(res, 200, renderPage(req), 'text/html; charset=utf-8')
    return
  }
  if (pathname === '/setup') {
    writeText(res, 200, renderPage(req, null, 'setup'), 'text/html; charset=utf-8')
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
  } else if (action === 'snapshot.jpg' || action === 'preview.jpg') {
    runtime.writeSnapshot(res)
  } else {
    writeJson(res, 404, { error: 'unknown camera endpoint', id, action })
  }
}

const server = http.createServer((req, res) => {
  try {
    Promise.resolve(route(req, res)).catch((err) => {
      console.error(`${nowIso()} request failed: ${err.stack || err}`)
      if (!res.headersSent) writeJson(res, err.statusCode || 500, { error: err.message || 'internal error' })
      else res.destroy()
    })
  } catch (err) {
    console.error(`${nowIso()} request failed: ${err.stack || err}`)
    if (!res.headersSent) writeJson(res, err.statusCode || 500, { error: err.message || 'internal error' })
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
