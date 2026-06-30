package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	mcam byte = 0xf1
	mdrw byte = 0xd1

	msgPunch    byte = 0x41
	msgP2PRdy   byte = 0x42
	msgDRW      byte = 0xd0
	msgDRWAck   byte = 0xd1
	msgAlive    byte = 0xe0
	msgAliveAck byte = 0xe1
	msgClose    byte = 0xf0

	audioSampleRate      = 8000
	audioChannels        = 1
	audioBytesPerSample  = 2
	metricWindow         = 5 * time.Second
	onlineTrafficWindow  = 15 * time.Second
	staleTrafficWindow   = 45 * time.Second
	trafficRestartWindow = 60 * time.Second
	streamRequestWindow  = 2 * time.Second
	videoRefreshWindow   = 6 * time.Second
	videoStaleWindow     = 8 * time.Second
	audioStaleWindow     = 4 * time.Second
	mediaRestartWindow   = 25 * time.Second
	restartCooldown      = 5 * time.Second
	videoHoldInterval    = 1 * time.Second
	videoHoldWindow      = 75 * time.Second
	videoClientIdle      = 90 * time.Second
	audioClientIdle      = 30 * time.Second
)

var keyTable = [256]byte{
	0x7C, 0x9C, 0xE8, 0x4A, 0x13, 0xDE, 0xDC, 0xB2, 0x2F, 0x21, 0x23, 0xE4, 0x30, 0x7B, 0x3D, 0x8C,
	0xBC, 0x0B, 0x27, 0x0C, 0x3C, 0xF7, 0x9A, 0xE7, 0x08, 0x71, 0x96, 0x00, 0x97, 0x85, 0xEF, 0xC1,
	0x1F, 0xC4, 0xDB, 0xA1, 0xC2, 0xEB, 0xD9, 0x01, 0xFA, 0xBA, 0x3B, 0x05, 0xB8, 0x15, 0x87, 0x83,
	0x28, 0x72, 0xD1, 0x8B, 0x5A, 0xD6, 0xDA, 0x93, 0x58, 0xFE, 0xAA, 0xCC, 0x6E, 0x1B, 0xF0, 0xA3,
	0x88, 0xAB, 0x43, 0xC0, 0x0D, 0xB5, 0x45, 0x38, 0x4F, 0x50, 0x22, 0x66, 0x20, 0x7F, 0x07, 0x5B,
	0x14, 0x98, 0x1D, 0x9B, 0xA7, 0x2A, 0xB9, 0xA8, 0xCB, 0xF1, 0xFC, 0x49, 0x47, 0x06, 0x3E, 0xB1,
	0x0E, 0x04, 0x3A, 0x94, 0x5E, 0xEE, 0x54, 0x11, 0x34, 0xDD, 0x4D, 0xF9, 0xEC, 0xC7, 0xC9, 0xE3,
	0x78, 0x1A, 0x6F, 0x70, 0x6B, 0xA4, 0xBD, 0xA9, 0x5D, 0xD5, 0xF8, 0xE5, 0xBB, 0x26, 0xAF, 0x42,
	0x37, 0xD8, 0xE1, 0x02, 0x0A, 0xAE, 0x5F, 0x1C, 0xC5, 0x73, 0x09, 0x4E, 0x69, 0x24, 0x90, 0x6D,
	0x12, 0xB3, 0x19, 0xAD, 0x74, 0x8A, 0x29, 0x40, 0xF5, 0x2D, 0xBE, 0xA5, 0x59, 0xE0, 0xF4, 0x79,
	0xD2, 0x4B, 0xCE, 0x89, 0x82, 0x48, 0x84, 0x25, 0xC6, 0x91, 0x2B, 0xA2, 0xFB, 0x8F, 0xE9, 0xA6,
	0xB0, 0x9E, 0x3F, 0x65, 0xF6, 0x03, 0x31, 0x2E, 0xAC, 0x0F, 0x95, 0x2C, 0x5C, 0xED, 0x39, 0xB7,
	0x33, 0x6C, 0x56, 0x7E, 0xB4, 0xA0, 0xFD, 0x7A, 0x81, 0x53, 0x51, 0x86, 0x8D, 0x9F, 0x77, 0xFF,
	0x6A, 0x80, 0xDF, 0xE2, 0xBF, 0x10, 0xD7, 0x75, 0x64, 0x57, 0x76, 0xF3, 0x55, 0xCD, 0xD0, 0xC8,
	0x18, 0xE6, 0x36, 0x41, 0x62, 0xCF, 0x99, 0xF2, 0x32, 0x4C, 0x67, 0x60, 0x61, 0x92, 0xCA, 0xD3,
	0xEA, 0x63, 0x7D, 0x16, 0xB6, 0x8E, 0xD4, 0x68, 0x35, 0xC3, 0x52, 0x9D, 0x46, 0x44, 0x1E, 0x17,
}

type Config struct {
	Server struct {
		Host string `json:"host"`
		Port int    `json:"port"`
		Bind string `json:"bind"`
	} `json:"server"`
	Cameras []CameraConfig `json:"cameras"`
}

