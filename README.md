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

Firmware command and cloud/push notes are documented in
[`docs/research/a9_stock_firmware_dump.md`](docs/research/a9_stock_firmware_dump.md).

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

## Router Blocking

The safest anti-cloud setup is to block camera WAN egress on the router and allow
only LAN traffic between cameras and this gateway. Firmware plaintext strings
from this camera show these external targets:

```text
120.79.76.240:9093   hardcoded cypush login endpoint
120.76.44.223:9093   current EasyFlash push_ip/push_port value seen on the test camera
cn.ntp.org.cn        firmware NTP fallback
cn.pool.ntp.org      firmware NTP fallback
ntp1.aliyun.com      firmware NTP fallback
114.114.114.114      only appears as a DNS CLI example; block defensively if desired
```

`ilnk.work` was not found in this camera firmware dump (`decrc2m`, raw 2 MiB
and raw 4 MiB were checked). It is still a reasonable defensive DNS block for
iLnk/PPCS clients or app-derived traffic; on 2026-06-30 it resolved to:

```text
ilnk.work A     34.41.139.193
ilnk.work AAAA  2600:1900:4001:96e:8000:1:697:b36
```

Older iLnk/cam-reverse APK research also observed these P2P hello targets, not
confirmed as plaintext targets in this BK7252N firmware:

```text
139.155.68.77
119.45.114.92
162.62.63.154
3.132.215.40
```

The firmware has a PPCS/P2P stack that can resolve servers dynamically, so
blocking only static domains/IPs is weaker than blocking all WAN egress for
camera IPs. Example OpenWrt firewall rule:

```sh
uci add firewall rule
uci set firewall.@rule[-1].name='Block BKCam WAN'
uci set firewall.@rule[-1].src='lan'
uci add_list firewall.@rule[-1].src_ip='192.168.1.162'
uci add_list firewall.@rule[-1].src_ip='192.168.1.203'
uci set firewall.@rule[-1].dest='wan'
uci set firewall.@rule[-1].target='REJECT'
uci commit firewall
/etc/init.d/firewall reload
```

Adjust `src_ip` to your camera DHCP reservations. If you only want DNS-level
blocking for the known Chinese NTP names:

```sh
uci add_list dhcp.@dnsmasq[0].address='/cn.ntp.org.cn/0.0.0.0'
uci add_list dhcp.@dnsmasq[0].address='/cn.pool.ntp.org/0.0.0.0'
uci add_list dhcp.@dnsmasq[0].address='/ntp1.aliyun.com/0.0.0.0'
uci add_list dhcp.@dnsmasq[0].address='/ilnk.work/0.0.0.0'
uci commit dhcp
/etc/init.d/dnsmasq restart
```

## Third-Party Code

This repo contains modified/reference code from multiple upstream projects. See `NOTICE.md` and per-folder license/readme files before redistributing derived work.
