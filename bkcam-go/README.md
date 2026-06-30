# BKCam Go

Native Go backend for BK7252N/A9 PPPP cameras.

## Run

```bash
cp config.example.json config.json
go run .
```

Or from the repository root:

```bash
make run
```

Open:

```text
http://<server-ip>:8088/
```

If `bkcam-go/config.json` is missing, the server will reuse the legacy local `../bkcam-server/config.json` when it exists. You can always set an explicit config:

```bash
BKCAM_CONFIG=/path/to/config.json go run .
```

## Build

```bash
go build -o bkcam
./bkcam
```

## Features

- Multi-camera PPPP sessions with peer pinning by configured IP.
- Safari-compatible MJPEG video and Web Audio raw PCM playback.
- Offline `/setup` wizard that writes Wi-Fi settings to the camera over PPPP and saves local config.
- Camera CRUD and maintenance API compatible with the earlier Node backend.
- Frigate/go2rtc snippets with video and audio restream sources.