type CameraConfig struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	IP           string `json:"ip"`
	Discovery    string `json:"discovery"`
	LocalAddress string `json:"localAddress"`
	PSK          string `json:"psk"`
	Username     string `json:"username"`
	Password     string `json:"password"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	Enabled      *bool  `json:"enabled,omitempty"`
	Verbose      bool   `json:"verbose"`
	AckRepeats   int    `json:"ackRepeats"`
	AvStream     *bool  `json:"avStream,omitempty"`
}

func (c CameraConfig) enabled() bool {
	return c.Enabled == nil || *c.Enabled
}

func (c CameraConfig) avStream() bool {
	return c.AvStream == nil || *c.AvStream
}

func (c CameraConfig) name() string {
	if c.Name != "" {
		return c.Name
	}
	return c.ID
}

func (c CameraConfig) psk() string {
	if c.PSK != "" {
		return c.PSK
	}
	return "SHIX"
}

func (c CameraConfig) username() string {
	if c.Username != "" {
		return c.Username
	}
	return "admin"
}

func (c CameraConfig) password() string {
	if c.Password != "" {
		return c.Password
	}
	return "6666"
}

func (c CameraConfig) width() int {
	if c.Width > 0 {
		return c.Width
	}
	return 640
}

func (c CameraConfig) height() int {
	if c.Height > 0 {
		return c.Height
	}
	return 480
}

func (c CameraConfig) videoMode() int {
	if c.width() <= 320 || c.height() <= 240 {
		return 2
	}
	return 1
}

func (c CameraConfig) ackRepeats() int {
	if c.AckRepeats < 1 {
		return 3
	}
	if c.AckRepeats > 9 {
		return 9
	}
	return c.AckRepeats
}

func (c CameraConfig) discovery() string {
	if c.Discovery != "" {
		return c.Discovery
	}
	if c.IP != "" {
		return c.IP
	}
	return "255.255.255.255"
}

func expectedAddress(c CameraConfig) string {
	if isUnicastIPv4(c.IP) {
		return c.IP
	}
	if isUnicastIPv4(c.Discovery) {
		return c.Discovery
	}
	return ""
}

type Packet struct {
	Type    byte
	Size    int
	Channel byte
	Index   uint16
	Data    []byte
}

type metricPoint struct {
	at    time.Time
	bytes int
}

type metricResult struct {
	Rate float64 `json:"rate"`
	Kbps float64 `json:"kbps"`
}

type Client struct {
	ch chan []byte
}

type CommandResult struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

type CameraRuntime struct {
	cfg CameraConfig

	mu sync.RWMutex

	pppp        *PPPP
	startedAt   time.Time
	connectedAt time.Time
	peerAddress string
	peerPort    int

	latestFrame []byte
	lastVideoAt time.Time
	lastAudioAt time.Time
	lastTraffic time.Time

	videoFrames uint64
	audioFrames uint64

	videoMetric []metricPoint
	audioMetric []metricPoint

	lastFrameBytes int
	streamMode     string
	lastError      string
	lastCommand    string
	restartCount   int
	lastRestartAt  time.Time

	lastVideoRequest time.Time
	lastAudioRequest time.Time

	videoClients map[*Client]struct{}
	audioClients map[*Client]struct{}

	lastSnapshotDemand time.Time
	stopCh             chan struct{}
}

type PPPP struct {
	cfg      CameraConfig
	key      [4]byte
	conn     *net.UDPConn
	ctx      context.Context
	cancel   context.CancelFunc
	verbose  bool
	closed   atomic.Bool
	outIndex uint32

	mu          sync.RWMutex
	remote      *net.UDPAddr
	expectedIP  string
	punchCount  int
	isConnected bool

	videoMu    sync.Mutex
	videoFrame *videoFrame

	audio ADPCMDecoder

	OnPacket    func(Packet, *net.UDPAddr)
	OnConnected func(*net.UDPAddr)
	OnVideo     func([]byte, uint16)
	OnAudio     func([]byte, uint16)
	OnCommand   func(string)
	OnClose     func(string)
	OnLog       func(string)
}

type videoFrame struct {
	startIndex     uint16
	expectedLength int
	receivedLength int
	maxSlot        int
	chunks         map[int][]byte
}

type ADPCMDecoder struct {
	index   int
	valPred int
}

var indexTable = [16]int{-1, -1, -1, -1, 2, 4, 6, 8, -1, -1, -1, -1, 2, 4, 6, 8}
var stepTable = [89]int{
	7, 8, 9, 10, 11, 12, 13, 14, 16, 17, 19, 21, 23, 25, 28, 31, 34, 37, 41, 45,
	50, 55, 60, 66, 73, 80, 88, 97, 107, 118, 130, 143, 157, 173, 190, 209, 230,
	253, 279, 307, 337, 371, 408, 449, 494, 544, 598, 658, 724, 796, 876, 963,
	1060, 1166, 1282, 1411, 1552, 1707, 1878, 2066, 2272, 2499, 2749, 3024, 3327,
	3660, 4026, 4428, 4871, 5358, 5894, 6484, 7132, 7845, 8630, 9493, 10442,
	11487, 12635, 13899, 15289, 16818, 18500, 20350, 22385, 24623, 27086, 29794,
	32767,
}

type App struct {
	configPath string
	config     Config
	runtimes   map[string]*CameraRuntime
	publicHost string
	port       int
	bind       string
	mu         sync.RWMutex
}

func main() {
	configPath := flag.String("config", defaultConfigPath(), "config path")
	portOverride := flag.Int("port", 0, "override HTTP port")
	flag.Parse()

	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	if cfg.Server.Host == "" {
		cfg.Server.Host = "127.0.0.1"
	}
	if cfg.Server.Port == 0 {
		cfg.Server.Port = 8088
	}
	if *portOverride != 0 {
		cfg.Server.Port = *portOverride
	}
	if cfg.Server.Bind == "" {
		cfg.Server.Bind = "0.0.0.0"
	}

	app := &App{
		configPath: *configPath,
		config:     cfg,
		runtimes:   map[string]*CameraRuntime{},
		publicHost: cfg.Server.Host,
		port:       cfg.Server.Port,
		bind:       cfg.Server.Bind,
	}
	for _, cam := range cfg.Cameras {
		if cam.ID == "" {
			continue
		}
		rt := NewCameraRuntime(cam)
		app.runtimes[cam.ID] = rt
		if cam.enabled() {
			rt.Start()
		}
	}

	addr := net.JoinHostPort(app.bind, strconv.Itoa(app.port))
	log.Printf("BKCam Go listening on http://%s:%d", app.publicHost, app.port)
	log.Printf("config %s", app.configPath)
	if err := http.ListenAndServe(addr, app.routes()); err != nil {
		log.Fatal(err)
	}
}

func defaultConfigPath() string {
	if v := os.Getenv("BKCAM_CONFIG"); v != "" {
		return v
	}
	candidates := []string{
		"config.json",
		filepath.Join("..", "bkcam-server", "config.json"),
		filepath.Join("bkcam-go", "config.json"),
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return "config.json"
}

func loadConfig(path string) (Config, error) {
	fallback := Config{}
	fallback.Server.Host = "127.0.0.1"
	fallback.Server.Port = 8088
	fallback.Server.Bind = "0.0.0.0"
	source := path
	if _, err := os.Stat(source); err != nil {
		example := filepath.Join(filepath.Dir(path), "config.example.json")
		if _, exampleErr := os.Stat(example); exampleErr == nil {
			source = example
		} else {
			return fallback, nil
		}
	}
	b, err := os.ReadFile(source)
	if err != nil {
		return fallback, err
	}
	if err := json.Unmarshal(b, &fallback); err != nil {
		return fallback, err
	}
	return fallback, nil
}

func (a *App) saveConfigLocked() error {
	if err := os.MkdirAll(filepath.Dir(a.configPath), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(a.config, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	tmp := fmt.Sprintf("%s.%d.tmp", a.configPath, os.Getpid())
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, a.configPath)
}

func readJSONBody(r *http.Request) (map[string]any, error) {
	defer r.Body.Close()
	b, err := io.ReadAll(io.LimitReader(r.Body, 128*1024+1))
	if err != nil {
		return nil, err
	}
	if len(b) > 128*1024 {
		return nil, httpErr(http.StatusRequestEntityTooLarge, "request body too large")
	}
	if len(strings.TrimSpace(string(b))) == 0 {
		return map[string]any{}, nil
	}
	var body map[string]any
	if err := json.Unmarshal(b, &body); err != nil {
		return nil, httpErr(http.StatusBadRequest, "invalid JSON body")
	}
	return body, nil
}

type apiError struct {
	status int
	msg    string
}

func (e apiError) Error() string {
	return e.msg
}

func httpErr(status int, msg string) error {
	return apiError{status: status, msg: msg}
}

func writeAPIError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	if e, ok := err.(apiError); ok {
		status = e.status
	}
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func asStringValue(v any, fallback string) string {
	if v == nil {
		return fallback
	}
	return strings.TrimSpace(fmt.Sprint(v))
}

func inputString(input map[string]any, key, fallback string) string {
	if v, ok := input[key]; ok {
		return asStringValue(v, "")
	}
	return fallback
}

func inputPassword(input map[string]any, key, fallback string) string {
	if v, ok := input[key]; ok {
		s := fmt.Sprint(v)
		if s != "" {
			return s
		}
	}
	return fallback
}

func asBoolValue(v any, fallback bool) bool {
	if v == nil {
		return fallback
	}
	switch x := v.(type) {
	case bool:
		return x
	case string:
		s := strings.ToLower(strings.TrimSpace(x))
		if s == "" {
			return fallback
		}
		return s == "1" || s == "true" || s == "yes" || s == "on" || s == "enabled"
	case float64:
		return x != 0
	case int:
		return x != 0
	default:
		return fallback
	}
}

func inputBool(input map[string]any, key string, fallback bool) bool {
	if v, ok := input[key]; ok {
		return asBoolValue(v, fallback)
	}
	return fallback
}

func asIntValue(v any, fallback int) int {
	if v == nil {
		return fallback
	}
	switch x := v.(type) {
	case float64:
		return int(x + 0.5)
	case int:
		return x
	case string:
		if strings.TrimSpace(x) == "" {
			return fallback
		}
		n, err := strconv.Atoi(strings.TrimSpace(x))
		if err != nil {
			return fallback
		}
		return n
	default:
		return fallback
	}
}

func inputInt(input map[string]any, key string, fallback int) int {
	if v, ok := input[key]; ok {
		return asIntValue(v, fallback)
	}
	return fallback
}

func boolPtr(v bool) *bool {
	return &v
}

func validCameraID(id string) bool {
	if id == "" {
		return false
	}
	for _, r := range id {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			continue
		}
		return false
	}
	return true
}

func normalizeCamera(input map[string]any, existing *CameraConfig) (CameraConfig, error) {
	var cam CameraConfig
	if existing != nil {
		cam = *existing
	}
	id := inputString(input, "id", cam.ID)
	if id == "" {
		return cam, httpErr(http.StatusBadRequest, "camera id is required")
	}
	if !validCameraID(id) {
		return cam, httpErr(http.StatusBadRequest, "camera id must match /^[A-Za-z0-9_-]+$/")
	}
	cam.ID = id
	cam.Name = inputString(input, "name", firstNonEmpty(cam.Name, id))
	cam.IP = inputString(input, "ip", cam.IP)
	cam.Discovery = inputString(input, "discovery", firstNonEmpty(cam.Discovery, cam.IP, "255.255.255.255"))
	cam.LocalAddress = inputString(input, "localAddress", cam.LocalAddress)
	cam.PSK = inputString(input, "psk", firstNonEmpty(cam.PSK, "SHIX"))
	cam.Username = inputString(input, "username", firstNonEmpty(cam.Username, "admin"))
	cam.Password = inputPassword(input, "password", firstNonEmpty(cam.Password, "6666"))
	cam.Width = inputInt(input, "width", cam.width())
	cam.Height = inputInt(input, "height", cam.height())
	enabled := inputBool(input, "enabled", cam.enabled())
	verbose := inputBool(input, "verbose", cam.Verbose)
	avStream := inputBool(input, "avStream", cam.avStream())
	cam.Enabled = boolPtr(enabled)
	cam.Verbose = verbose
	cam.AvStream = boolPtr(avStream)
	cam.AckRepeats = inputInt(input, "ackRepeats", cam.ackRepeats())
	if cam.AckRepeats < 1 {
		cam.AckRepeats = 1
	}
	if cam.AckRepeats > 9 {
		cam.AckRepeats = 9
	}
	if cam.IP == "" && cam.Discovery == "" {
		return cam, httpErr(http.StatusBadRequest, "camera ip or discovery address is required")
	}
	return cam, nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	b, _ := json.MarshalIndent(v, "", "  ")
	w.Header().Set("content-type", "application/json; charset=utf-8")
	w.Header().Set("cache-control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

func writeText(w http.ResponseWriter, status int, text, contentType string) {
	w.Header().Set("content-type", contentType)
	w.Header().Set("cache-control", "no-store")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, text)
}

func (a *App) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", a.handleRoot)
	return mux
}

func (a *App) handleRoot(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if strings.HasPrefix(path, "/api/") {
		if err := a.handleAPI(w, r, path); err != nil {
			writeAPIError(w, err)
		}
		return
	}
	switch {
	case path == "/":
		writeText(w, http.StatusOK, a.renderPage(r, "", "dashboard"), "text/html; charset=utf-8")
	case path == "/frigate.yml":
		writeText(w, http.StatusOK, a.renderFrigate(baseURL(r)), "text/yaml; charset=utf-8")
	case path == "/go2rtc.yml":
		writeText(w, http.StatusOK, a.renderGo2RTC(baseURL(r)), "text/yaml; charset=utf-8")
	case path == "/setup":
		writeText(w, http.StatusOK, a.renderPage(r, "", "setup"), "text/html; charset=utf-8")
	case strings.HasPrefix(path, "/cam/"):
		a.handleCamera(w, r)
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	}
}

func (a *App) handleAPI(w http.ResponseWriter, r *http.Request, path string) error {
	method := r.Method
	if path == "/api/status" && method == http.MethodGet {
		writeJSON(w, http.StatusOK, map[string]any{
			"server":  map[string]any{"host": a.publicHost, "port": a.port, "bind": a.bind, "backend": "go"},
			"cameras": a.allStatuses(r),
		})
		return nil
	}
	if path == "/api/config" && method == http.MethodGet {
		writeJSON(w, http.StatusOK, map[string]any{
			"configPath": a.configPath,
			"server":     map[string]any{"host": a.publicHost, "port": a.port, "bind": a.bind, "backend": "go"},
			"cameras":    a.publicConfigs(),
		})
		return nil
	}
	if path == "/api/setup/provision" && method == http.MethodPost {
		body, err := readJSONBody(r)
		if err != nil {
			return err
		}
		res, err := a.provisionCamera(body, r)
		if err != nil {
			return err
		}
		writeJSON(w, http.StatusOK, res)
		return nil
	}
	if path == "/api/cameras" {
		switch method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, map[string]any{"cameras": a.publicConfigsWithStatus(r)})
			return nil
		case http.MethodPost:
			body, err := readJSONBody(r)
			if err != nil {
				return err
			}
			camera, err := normalizeCamera(body, nil)
			if err != nil {
				return err
			}
			a.mu.Lock()
			if a.cameraIndexLocked(camera.ID) != -1 {
				a.mu.Unlock()
				return httpErr(http.StatusConflict, "camera already exists")
			}
			a.config.Cameras = append(a.config.Cameras, camera)
			if err := a.saveConfigLocked(); err != nil {
				a.mu.Unlock()
				return err
			}
			rt := a.upsertRuntimeLocked(camera)
			a.mu.Unlock()
			writeJSON(w, http.StatusCreated, map[string]any{
				"camera": publicConfig(camera),
				"status": rt.Status(baseURL(r)),
			})
			return nil
		default:
			return httpErr(http.StatusMethodNotAllowed, "method not allowed")
		}
	}
	if !strings.HasPrefix(path, "/api/cameras/") {
		return httpErr(http.StatusNotFound, "not found")
	}

	rest := strings.TrimPrefix(path, "/api/cameras/")
	parts := strings.SplitN(rest, "/", 2)
	id, err := urlPathUnescape(parts[0])
	if err != nil {
		return httpErr(http.StatusBadRequest, "bad camera id")
	}
	action := ""
	if len(parts) == 2 {
		action = parts[1]
	}

	a.mu.RLock()
	idx := a.cameraIndexLocked(id)
	if idx == -1 {
		a.mu.RUnlock()
		return httpErr(http.StatusNotFound, "unknown camera")
	}
	camera := a.config.Cameras[idx]
	rt := a.runtimes[id]
	a.mu.RUnlock()
	if rt == nil {
		return httpErr(http.StatusConflict, "camera runtime is not available")
	}

	if action == "" && method == http.MethodGet {
		writeJSON(w, http.StatusOK, map[string]any{
			"camera": publicConfig(camera),
			"status": rt.Status(baseURL(r)),
		})
		return nil
	}
	if action == "" && (method == http.MethodPatch || method == http.MethodPut) {
		body, err := readJSONBody(r)
		if err != nil {
			return err
		}
		if nextID := inputString(body, "id", id); nextID != id {
			return httpErr(http.StatusBadRequest, "camera id cannot be changed")
		}
		body["id"] = id
		next, err := normalizeCamera(body, &camera)
		if err != nil {
			return err
		}
		a.mu.Lock()
		idx = a.cameraIndexLocked(id)
		if idx == -1 {
			a.mu.Unlock()
			return httpErr(http.StatusNotFound, "unknown camera")
		}
		a.config.Cameras[idx] = next
		if err := a.saveConfigLocked(); err != nil {
			a.mu.Unlock()
			return err
		}
		nextRuntime := a.upsertRuntimeLocked(next)
		a.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{
			"camera": publicConfig(next),
			"status": nextRuntime.Status(baseURL(r)),
		})
		return nil
	}
	if action == "" && method == http.MethodDelete {
		a.mu.Lock()
		idx = a.cameraIndexLocked(id)
		if idx == -1 {
			a.mu.Unlock()
			return httpErr(http.StatusNotFound, "unknown camera")
		}
		a.config.Cameras = append(a.config.Cameras[:idx], a.config.Cameras[idx+1:]...)
		if err := a.saveConfigLocked(); err != nil {
			a.mu.Unlock()
			return err
		}
		a.removeRuntimeLocked(id)
		a.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id})
		return nil
	}
	if method != http.MethodPost {
		return httpErr(http.StatusMethodNotAllowed, "method not allowed")
	}

	body, err := readJSONBody(r)
	if err != nil {
		return err
	}
	switch action {
	case "restart":
		rt.restart("manual restart")
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id})
	case "wifi":
		result := rt.SetWifi(inputString(body, "ssid", ""), inputPassword(body, "password", ""))
		if result.OK && inputBool(body, "reboot", false) {
			time.AfterFunc(1500*time.Millisecond, func() { _ = rt.Reboot() })
		}
		if result.OK {
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id})
		} else {
			writeJSON(w, http.StatusConflict, result)
		}
	case "scan-wifi":
		result := rt.ScanWifi()
		if result.OK {
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id})
		} else {
			writeJSON(w, http.StatusConflict, result)
		}
	case "params":
		result := rt.RefreshParams()
		if result.OK {
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id})
		} else {
			writeJSON(w, http.StatusConflict, result)
		}
	case "reboot":
		result := rt.Reboot()
		if result.OK {
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id})
		} else {
			writeJSON(w, http.StatusConflict, result)
		}
	default:
		return httpErr(http.StatusNotFound, "unknown camera action")
	}
	return nil
}

func baseURL(r *http.Request) string {
	host := r.Host
	if host == "" {
		host = "127.0.0.1"
	}
	proto := r.Header.Get("x-forwarded-proto")
	if proto == "" {
		proto = "http"
	}
	return proto + "://" + host
}

func (a *App) runtime(id string) *CameraRuntime {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.runtimes[id]
}

func (a *App) sortedRuntimes() []*CameraRuntime {
	a.mu.RLock()
	defer a.mu.RUnlock()
	ids := make([]string, 0, len(a.runtimes))
	for id := range a.runtimes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]*CameraRuntime, 0, len(ids))
	for _, id := range ids {
		out = append(out, a.runtimes[id])
	}
	return out
}

func (a *App) allStatuses(r *http.Request) []map[string]any {
	base := baseURL(r)
	var out []map[string]any
	for _, rt := range a.sortedRuntimes() {
		out = append(out, rt.Status(base))
	}
	return out
}

func (a *App) publicConfigs() []map[string]any {
	out := make([]map[string]any, 0, len(a.config.Cameras))
	for _, cam := range a.config.Cameras {
		out = append(out, publicConfig(cam))
	}
	return out
}

func (a *App) publicConfigsWithStatus(r *http.Request) []map[string]any {
	base := baseURL(r)
	out := make([]map[string]any, 0, len(a.config.Cameras))
	for _, cam := range a.config.Cameras {
		item := publicConfig(cam)
		if rt := a.runtime(cam.ID); rt != nil {
			item["status"] = rt.Status(base)
		}
		out = append(out, item)
	}
	return out
}

func (a *App) cameraIndexLocked(id string) int {
	for i, cam := range a.config.Cameras {
		if cam.ID == id {
			return i
		}
	}
	return -1
}

func (a *App) upsertRuntimeLocked(camera CameraConfig) *CameraRuntime {
	if rt := a.runtimes[camera.ID]; rt != nil {
		rt.UpdateConfig(camera)
		return rt
	}
	rt := NewCameraRuntime(camera)
	a.runtimes[camera.ID] = rt
	if camera.enabled() {
		rt.Start()
	}
	return rt
}

func (a *App) upsertRuntime(camera CameraConfig) *CameraRuntime {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.upsertRuntimeLocked(camera)
}

func (a *App) removeRuntimeLocked(id string) bool {
	rt := a.runtimes[id]
	if rt == nil {
		return false
	}
	rt.Stop()
	delete(a.runtimes, id)
	return true
}

func (a *App) removeRuntime(id string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.removeRuntimeLocked(id)
}

func (a *App) defaultLanDiscovery() string {
	ip := net.ParseIP(a.publicHost).To4()
	if ip == nil {
		return "255.255.255.255"
	}
	return fmt.Sprintf("%d.%d.%d.255", ip[0], ip[1], ip[2])
}

func (a *App) provisionCamera(body map[string]any, r *http.Request) (map[string]any, error) {
	id := inputString(body, "id", "")
	if id == "" {
		return nil, httpErr(http.StatusBadRequest, "camera id is required")
	}
	if !validCameraID(id) {
		return nil, httpErr(http.StatusBadRequest, "camera id must match /^[A-Za-z0-9_-]+$/")
	}
	ssid := inputString(body, "ssid", "")
	wifiPassword := inputPassword(body, "wifiPassword", "")
	if ssid == "" {
		return nil, httpErr(http.StatusBadRequest, "target SSID is required")
	}
	if wifiPassword == "" {
		return nil, httpErr(http.StatusBadRequest, "target Wi-Fi password is required")
	}

	a.mu.RLock()
	idx := a.cameraIndexLocked(id)
	var existing *CameraConfig
	if idx != -1 {
		copy := a.config.Cameras[idx]
		existing = &copy
	}
	a.mu.RUnlock()

	setupIP := inputString(body, "setupIp", "")
	setupDiscovery := inputString(body, "setupDiscovery", firstNonEmpty(setupIP, "255.255.255.255"))
	setupInput := map[string]any{
		"id":           id,
		"name":         inputString(body, "name", firstNonEmpty(existingName(existing), id)),
		"ip":           setupIP,
		"discovery":    setupDiscovery,
		"localAddress": inputString(body, "localAddress", existingLocalAddress(existing)),
		"psk":          inputString(body, "psk", firstNonEmpty(existingPSK(existing), "SHIX")),
		"username":     inputString(body, "username", firstNonEmpty(existingUsername(existing), "admin")),
		"password":     inputPassword(body, "password", firstNonEmpty(existingPassword(existing), "6666")),
		"width":        inputInt(body, "width", existingWidth(existing)),
		"height":       inputInt(body, "height", existingHeight(existing)),
		"ackRepeats":   inputInt(body, "ackRepeats", existingAckRepeats(existing)),
		"enabled":      true,
		"avStream":     inputBool(body, "avStream", existingAvStream(existing)),
		"verbose":      inputBool(body, "verbose", existingVerbose(existing)),
	}
	setupCamera, err := normalizeCamera(setupInput, existing)
	if err != nil {
		return nil, err
	}

	runtime := a.upsertRuntime(setupCamera)
	if !runtime.WaitForSession(18 * time.Second) {
		if existing != nil {
			a.upsertRuntime(*existing)
		} else {
			a.removeRuntime(id)
		}
		return nil, httpErr(http.StatusConflict, fmt.Sprintf("camera did not answer at %s", setupCamera.discovery()))
	}

	result := runtime.SetWifi(ssid, wifiPassword)
	if !result.OK {
		if existing != nil {
			a.upsertRuntime(*existing)
		} else {
			a.removeRuntime(id)
		}
		return nil, httpErr(http.StatusConflict, firstNonEmpty(result.Error, "failed to send Wi-Fi settings"))
	}
	if inputBool(body, "reboot", true) {
		time.Sleep(1200 * time.Millisecond)
		_ = runtime.Reboot()
	}

	finalIP := inputString(body, "finalIp", "")
	finalDiscovery := inputString(body, "finalDiscovery", firstNonEmpty(finalIP, a.defaultLanDiscovery()))
	finalCamera, err := normalizeCamera(map[string]any{
		"id":        id,
		"name":      setupCamera.Name,
		"ip":        finalIP,
		"discovery": finalDiscovery,
		"enabled":   true,
	}, &setupCamera)
	if err != nil {
		return nil, err
	}

	a.mu.Lock()
	idx = a.cameraIndexLocked(id)
	if idx == -1 {
		a.config.Cameras = append(a.config.Cameras, finalCamera)
	} else {
		a.config.Cameras[idx] = finalCamera
	}
	if err := a.saveConfigLocked(); err != nil {
		a.mu.Unlock()
		return nil, err
	}
	finalRuntime := a.upsertRuntimeLocked(finalCamera)
	a.mu.Unlock()

	return map[string]any{
		"ok":      true,
		"message": "Wi-Fi sent and camera config saved",
		"camera":  publicConfig(finalCamera),
		"status":  finalRuntime.Status(baseURL(r)),
	}, nil
}

func existingName(c *CameraConfig) string {
	if c == nil {
		return ""
	}
	return c.Name
}

func existingLocalAddress(c *CameraConfig) string {
	if c == nil {
		return ""
	}
	return c.LocalAddress
}

func existingPSK(c *CameraConfig) string {
	if c == nil {
		return ""
	}
	return c.PSK
}

func existingUsername(c *CameraConfig) string {
	if c == nil {
		return ""
	}
	return c.Username
}

func existingPassword(c *CameraConfig) string {
	if c == nil {
		return ""
	}
	return c.Password
}

func existingWidth(c *CameraConfig) int {
	if c == nil {
		return 640
	}
	return c.width()
}

func existingHeight(c *CameraConfig) int {
	if c == nil {
		return 480
	}
	return c.height()
}

func existingAckRepeats(c *CameraConfig) int {
	if c == nil {
		return 3
	}
	return c.ackRepeats()
}

func existingAvStream(c *CameraConfig) bool {
	if c == nil {
		return true
	}
	return c.avStream()
}

func existingVerbose(c *CameraConfig) bool {
	if c == nil {
		return false
	}
	return c.Verbose
}

func publicConfig(cam CameraConfig) map[string]any {
	return map[string]any{
		"id":           cam.ID,
		"name":         cam.name(),
		"ip":           cam.IP,
		"discovery":    cam.Discovery,
		"localAddress": cam.LocalAddress,
		"psk":          cam.psk(),
		"username":     cam.username(),
		"hasPassword":  cam.Password != "",
		"width":        cam.width(),
		"height":       cam.height(),
		"videoMode":    cam.videoMode(),
		"enabled":      cam.enabled(),
		"verbose":      cam.Verbose,
		"avStream":     cam.avStream(),
		"ackRepeats":   cam.ackRepeats(),
	}
}

func (a *App) handleCamera(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/cam/")
	parts := strings.SplitN(rest, "/", 2)
	id, err := urlPathUnescape(parts[0])
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad camera id"})
		return
	}
	action := ""
	if len(parts) == 2 {
		action = parts[1]
	}
	rt := a.runtime(id)
	if rt == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown camera", "id": id})
		return
	}
	switch action {
	case "":
		writeText(w, http.StatusOK, a.renderPage(r, id), "text/html; charset=utf-8")
	case "video.mjpg":
		rt.ServeVideo(w, r)
	case "audio.wav":
		rt.ServeAudio(w, r, true)
	case "audio.raw":
		rt.ServeAudio(w, r, false)
	case "snapshot.jpg", "preview.jpg":
		rt.ServeSnapshot(w, r)
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown camera endpoint", "id": id})
	}
}

func urlPathUnescape(s string) (string, error) {
	out, err := url.PathUnescape(s)
	if err != nil {
		return "", err
	}
	if strings.Contains(out, "/") {
		return "", errors.New("slash in id")
	}
	return out, nil
}

func htmlValue(v any) string {
	if v == nil {
		return ""
	}
	s := fmt.Sprint(v)
	if s == "<nil>" {
		return ""
	}
	return html.EscapeString(s)
}

func (a *App) renderPage(r *http.Request, cameraID string, mode ...string) string {
	isSetup := len(mode) > 0 && mode[0] == "setup"
	statuses := a.allStatuses(r)
	configs := map[string]map[string]any{}
	for _, cfg := range a.publicConfigs() {
		configs[fmt.Sprint(cfg["id"])] = cfg
	}

	var cameras []map[string]any
	if !isSetup {
		for _, st := range statuses {
			if cameraID != "" && fmt.Sprint(st["id"]) != cameraID {
				continue
			}
			cameras = append(cameras, st)
		}
	}

	manager := ""
	if isSetup {
		manager = renderWizard(a.defaultLanDiscovery())
	} else if cameraID == "" {
		manager = `<section class="overview-head">
        <div>
          <h2>Cameras</h2>
          <p class="meta">Snapshot previews keep the overview light; open a camera for live video and sound.</p>
        </div>
        <a class="primary" href="/setup">Set up new camera</a>
      </section>`
	}

	var cards []string
	for _, c := range cameras {
		id := fmt.Sprint(c["id"])
		pathID := url.PathEscape(id)
		name := htmlValue(c["name"])
		ip := htmlValue(c["ip"])
		state := htmlValue(firstNonEmpty(fmt.Sprint(c["healthState"]), "offline"))
		label := htmlValue(firstNonEmpty(fmt.Sprint(c["healthLabel"]), "offline"))
		media := fmt.Sprintf(`<a href="/cam/%s"><img data-preview="%s" src="/cam/%s/snapshot.jpg?ts=%d" alt="%s preview"></a>`, pathID, htmlValue(id), pathID, time.Now().UnixMilli(), name)
		if cameraID != "" {
			media = fmt.Sprintf(`<img data-live="%s" src="/cam/%s/video.mjpg?ts=%d" alt="%s live video">`, htmlValue(id), pathID, time.Now().UnixMilli(), name)
		}
		reconnectButton := ""
		if cameraID != "" {
			reconnectButton = fmt.Sprintf(`<button data-live-reconnect="%s">Reconnect video</button>`, htmlValue(id))
		}
		cards = append(cards, fmt.Sprintf(`<section class="camera %s" data-camera-id="%s">
      <header class="camera-head">
        <div>
          <h2>%s</h2>
          <p class="meta">%s - %s</p>
        </div>
        <span class="state %s">%s</span>
      </header>
      <div class="media">%s</div>
      <div class="toolbar">
        <a href="/cam/%s">Open live</a>
        <button data-audio="%s">Sound</button>
        %s
        <a href="/cam/%s/snapshot.jpg" target="_blank">Snapshot</a>
      </div>
      <audio id="audio-%s" controls preload="none" hidden></audio>
      <dl class="stats">
        <div><dt>Video</dt><dd data-field="%s:videoFrames">%s</dd></div>
        <div><dt>FPS</dt><dd data-field="%s:videoFps">%s</dd></div>
        <div><dt>Video kbps</dt><dd data-field="%s:videoKbps">%s</dd></div>
        <div><dt>Audio</dt><dd data-field="%s:audioFrames">%s</dd></div>
        <div><dt>Status</dt><dd data-field="%s:healthLabel">%s</dd></div>
        <div><dt>Clients</dt><dd data-field="%s:clients">%s/%s</dd></div>
        <div><dt>Restarts</dt><dd data-field="%s:restartCount">%s</dd></div>
        <div><dt>Mode</dt><dd data-field="%s:streamMode">%s</dd></div>
        <div><dt>Peer</dt><dd data-field="%s:peer">%s</dd></div>
      </dl>
      <details>
        <summary>Settings</summary>
        %s
      </details>
      <details>
        <summary>Maintenance</summary>
        <div class="toolbar maintenance">
          <button data-command="restart" data-id="%s">Reconnect camera session</button>
          <button data-command="params" data-id="%s">Refresh camera info</button>
          <button data-command="reboot" data-confirm="Restart camera hardware?" data-id="%s">Restart camera hardware</button>
          <a href="/cam/%s/video.mjpg" target="_blank">Raw MJPEG</a>
          <a href="/cam/%s/audio.wav" target="_blank">Raw WAV</a>
        </div>
        <form data-wifi-camera="%s" class="config-form compact">
          <label><span>Wi-Fi SSID</span><input name="ssid" autocomplete="off"></label>
          <label><span>Wi-Fi password</span><input name="password" type="password" autocomplete="new-password"></label>
          <label class="check"><input name="reboot" type="checkbox" checked><span>Reboot</span></label>
          <button type="submit">Set Wi-Fi</button>
          <output></output>
        </form>
      </details>
    </section>`,
			mapBool(cameraID != "", "detail", "summary-card"), htmlValue(id), name, ip, htmlValue(id), state, label, media,
			pathID, htmlValue(id), reconnectButton, pathID, htmlValue(id),
			htmlValue(id), htmlValue(c["videoFrames"]),
			htmlValue(id), htmlValue(c["videoFps"]),
			htmlValue(id), htmlValue(c["videoKbps"]),
			htmlValue(id), htmlValue(c["audioFrames"]),
			htmlValue(id), label,
			htmlValue(id), htmlValue(c["videoClients"]), htmlValue(c["audioClients"]),
			htmlValue(id), htmlValue(c["restartCount"]),
			htmlValue(id), htmlValue(c["streamMode"]),
			htmlValue(id), htmlValue(firstNonEmpty(fmt.Sprint(c["peerAddress"]), fmt.Sprint(c["expectedAddress"]))),
			renderCameraConfigForm(configs[id]),
			htmlValue(id), htmlValue(id), htmlValue(id), pathID, pathID, htmlValue(id)))
	}

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
  <main>` + manager + `<div class="grid ` + mapBool(cameraID != "", "detail-grid", "") + `">` + strings.Join(cards, "") + `</div></main>
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
      const buffer = ctx.createBuffer(1, samples, ` + strconv.Itoa(audioSampleRate) + `)
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
      if (state.button) state.button.textContent = 'Sound'
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
        if (!audio.src) audio.src = cameraPath(id, '/audio.wav')
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
        const res = await fetch(cameraPath(id, '/audio.raw'), { cache: 'no-store', signal: controller.signal })
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
          if (!audio.src) audio.src = cameraPath(id, '/audio.wav')
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
        if (form.dataset.provisionCamera !== undefined) {
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

func renderInput(name, label string, value any, attrs string) string {
	return fmt.Sprintf(`<label><span>%s</span><input name="%s" value="%s" %s></label>`, htmlValue(label), htmlValue(name), htmlValue(value), attrs)
}

func renderCameraConfigForm(camera map[string]any) string {
	if camera == nil {
		return ""
	}
	return `<form data-update-camera="` + htmlValue(camera["id"]) + `" class="config-form">
      ` + renderInput("name", "Name", camera["name"], "") + `
      ` + renderInput("ip", "Camera IP", camera["ip"], `inputmode="numeric" autocomplete="off"`) + `
      ` + renderInput("discovery", "Discovery", camera["discovery"], `inputmode="numeric" autocomplete="off"`) + `
      ` + renderInput("localAddress", "Local bind", camera["localAddress"], `inputmode="numeric" autocomplete="off"`) + `
      ` + renderInput("psk", "PSK", camera["psk"], `autocomplete="off"`) + `
      ` + renderInput("username", "User", camera["username"], `autocomplete="off"`) + `
      ` + renderInput("password", "Password", "", `type="password" placeholder="keep current" autocomplete="new-password"`) + `
      ` + renderInput("ackRepeats", "ACK repeats", camera["ackRepeats"], `type="number" min="1" max="9"`) + `
      ` + renderInput("width", "Width", camera["width"], `type="number" min="1"`) + `
      ` + renderInput("height", "Height", camera["height"], `type="number" min="1"`) + `
      <label class="check"><input name="avStream" type="checkbox" ` + mapBool(asBoolValue(camera["avStream"], true), "checked", "") + `><span>Request AV stream</span></label>
      <label class="check"><input name="enabled" type="checkbox" ` + mapBool(asBoolValue(camera["enabled"], true), "checked", "") + `><span>Enabled</span></label>
      <button type="submit">Save</button>
      <output></output>
    </form>`
}

func renderWizard(lanDiscovery string) string {
	return `<section class="wizard setup-wizard">
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
            <label><span>Final discovery</span><input name="finalDiscovery" placeholder="` + htmlValue(lanDiscovery) + `" inputmode="numeric" autocomplete="off"></label>
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

