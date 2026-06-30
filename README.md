# BK7252N Gate

Local gateway for cheap BK7252N/A9-style PPPP cameras.

![BKCam dashboard](docs/assets/bkcam-dashboard.png)

The main service keeps a local PPPP session to each camera and exposes:

- Safari/Chrome dashboard
- compact snapshot previews on the overview page
- separate offline setup wizard at `/setup`
- MJPEG video
- WAV/raw PCM audio
- snapshots
- status JSON
- local setup wizard for camera AP / no-internet provisioning
- go2rtc/Frigate config snippets

This is intended for cameras you own and operate on your LAN. It does not require reflashing the camera.

## Quick Start

```bash
cd bkcam-go
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

Daily use starts on `/`: it shows lightweight camera previews and the main actions. The new-camera wizard is deliberately kept on `/setup`, so it does not get in the way during normal monitoring.

Useful endpoints:

```text
/api/status
/setup
/cam/<id>/video.mjpg
/cam/<id>/audio.wav
/cam/<id>/audio.raw
/cam/<id>/snapshot.jpg
/frigate.yml
/go2rtc.yml
```

Camera management API:

```text
POST   /api/setup/provision
GET    /api/cameras
POST   /api/cameras
GET    /api/cameras/<id>
PATCH  /api/cameras/<id>
DELETE /api/cameras/<id>
POST   /api/cameras/<id>/wifi
POST   /api/cameras/<id>/restart
POST   /api/cameras/<id>/reboot
POST   /api/cameras/<id>/params
```

For DID prefix `EEE`, the local PPPP PSK is usually `SHIX`.

## Repository Layout

- `bkcam-go/` - main native Go gateway and dashboard
- `bkcam-server/` - legacy Node.js gateway kept as a reference during the Go migration
- `PPPP/` - patched JavaScript PPPP client based on A9_PPPP
- `a9serv/` - small C MJPEG/WAV fallback proxy
- `pppp-dissector/` - Wireshark dissector and PPPP notes
- `aiopppp/` - Python PPPP reference implementation
- `docs/` - project handoff and reverse-engineering notes

Generated archives, firmware dumps, local configs and captures are intentionally not tracked.

## Frigate

Open:

```text
http://<server-ip>:8088/frigate.yml
```

The generated snippet uses go2rtc/ffmpeg to restream MJPEG as H.264 and the WAV/PCM endpoint as AAC/Opus audio, so Frigate can consume one RTSP stream with video and sound.

## Camera Setup

If a camera is already on Wi-Fi, open `/setup` or add it from the API. If a new camera exposes its own AP, connect this computer to that AP, open `/setup`, enter the camera's current AP address/discovery, target Wi-Fi credentials and the final LAN address if you already know it. The wizard sends `set_wifi` over the camera's PPPP Wi-Fi session and saves the matching local camera config. No internet is required.

For multiple cameras, prefer fixed DHCP leases and unicast `discovery` values equal to each camera IP. The PPPP client pins the session to the expected peer so one camera cannot silently occupy another camera card. With `avStream` enabled, the Go backend keeps the camera in audio+video stream mode even if only the MJPEG endpoint is open; this has proven more stable on weak A9 Wi-Fi links and keeps Frigate/go2rtc audio available. If Wi-Fi latency climbs under multiple MJPEG streams, set a camera to `width: 320` and `height: 240`; the backend will request the camera's lower-bandwidth `video:2` mode.

UART is not part of the wizard. It is only a manual development/recovery fallback:

```text
setenv workmode sta
setenv ssid0 <SSID>
setenv passwd0 <PASSWORD>
saveenv
reboot
```

Those settings are stored in the camera flash and survive reboots.

## Third-Party Code

This repo contains modified/reference code from multiple upstream projects. See `NOTICE.md` and per-folder license/readme files before redistributing derived work.