func (a *App) renderFrigate(base string) string {
	var streams []string
	var cams []string
	for _, rt := range a.sortedRuntimes() {
		if !rt.cfg.enabled() {
			continue
		}
		id := rt.cfg.ID
		streams = append(streams, fmt.Sprintf("    %s:\n      - %s\n      - %s", id, yamlQuote(fmt.Sprintf("ffmpeg:%s/cam/%s/video.mjpg#video=h264", base, id)), yamlQuote(fmt.Sprintf("ffmpeg:%s/cam/%s/audio.wav#audio=aac#audio=opus", base, id))))
		cams = append(cams, fmt.Sprintf("  %s:\n    ffmpeg:\n      inputs:\n        - path: %s\n          input_args: preset-rtsp-restream\n          roles:\n            - detect\n            - record\n    detect:\n      width: %d\n      height: %d", id, yamlQuote(fmt.Sprintf("rtsp://127.0.0.1:8554/%s?video=h264&audio=aac", id)), rt.cfg.width(), rt.cfg.height()))
	}
	return "ffmpeg:\n  output_args:\n    record: preset-record-generic-audio-aac\n\ngo2rtc:\n  streams:\n" + strings.Join(streams, "\n") + "\n\ncameras:\n" + strings.Join(cams, "\n") + "\n"
}

func (a *App) renderGo2RTC(base string) string {
	var streams []string
	for _, rt := range a.sortedRuntimes() {
		if !rt.cfg.enabled() {
			continue
		}
		id := rt.cfg.ID
		streams = append(streams, fmt.Sprintf("  %s:\n    - %s\n    - %s", id, yamlQuote(fmt.Sprintf("ffmpeg:%s/cam/%s/video.mjpg#video=h264", base, id)), yamlQuote(fmt.Sprintf("ffmpeg:%s/cam/%s/audio.wav#audio=aac#audio=opus", base, id))))
	}
	return "streams:\n" + strings.Join(streams, "\n") + "\n"
}

func NewCameraRuntime(cfg CameraConfig) *CameraRuntime {
	return &CameraRuntime{
		cfg:          cfg,
		streamMode:   "idle",
		videoClients: map[*Client]struct{}{},
		audioClients: map[*Client]struct{}{},
		stopCh:       make(chan struct{}),
	}
}

func (rt *CameraRuntime) UpdateConfig(cfg CameraConfig) {
	rt.Stop()
	rt.mu.Lock()
	rt.cfg = cfg
	rt.connectedAt = time.Time{}
	rt.peerAddress = ""
	rt.peerPort = 0
	rt.latestFrame = nil
	rt.lastVideoAt = time.Time{}
	rt.lastAudioAt = time.Time{}
	rt.lastTraffic = time.Time{}
	rt.videoMetric = nil
	rt.audioMetric = nil
	rt.lastFrameBytes = 0
	rt.streamMode = "idle"
	rt.lastCommand = ""
	rt.videoClients = map[*Client]struct{}{}
	rt.audioClients = map[*Client]struct{}{}
	rt.mu.Unlock()
	if cfg.enabled() {
		rt.Start()
	}
}

func (rt *CameraRuntime) Start() {
	rt.mu.Lock()
	if rt.pppp != nil || !rt.cfg.enabled() {
		rt.mu.Unlock()
		return
	}
	rt.startedAt = time.Now()
	rt.lastError = ""
	pp := NewPPPP(rt.cfg)
	rt.pppp = pp
	rt.mu.Unlock()

	pp.OnPacket = func(_ Packet, _ *net.UDPAddr) {
		rt.mu.Lock()
		rt.lastTraffic = time.Now()
		rt.mu.Unlock()
	}
	pp.OnConnected = func(addr *net.UDPAddr) {
		rt.mu.Lock()
		rt.connectedAt = time.Now()
		rt.lastTraffic = rt.connectedAt
		rt.peerAddress = addr.IP.String()
		rt.peerPort = addr.Port
		rt.lastError = ""
		rt.mu.Unlock()
		log.Printf("camera %s connected %s", rt.cfg.ID, addr.String())
		time.AfterFunc(200*time.Millisecond, func() { rt.requestStreams(true) })
		time.AfterFunc(800*time.Millisecond, func() { _ = rt.sendGetParams() })
	}
	pp.OnVideo = func(frame []byte, _ uint16) {
		now := time.Now()
		rt.mu.Lock()
		rt.latestFrame = append(rt.latestFrame[:0], frame...)
		rt.lastVideoAt = now
		rt.lastTraffic = now
		rt.lastFrameBytes = len(frame)
		rt.lastError = ""
		atomic.AddUint64(&rt.videoFrames, 1)
		rt.videoMetric = appendMetric(rt.videoMetric, now, len(frame))
		clients := keys(rt.videoClients)
		rt.mu.Unlock()
		for _, c := range clients {
			sendDropOld(c.ch, frame)
		}
	}
	pp.OnAudio = func(pcm []byte, _ uint16) {
		now := time.Now()
		rt.mu.Lock()
		rt.lastAudioAt = now
		rt.lastTraffic = now
		rt.lastError = ""
		atomic.AddUint64(&rt.audioFrames, 1)
		rt.audioMetric = appendMetric(rt.audioMetric, now, len(pcm))
		clients := keys(rt.audioClients)
		rt.mu.Unlock()
		for _, c := range clients {
			sendDropOld(c.ch, pcm)
		}
	}
	pp.OnCommand = func(cmd string) {
		rt.mu.Lock()
		rt.lastCommand = strings.TrimSpace(cmd)
		rt.lastTraffic = time.Now()
		rt.mu.Unlock()
	}
	pp.OnClose = func(reason string) {
		rt.restart(reason)
	}
	go pp.Run()
	go rt.healthLoop()
	go rt.streamLoop()
}

func (rt *CameraRuntime) WaitForSession(timeout time.Duration) bool {
	rt.Start()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		rt.mu.RLock()
		connected := !rt.connectedAt.IsZero()
		rt.mu.RUnlock()
		if connected {
			return true
		}
		time.Sleep(250 * time.Millisecond)
	}
	rt.mu.RLock()
	connected := !rt.connectedAt.IsZero()
	rt.mu.RUnlock()
	return connected
}

func (rt *CameraRuntime) Stop() {
	rt.mu.Lock()
	pp := rt.pppp
	rt.pppp = nil
	rt.connectedAt = time.Time{}
	rt.startedAt = time.Time{}
	rt.peerAddress = ""
	rt.peerPort = 0
	rt.mu.Unlock()
	if pp != nil {
		pp.Close()
	}
}

func (rt *CameraRuntime) restart(reason string) bool {
	now := time.Now()
	rt.mu.Lock()
	if !rt.lastRestartAt.IsZero() && now.Sub(rt.lastRestartAt) < restartCooldown {
		rt.lastError = reason + " (restart throttled)"
		rt.mu.Unlock()
		return false
	}
	rt.lastError = reason
	rt.lastRestartAt = now
	rt.restartCount++
	rt.mu.Unlock()
	log.Printf("camera %s restarting: %s", rt.cfg.ID, reason)
	rt.Stop()
	time.AfterFunc(1500*time.Millisecond, rt.Start)
	return true
}

func (rt *CameraRuntime) healthLoop() {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for range t.C {
		rt.mu.RLock()
		pp := rt.pppp
		started := rt.startedAt
		connected := rt.connectedAt
		lastTraffic := rt.lastTraffic
		rt.mu.RUnlock()
		if pp == nil {
			return
		}
		now := time.Now()
		if connected.IsZero() && !started.IsZero() && now.Sub(started) > 20*time.Second {
			if rt.restart("connect timeout") {
				return
			}
			continue
		}
		if !connected.IsZero() && !lastTraffic.IsZero() && now.Sub(lastTraffic) > trafficRestartWindow {
			if rt.restart("traffic timeout") {
				return
			}
			continue
		}
		if !connected.IsZero() {
			_ = pp.SendAlive()
		}
	}
}

func (rt *CameraRuntime) streamLoop() {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for range t.C {
		rt.mu.RLock()
		pp := rt.pppp
		now := time.Now()
		connectedAt := rt.connectedAt
		connected := !connectedAt.IsZero()
		hasVideo := len(rt.videoClients) > 0 || now.Sub(rt.lastSnapshotDemand) < 10*time.Second || len(rt.latestFrame) == 0
		hasAudio := len(rt.audioClients) > 0
		videoAge := now.Sub(rt.lastVideoAt)
		audioAge := now.Sub(rt.lastAudioAt)
		staleVideo := hasVideo && (rt.lastVideoAt.IsZero() || videoAge > videoStaleWindow)
		staleAudio := hasAudio && (rt.lastAudioAt.IsZero() || audioAge > audioStaleWindow)
		refreshVideo := hasVideo && !staleVideo && (rt.lastVideoRequest.IsZero() || now.Sub(rt.lastVideoRequest) >= videoRefreshWindow)
		deadVideo := hasVideo && connected && ((rt.lastVideoAt.IsZero() && now.Sub(connectedAt) > mediaRestartWindow) || (!rt.lastVideoAt.IsZero() && videoAge > mediaRestartWindow))
		deadAudio := hasAudio && connected && !hasVideo && ((rt.lastAudioAt.IsZero() && now.Sub(connectedAt) > mediaRestartWindow) || (!rt.lastAudioAt.IsZero() && audioAge > mediaRestartWindow))
		rt.mu.RUnlock()
		if pp == nil {
			return
		}
		if !connected {
			continue
		}
		if deadVideo {
			if rt.restart("video timeout") {
				return
			}
			continue
		}
		if deadAudio {
			if rt.restart("audio timeout") {
				return
			}
			continue
		}
		if staleVideo {
			rt.requestStreams(false)
		} else if refreshVideo {
			rt.requestStreams(false)
		} else if staleAudio {
			rt.requestStreams(false)
		}
	}
}

func (rt *CameraRuntime) requestStreams(forceVideo bool, videoOnly ...bool) {
	rt.mu.Lock()
	pp := rt.pppp
	now := time.Now()
	wantsVideo := forceVideo || len(rt.videoClients) > 0 || len(rt.latestFrame) == 0 || now.Sub(rt.lastSnapshotDemand) < 10*time.Second
	wantsAudio := len(rt.audioClients) > 0 || (rt.cfg.avStream() && wantsVideo)
	skipAudio := len(videoOnly) > 0 && videoOnly[0]
	sendVideo := wantsVideo && (forceVideo || rt.lastVideoRequest.IsZero() || now.Sub(rt.lastVideoRequest) >= streamRequestWindow)
	sendAudio := wantsAudio && !skipAudio && (forceVideo || rt.lastAudioRequest.IsZero() || now.Sub(rt.lastAudioRequest) >= streamRequestWindow)
	if sendVideo {
		rt.lastVideoRequest = now
	}
	if sendAudio {
		rt.lastAudioRequest = now
	}
	rt.mu.Unlock()
	if pp == nil {
		return
	}
	sentVideo := false
	if sendVideo {
		if err := pp.SendCommand(111, "stream", map[string]any{"video": rt.cfg.videoMode()}); err == nil {
			sentVideo = true
			rt.mu.Lock()
			rt.streamMode = "video"
			rt.mu.Unlock()
		}
	}
	if sendAudio {
		if err := pp.SendCommand(111, "stream", map[string]any{"audio": 1}); err == nil {
			rt.mu.Lock()
			if sentVideo || wantsVideo {
				rt.streamMode = "audio+video"
			} else {
				rt.streamMode = "audio"
			}
			rt.mu.Unlock()
		}
	}
}

func (rt *CameraRuntime) sendGetParams() error {
	rt.mu.RLock()
	pp := rt.pppp
	rt.mu.RUnlock()
	if pp == nil {
		return errors.New("not connected")
	}
	return pp.SendCommand(101, "get_parms", nil)
}

func (rt *CameraRuntime) command(cmd int, pro string, args map[string]any) CommandResult {
	rt.mu.RLock()
	pp := rt.pppp
	connected := !rt.connectedAt.IsZero()
	rt.mu.RUnlock()
	if pp == nil || !connected {
		return CommandResult{OK: false, Error: "camera is not connected"}
	}
	if err := pp.SendCommand(cmd, pro, args); err != nil {
		return CommandResult{OK: false, Error: err.Error()}
	}
	return CommandResult{OK: true}
}

func (rt *CameraRuntime) SetWifi(ssid, password string) CommandResult {
	if strings.TrimSpace(ssid) == "" || password == "" {
		return CommandResult{OK: false, Error: "ssid and password are required"}
	}
	return rt.command(114, "set_wifi", map[string]any{
		"wifissid":    ssid,
		"encwifissid": ssid,
		"wifipwd":     password,
		"encwifipwd":  password,
		"encryption":  1,
	})
}

func (rt *CameraRuntime) ScanWifi() CommandResult {
	return rt.command(113, "scan_wifi", nil)
}

func (rt *CameraRuntime) RefreshParams() CommandResult {
	return rt.command(101, "get_parms", nil)
}

func (rt *CameraRuntime) Reboot() CommandResult {
	return rt.command(102, "dev_control", map[string]any{"reboot": 1})
}

func (rt *CameraRuntime) Status(base string) map[string]any {
	rt.mu.Lock()
	now := time.Now()
	rt.videoMetric = trimMetric(rt.videoMetric, now)
	rt.audioMetric = trimMetric(rt.audioMetric, now)
	videoMetric := calcMetric(rt.videoMetric)
	audioMetric := calcMetric(rt.audioMetric)
	state, label := rt.healthLocked()
	videoDemand := len(rt.videoClients) > 0 || now.Sub(rt.lastSnapshotDemand) < 10*time.Second
	audioDemand := len(rt.audioClients) > 0
	videoFresh := !rt.lastVideoAt.IsZero() && now.Sub(rt.lastVideoAt) <= videoStaleWindow
	audioFresh := !rt.lastAudioAt.IsZero() && now.Sub(rt.lastAudioAt) <= audioStaleWindow
	st := map[string]any{
		"id":                    rt.cfg.ID,
		"name":                  rt.cfg.name(),
		"enabled":               rt.cfg.enabled(),
		"ip":                    rt.cfg.IP,
		"discovery":             rt.cfg.Discovery,
		"expectedAddress":       expectedAddress(rt.cfg),
		"connected":             state == "online",
		"transportConnected":    !rt.connectedAt.IsZero(),
		"healthState":           state,
		"healthLabel":           label,
		"connectedAt":           formatTime(rt.connectedAt),
		"peerAddress":           emptyNil(rt.peerAddress),
		"peerPort":              emptyIntNil(rt.peerPort),
		"lastTrafficAt":         formatTime(rt.lastTraffic),
		"lastVideoAt":           formatTime(rt.lastVideoAt),
		"lastAudioAt":           formatTime(rt.lastAudioAt),
		"lastTrafficAgeMs":      ageMs(rt.lastTraffic),
		"lastVideoAgeMs":        ageMs(rt.lastVideoAt),
		"lastAudioAgeMs":        ageMs(rt.lastAudioAt),
		"videoFrames":           atomic.LoadUint64(&rt.videoFrames),
		"audioFrames":           atomic.LoadUint64(&rt.audioFrames),
		"videoFps":              videoMetric.Rate,
		"audioPacketsPerSecond": audioMetric.Rate,
		"videoKbps":             videoMetric.Kbps,
		"videoMode":             rt.cfg.videoMode(),
		"audioKbps":             audioMetric.Kbps,
		"videoDemand":           videoDemand,
		"audioDemand":           audioDemand,
		"videoFresh":            videoFresh,
		"audioFresh":            audioFresh,
		"lastFrameBytes":        rt.lastFrameBytes,
		"streamMode":            rt.streamMode,
		"avStream":              rt.cfg.avStream(),
		"videoClients":          len(rt.videoClients),
		"audioClients":          len(rt.audioClients),
		"restartCount":          rt.restartCount,
		"lastError":             emptyNil(rt.lastError),
		"lastCommand":           emptyNil(rt.lastCommand),
		"urls": map[string]string{
			"page":     fmt.Sprintf("%s/cam/%s", base, rt.cfg.ID),
			"video":    fmt.Sprintf("%s/cam/%s/video.mjpg", base, rt.cfg.ID),
			"audio":    fmt.Sprintf("%s/cam/%s/audio.wav", base, rt.cfg.ID),
			"audioRaw": fmt.Sprintf("%s/cam/%s/audio.raw", base, rt.cfg.ID),
			"snapshot": fmt.Sprintf("%s/cam/%s/snapshot.jpg", base, rt.cfg.ID),
		},
	}
	rt.mu.Unlock()
	return st
}

func (rt *CameraRuntime) healthLocked() (string, string) {
	if !rt.cfg.enabled() {
		return "disabled", "disabled"
	}
	if rt.pppp == nil || rt.connectedAt.IsZero() {
		if !rt.startedAt.IsZero() && rt.restartCount == 0 && time.Since(rt.startedAt) <= 20*time.Second {
			return "connecting", "connecting"
		}
		if !rt.lastVideoAt.IsZero() && time.Since(rt.lastVideoAt) <= staleTrafficWindow {
			return "stale", "reconnecting"
		}
		return "offline", "offline"
	}
	if rt.lastTraffic.IsZero() {
		return "connecting", "connecting"
	}
	now := time.Now()
	trafficAge := now.Sub(rt.lastTraffic)
	if trafficAge > staleTrafficWindow {
		return "offline", "offline"
	}

	videoDemand := len(rt.videoClients) > 0 || now.Sub(rt.lastSnapshotDemand) < 10*time.Second
	videoFresh := false
	if videoDemand {
		if rt.lastVideoAt.IsZero() {
			if now.Sub(rt.connectedAt) <= mediaRestartWindow {
				return "connecting", "waiting for video"
			}
			return "stale", "video stale"
		}
		if videoAge := now.Sub(rt.lastVideoAt); videoAge > videoStaleWindow {
			if videoAge > staleTrafficWindow {
				return "offline", "video offline"
			}
			return "stale", "video stale"
		}
		videoFresh = true
	}

	audioDemand := len(rt.audioClients) > 0
	if audioDemand {
		if rt.lastAudioAt.IsZero() {
			if videoFresh {
				return "online", "audio stale"
			}
			if now.Sub(rt.connectedAt) <= mediaRestartWindow {
				return "connecting", "waiting for audio"
			}
			return "stale", "audio stale"
		}
		if audioAge := now.Sub(rt.lastAudioAt); audioAge > audioStaleWindow {
			if videoFresh {
				return "online", "audio stale"
			}
			if audioAge > staleTrafficWindow {
				return "offline", "audio offline"
			}
			return "stale", "audio stale"
		}
	}

	if trafficAge <= onlineTrafficWindow {
		return "online", "online"
	}
	if trafficAge <= staleTrafficWindow {
		return "stale", "stale"
	}
	return "offline", "offline"
}

func (rt *CameraRuntime) ServeVideo(w http.ResponseWriter, r *http.Request) {
	rt.Start()
	w.Header().Set("content-type", "multipart/x-mixed-replace; boundary=frame")
	w.Header().Set("cache-control", "no-store, no-cache, must-revalidate, private")
	w.Header().Set("pragma", "no-cache")
	w.Header().Set("connection", "close")
	w.Header().Set("x-accel-buffering", "no")
	flusher, _ := w.(http.Flusher)
	client := &Client{ch: make(chan []byte, 8)}
	rt.mu.Lock()
	rt.videoClients[client] = struct{}{}
	if len(rt.latestFrame) > 0 && !rt.lastVideoAt.IsZero() && time.Since(rt.lastVideoAt) <= videoStaleWindow {
		sendDropOld(client.ch, append([]byte(nil), rt.latestFrame...))
	}
	rt.mu.Unlock()
	defer rt.removeVideoClient(client)
	rt.requestStreams(false)
	idle := time.NewTimer(videoClientIdle)
	defer idle.Stop()
	hold := time.NewTicker(videoHoldInterval)
	defer hold.Stop()
	var lastSent []byte
	var lastRealFrameAt time.Time
	var lastWriteAt time.Time
	for {
		select {
		case <-r.Context().Done():
			return
		case <-idle.C:
			return
		case frame := <-client.ch:
			now := time.Now()
			lastSent = append(lastSent[:0], frame...)
			lastRealFrameAt = now
			lastWriteAt = now
			resetTimer(idle, videoClientIdle)
			if err := writeMJPEGFrame(w, frame); err != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		case <-hold.C:
			now := time.Now()
			if len(lastSent) == 0 || now.Sub(lastRealFrameAt) > videoHoldWindow || now.Sub(lastWriteAt) < videoHoldInterval {
				continue
			}
			lastWriteAt = now
			resetTimer(idle, videoClientIdle)
			if err := writeMJPEGFrame(w, lastSent); err != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
	}
}

func writeMJPEGFrame(w io.Writer, frame []byte) error {
	if _, err := fmt.Fprintf(w, "--frame\r\nContent-Type: image/jpeg\r\nContent-Length: %d\r\n\r\n", len(frame)); err != nil {
		return err
	}
	if _, err := w.Write(frame); err != nil {
		return err
	}
	if _, err := w.Write([]byte("\r\n")); err != nil {
		return err
	}
	return nil
}

func (rt *CameraRuntime) ServeAudio(w http.ResponseWriter, r *http.Request, wav bool) {
	rt.Start()
	if wav {
		w.Header().Set("content-type", "audio/wav")
	} else {
		w.Header().Set("content-type", "application/octet-stream")
	}
	w.Header().Set("cache-control", "no-store, no-cache, must-revalidate, private")
	w.Header().Set("connection", "close")
	w.Header().Set("x-accel-buffering", "no")
	w.Header().Set("x-audio-format", "pcm_s16le")
	w.Header().Set("x-audio-sample-rate", strconv.Itoa(audioSampleRate))
	w.Header().Set("x-audio-channels", strconv.Itoa(audioChannels))
	if wav {
		_, _ = w.Write(wavHeader())
	}
	flusher, _ := w.(http.Flusher)
	client := &Client{ch: make(chan []byte, 64)}
	rt.mu.Lock()
	rt.audioClients[client] = struct{}{}
	rt.mu.Unlock()
	defer rt.removeAudioClient(client)
	rt.requestStreams(false)
	idle := time.NewTimer(audioClientIdle)
	defer idle.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-idle.C:
			return
		case pcm := <-client.ch:
			resetTimer(idle, audioClientIdle)
			if _, err := w.Write(pcm); err != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
	}
}

func (rt *CameraRuntime) ServeSnapshot(w http.ResponseWriter, r *http.Request) {
	rt.Start()
	rt.mu.Lock()
	rt.lastSnapshotDemand = time.Now()
	frame := append([]byte(nil), rt.latestFrame...)
	fresh := !rt.lastVideoAt.IsZero() && time.Since(rt.lastVideoAt) <= videoStaleWindow
	state, _ := rt.healthLocked()
	rt.mu.Unlock()
	rt.requestStreams(false)
	if len(frame) == 0 || !fresh || state == "offline" || state == "disabled" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "snapshot not ready", "camera": rt.cfg.ID, "healthState": state})
		return
	}
	w.Header().Set("content-type", "image/jpeg")
	w.Header().Set("cache-control", "no-store")
	w.Header().Set("content-length", strconv.Itoa(len(frame)))
	_, _ = w.Write(frame)
}

func (rt *CameraRuntime) removeVideoClient(c *Client) {
	rt.mu.Lock()
	delete(rt.videoClients, c)
	rt.mu.Unlock()
}

func (rt *CameraRuntime) removeAudioClient(c *Client) {
	rt.mu.Lock()
	delete(rt.audioClients, c)
	rt.mu.Unlock()
}

func NewPPPP(cfg CameraConfig) *PPPP {
	ctx, cancel := context.WithCancel(context.Background())
	return &PPPP{
		cfg:        cfg,
		key:        keyFromPSK(cfg.psk()),
		ctx:        ctx,
		cancel:     cancel,
		verbose:    cfg.Verbose,
		expectedIP: expectedAddress(cfg),
	}
}

func (p *PPPP) Run() {
	addr := &net.UDPAddr{IP: net.IPv4zero, Port: 0}
	if p.cfg.LocalAddress != "" {
		addr.IP = net.ParseIP(p.cfg.LocalAddress)
	}
	conn, err := net.ListenUDP("udp4", addr)
	if err != nil {
		if p.OnClose != nil {
			p.OnClose("udp listen: " + err.Error())
		}
		return
	}
	p.conn = conn
	defer conn.Close()
	go p.discoveryLoop()
	buf := make([]byte, 2048)
	for {
		_ = conn.SetReadDeadline(time.Now().Add(time.Second))
		n, remote, err := conn.ReadFromUDP(buf)
		if err != nil {
			if p.ctx.Err() != nil {
				return
			}
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			if p.OnClose != nil {
				p.OnClose("udp read: " + err.Error())
			}
			return
		}
		msg := append([]byte(nil), buf[:n]...)
		if !p.acceptsPeer(remote) {
			continue
		}
		plain := decrypt(msg, p.key)
		pkt, ok := parsePacket(plain)
		if !ok {
			continue
		}
		p.handlePacket(pkt, msg, remote)
	}
}

func (p *PPPP) discoveryLoop() {
	dst := p.cfg.discovery()
	addr, err := net.ResolveUDPAddr("udp4", net.JoinHostPort(dst, "32108"))
	if err != nil {
		if p.OnClose != nil {
			p.OnClose("resolve discovery: " + err.Error())
		}
		return
	}
	msg := encrypt([]byte{mcam, 0x30, 0, 0}, p.key)
	t := time.NewTicker(500 * time.Millisecond)
	defer t.Stop()
	for {
		p.mu.RLock()
		connected := p.isConnected
		p.mu.RUnlock()
		if connected {
			return
		}
		_, _ = p.conn.WriteToUDP(msg, addr)
		select {
		case <-p.ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (p *PPPP) Close() {
	if p.closed.CompareAndSwap(false, true) {
		_ = p.sendEnc([]byte{mcam, msgClose, 0, 0})
		_ = p.sendEnc([]byte{mcam, msgClose, 0, 0})
		_ = p.sendEnc([]byte{mcam, msgClose, 0, 0})
		p.cancel()
		if p.conn != nil {
			_ = p.conn.Close()
		}
	}
}

func (p *PPPP) acceptsPeer(addr *net.UDPAddr) bool {
	p.mu.RLock()
	remote := p.remote
	expected := p.expectedIP
	p.mu.RUnlock()
	if remote != nil {
		return addr.IP.Equal(remote.IP) && addr.Port == remote.Port
	}
	return expected == "" || addr.IP.String() == expected
}

func (p *PPPP) setRemote(addr *net.UDPAddr) {
	p.mu.Lock()
	p.remote = &net.UDPAddr{IP: append(net.IP(nil), addr.IP...), Port: addr.Port}
	p.isConnected = true
	p.mu.Unlock()
}

func (p *PPPP) handlePacket(pkt Packet, encrypted []byte, addr *net.UDPAddr) {
	if p.OnPacket != nil {
		p.OnPacket(pkt, addr)
	}
	switch pkt.Type {
	case msgPunch:
		p.mu.Lock()
		if p.punchCount < 5 {
			p.punchCount++
			_, _ = p.conn.WriteToUDP(encrypted, addr)
		}
		p.mu.Unlock()
	case msgP2PRdy:
		already := p.connected()
		p.setRemote(addr)
		if !already && p.OnConnected != nil {
			go p.OnConnected(addr)
		}
	case msgAlive:
		_ = p.sendEnc([]byte{mcam, msgAliveAck, 0, 0})
	case msgClose:
		_ = p.sendEnc([]byte{mcam, msgAlive, 0, 0})
		_ = p.sendEnc([]byte{mcam, msgAlive, 0, 0})
		_ = p.sendEnc([]byte{mcam, msgAlive, 0, 0})
	case msgDRW:
		p.sendDRWAck(pkt.Channel, pkt.Index)
		p.handleDRW(pkt)
	}
}

func (p *PPPP) connected() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.isConnected
}

func (p *PPPP) handleDRW(pkt Packet) {
	switch pkt.Channel {
	case 0:
		if bytes.HasPrefix(pkt.Data, []byte{0x06, 0x0a}) && len(pkt.Data) >= 8 {
			if p.OnCommand != nil {
				p.OnCommand(string(pkt.Data[8:]))
			}
		}
	case 1:
		if frame := p.handleVideo(pkt); len(frame) > 0 && p.OnVideo != nil {
			p.OnVideo(frame, pkt.Index)
		}
	case 2:
		raw := pkt.Data
		if bytes.HasPrefix(raw, []byte{0x55, 0xaa, 0x15, 0xa8, 0xaa, 0x01}) {
			if len(raw) < 0x20 {
				return
			}
			raw = raw[0x20:]
		}
		pcm := p.audio.Decode(raw)
		if len(pcm) > 0 && p.OnAudio != nil {
			p.OnAudio(pcm, pkt.Index)
		}
	}
}

func (p *PPPP) handleVideo(pkt Packet) []byte {
	const maxFrame = 512 * 1024
	marker := []byte{0x55, 0xaa, 0x15, 0xa8, 0x03, 0x00}
	data := pkt.Data
	isStart := bytes.HasPrefix(data, marker)
	p.videoMu.Lock()
	defer p.videoMu.Unlock()
	var previous []byte
	if isStart {
		if p.videoFrame != nil {
			previous = buildVideoFrame(p.videoFrame, true)
		}
		if len(data) < 0x20 {
			p.videoFrame = nil
			if len(previous) > 0 {
				return previous
			}
			return nil
		}
		expected := int(binary.LittleEndian.Uint32(data[16:20]))
		if expected <= 0 || expected > maxFrame {
			p.videoFrame = nil
			if len(previous) > 0 {
				return previous
			}
			return nil
		}
		data = data[0x20:]
		p.videoFrame = &videoFrame{
			startIndex:     pkt.Index,
			expectedLength: expected,
			chunks:         map[int][]byte{},
		}
	}
	if p.videoFrame == nil {
		if len(previous) > 0 {
			return previous
		}
		return nil
	}
	vf := p.videoFrame
	slot := indexDistance(vf.startIndex, pkt.Index)
	if slot > 128 {
		previous = buildVideoFrame(vf, true)
		p.videoFrame = nil
		if len(previous) > 0 {
			return previous
		}
		return nil
	}
	if _, ok := vf.chunks[slot]; !ok {
		vf.chunks[slot] = append([]byte(nil), data...)
		vf.receivedLength += len(data)
		if slot > vf.maxSlot {
			vf.maxSlot = slot
		}
	}
	if len(previous) > 0 {
		return previous
	}
	out := buildVideoFrame(vf, false)
	if len(out) == 0 {
		return nil
	}
	p.videoFrame = nil
	return out
}

func buildVideoFrame(vf *videoFrame, allowBoundaryComplete bool) []byte {
	var out []byte
	total := 0
	for i := 0; i <= vf.maxSlot; i++ {
		chunk, ok := vf.chunks[i]
		if !ok {
			return nil
		}
		out = append(out, chunk...)
		total += len(chunk)
		if total >= vf.expectedLength {
			break
		}
	}
	if len(out) >= vf.expectedLength {
		out = out[:vf.expectedLength]
		if len(out) > 0 {
			return out
		}
	}
	if !allowBoundaryComplete {
		return nil
	}
	return trimCompleteJPEG(out)
}

func trimCompleteJPEG(in []byte) []byte {
	soi := bytes.Index(in, []byte{0xff, 0xd8})
	if soi < 0 {
		return nil
	}
	eoi := -1
	for i := soi + 2; i+1 < len(in); i++ {
		if in[i] == 0xff && in[i+1] == 0xd9 {
			eoi = i + 2
		}
	}
	if eoi <= soi {
		return nil
	}
	return append([]byte(nil), in[soi:eoi]...)
}

func (p *PPPP) SendCommand(cmd int, pro string, args map[string]any) error {
	payload := map[string]any{
		"pro":    pro,
		"cmd":    cmd,
		"user":   p.cfg.username(),
		"pwd":    p.cfg.password(),
		"devmac": "0000",
	}
	for k, v := range args {
		payload[k] = v
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	buf := make([]byte, 8+len(b))
	copy(buf[:4], []byte{0x06, 0x0a, 0xa0, 0x80})
	binary.LittleEndian.PutUint32(buf[4:8], uint32(len(b)))
	copy(buf[8:], b)
	return p.sendDRWPacket(0, buf)
}

func (p *PPPP) SendAlive() error {
	return p.sendEnc([]byte{mcam, msgAlive, 0, 0})
}

func (p *PPPP) sendDRWPacket(channel byte, data []byte) error {
	idx := uint16(atomic.AddUint32(&p.outIndex, 1) - 1)
	buf := make([]byte, 8+len(data))
	buf[0] = mcam
	buf[1] = msgDRW
	binary.BigEndian.PutUint16(buf[2:4], uint16(len(data)+4))
	buf[4] = mdrw
	buf[5] = channel
	binary.BigEndian.PutUint16(buf[6:8], idx)
	copy(buf[8:], data)
	return p.sendEnc(buf)
}

func (p *PPPP) sendDRWAck(channel byte, index uint16) {
	buf := []byte{mcam, msgDRWAck, 0, 6, mdrw, channel, 0, 1, byte(index >> 8), byte(index)}
	for i := 0; i < p.cfg.ackRepeats(); i++ {
		_ = p.sendEnc(buf)
	}
}

func (p *PPPP) sendEnc(plain []byte) error {
	p.mu.RLock()
	remote := p.remote
	p.mu.RUnlock()
	if remote == nil || p.conn == nil {
		return errors.New("not connected")
	}
	encrypted := encrypt(plain, p.key)
	_, err := p.conn.WriteToUDP(encrypted, remote)
	return err
}

func parsePacket(b []byte) (Packet, bool) {
	if len(b) < 4 || b[0] != mcam {
		return Packet{}, false
	}
	p := Packet{Type: b[1], Size: int(binary.BigEndian.Uint16(b[2:4]))}
	if len(b) >= 8 {
		p.Channel = b[5]
		p.Index = binary.BigEndian.Uint16(b[6:8])
		p.Data = b[8:]
	}
	return p, true
}

func keyFromPSK(psk string) [4]byte {
	var key [4]byte
	for _, ch := range []byte(psk) {
		key[0] = byte((int(key[0]) + int(ch)) & 0xff)
		key[1] = byte((int(key[1]) - int(ch)) & 0xff)
		key[2] = byte((int(key[2]) + int(ch)/3) & 0xff)
		key[3] ^= ch
	}
	return key
}

func encrypt(buf []byte, key [4]byte) []byte {
	out := make([]byte, len(buf))
	prev := byte(0)
	for i, b := range buf {
		idx := byte(int(key[prev&3]) + int(prev))
		v := b ^ keyTable[idx]
		out[i] = v
		prev = v
	}
	return out
}

func decrypt(buf []byte, key [4]byte) []byte {
	out := make([]byte, len(buf))
	prev := byte(0)
	for i, b := range buf {
		idx := byte(int(key[prev&3]) + int(prev))
		out[i] = b ^ keyTable[idx]
		prev = b
	}
	return out
}

func (d *ADPCMDecoder) Decode(in []byte) []byte {
	out := make([]byte, 0, len(in)*4)
	step := stepTable[d.index]
	for _, input := range in {
		for nib := 0; nib < 2; nib++ {
			var delta int
			if nib == 0 {
				delta = int((input >> 4) & 0x0f)
			} else {
				delta = int(input & 0x0f)
			}
			d.index += indexTable[delta]
			if d.index < 0 {
				d.index = 0
			}
			if d.index > 88 {
				d.index = 88
			}
			sign := delta & 8
			delta &= 7
			vpdiff := step >> 3
			if delta&4 != 0 {
				vpdiff += step
			}
			if delta&2 != 0 {
				vpdiff += step >> 1
			}
			if delta&1 != 0 {
				vpdiff += step >> 2
			}
			if sign != 0 {
				d.valPred -= vpdiff
			} else {
				d.valPred += vpdiff
			}
			if d.valPred > 32767 {
				d.valPred = 32767
			}
			if d.valPred < -32768 {
				d.valPred = -32768
			}
			step = stepTable[d.index]
			out = binary.LittleEndian.AppendUint16(out, uint16(int16(d.valPred)))
		}
	}
	return out
}

func appendMetric(points []metricPoint, at time.Time, bytes int) []metricPoint {
	points = append(points, metricPoint{at: at, bytes: bytes})
	return trimMetric(points, at)
}

func trimMetric(points []metricPoint, now time.Time) []metricPoint {
	cut := now.Add(-metricWindow)
	i := 0
	for i < len(points) && points[i].at.Before(cut) {
		i++
	}
	if i > 0 {
		copy(points, points[i:])
		points = points[:len(points)-i]
	}
	return points
}

func calcMetric(points []metricPoint) metricResult {
	if len(points) < 2 {
		return metricResult{}
	}
	duration := points[len(points)-1].at.Sub(points[0].at).Seconds()
	if duration <= 0 {
		return metricResult{}
	}
	total := 0
	for _, p := range points {
		total += p.bytes
	}
	return metricResult{
		Rate: round2(float64(len(points)) / duration),
		Kbps: round1(float64(total*8) / duration / 1000),
	}
}

func keys(m map[*Client]struct{}) []*Client {
	out := make([]*Client, 0, len(m))
	for c := range m {
		out = append(out, c)
	}
	return out
}

func sendDropOld(ch chan []byte, data []byte) {
	frame := append([]byte(nil), data...)
	select {
	case ch <- frame:
		return
	default:
	}
	select {
	case <-ch:
	default:
	}
	select {
	case ch <- frame:
	default:
	}
}

func resetTimer(t *time.Timer, d time.Duration) {
	if !t.Stop() {
		select {
		case <-t.C:
		default:
		}
	}
	t.Reset(d)
}

func wavHeader() []byte {
	h := make([]byte, 44)
	copy(h[0:4], "RIFF")
	binary.LittleEndian.PutUint32(h[4:8], 0xffffffff)
	copy(h[8:12], "WAVE")
	copy(h[12:16], "fmt ")
	binary.LittleEndian.PutUint32(h[16:20], 16)
	binary.LittleEndian.PutUint16(h[20:22], 1)
	binary.LittleEndian.PutUint16(h[22:24], audioChannels)
	binary.LittleEndian.PutUint32(h[24:28], audioSampleRate)
	binary.LittleEndian.PutUint32(h[28:32], audioSampleRate*audioChannels*audioBytesPerSample)
	binary.LittleEndian.PutUint16(h[32:34], audioChannels*audioBytesPerSample)
	binary.LittleEndian.PutUint16(h[34:36], 16)
	copy(h[36:40], "data")
	binary.LittleEndian.PutUint32(h[40:44], 0xffffffff)
	return h
}

func isUnicastIPv4(value string) bool {
	ip := net.ParseIP(value)
	if ip == nil {
		return false
	}
	v4 := ip.To4()
	if v4 == nil {
		return false
	}
	return v4[0] != 0 && v4[0] < 224 && v4[3] != 0 && v4[3] != 255 && !(v4[0] == 255 && v4[1] == 255 && v4[2] == 255 && v4[3] == 255)
}

func indexDistance(start, current uint16) int {
	return int((uint32(current) - uint32(start) + 65536) % 65536)
}

func formatTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func ageMs(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return time.Since(t).Milliseconds()
}

func emptyNil(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func emptyIntNil(i int) any {
	if i == 0 {
		return nil
	}
	return i
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" && v != "<nil>" {
			return v
		}
	}
	return ""
}

func mapBool(ok bool, a, b string) string {
	if ok {
		return a
	}
	return b
}

func round2(v float64) float64 {
	return float64(int(v*100+0.5)) / 100
}

func round1(v float64) float64 {
	return float64(int(v*10+0.5)) / 10
}

func yamlQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}
