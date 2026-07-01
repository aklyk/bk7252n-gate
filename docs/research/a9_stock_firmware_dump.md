# A9/BK7252N Stock Firmware Notes

Sanitized notes from the local A9/BK7252N camera dump. The dump itself,
camera passwords, Wi-Fi credentials, DID, app keys and raw UART logs are not
tracked in this repository.

## Image

- Firmware family: `CYCam-A9` / `CYCAM_T99_v32_0226`.
- Build string: `Feb 26 2026`.
- RTOS: RT-Thread 3.1.0.
- Raw flash dump uses Beken CRC interleave: 32 data bytes followed by 2 CRC
  bytes. The useful logical image is the de-CRC'd image.
- The 4 MiB raw read contained two identical 2 MiB halves.

## Flash Layout

From UART/FAL:

| Partition | Offset | Size | Notes |
| --- | ---: | ---: | --- |
| `bootloader` | `0x00000000` | `0x00010000` | Beken on-chip CRC |
| `app` | `0x00010000` | `0x00120000` | Beken on-chip CRC |
| `download` | `0x00143000` | `0x000bb000` | OTA/download area |
| `EasyFlash` | `0x001fd000` | `0x00001000` | env/config |
| `rfparam` | `0x001fe000` | `0x00001000` | RF calibration |
| `netparam` | `0x001ff000` | `0x00001000` | network/MAC params |

## JSON Command Dispatch

The stock PPPP command parser dispatches primarily by JSON `pro` string. The
numeric `cmd` is echoed in responses and is useful as a correlation id. Known
legacy command numbers are still accepted for old commands, but the firmware
handlers themselves compare the `pro` string.

| `pro` | Handler | Useful fields | UI decision |
| --- | ---: | --- | --- |
| `check_user` | `0x19e28` | `user`, `pwd`, returns auth/check fields | internal |
| `get_parms` | `0x1a014` | `time`, `stream`, `icut`, `power`, `charging`, `sysver`, `server_ver`, `upgrade`, `rotmir`, `lamp`, `bright`, `contrast`, `rate_bit`, `sensor` | safe read |
| `edit_user` | `0x1a37c` | `edituser`, `newpwd`, user list | advanced only |
| `get_wifi` | `0x1a58c` | returns `ssid`, `conmode` | safe read |
| `scan_wifi` | `0x1a720` | returns up to 20 networks: `ssid`, `signal`, `encryption` | setup wizard |
| `set_wifi` | `0x1a9f8` | `wifissid`, `wifipwd` | setup wizard |
| `stream` | `0x1abe4` | `video`, `audio` | safe, but rate-sensitive |
| `get_sd` | `0x1ad88` | `total`, `free`, `sdstatu`, `recMode` | read only |
| `set_sd` | `0x1bea4` | `format`, `recMode` | dangerous/hidden |
| `get_record_day` | `0x1afcc` | `year`, `month[]` | later |
| `get_record_hour` | `0x1b1a8` | `ymd`, `record_hour` | later |
| `get_record_min` | `0x1b358` | `ymdh`, `date` | later |
| `get_record_list` | `0x1b518` | `ymd`, `date`, record list | later |
| `play_record_file` | `0x1b6dc` | `filename`, `type`, `audio` | later |
| `del_record_file` | `0x1c210` | `recordList`, `record_num` | dangerous/hidden |
| `dev_control` | `0x1b93c` | image/system controls below | curated UI only |
| `check_ota` | `0x1cc3c` | `otaType`, `bin_path`, `cloud_version` | do not expose |
| `set_datetime` | `0x1c060` | `tz`, `time` | safe if needed |
| `talk_send` | `0x1c484` | `isSend` | later |
| `get_cyalarm` | `0x1c828` | alarm state below | safe read |
| `set_cyalarm` | `0x1c60c` | alarm state below | advanced |
| `set_cypush` | `0x1c9c0` | push/cloud upload below | safe cloud-hardening |
| `snap_shot` | `0x1ce90` | no useful payload seen | existing stream snapshot is better |
| `set_sysparms` | `0x1ceb0` | system params below | advanced |
| `get_sysparms` | `0x1d0c0` | system params below | safe read |
| `get_cloudsupport` | `0x1d3a0` | `flashOrTf`, `uploadType`, `isExistTf` | safe read |
| `set_cloudinfo` | `0x1d514` | `creatTime`, `days`, `buyType` | do not expose |
| `get_cloudinfo` | `0x1d6f8` | `creatTime`, `days`, `buyType` | read only if debugging |
| `del_cloudinfo` | `0x1d884` | clears `user_cloud_info` | hidden |
| `ptz_control` | `0x1d9b8` | `parms`, `value` | only for PTZ variants |
| `set_whiteLight` | `0x1db60` | `status` | advanced/light variants |
| `get_whiteLight` | `0x1dcf4` | `status` | safe read |
| `set_sound_light_alarm` | `0x1de3c` | `status` | advanced |
| `get_sound_light_alarm` | `0x1dfc4` | `status` | safe read |

## Confirmed Parameters

### Wi-Fi

`set_wifi` uses these request fields:

| Field | Effect | Confidence |
| --- | --- | --- |
| `wifissid` | writes EasyFlash key `ssid0` | high |
| `wifipwd` | writes EasyFlash key `passwd0` | high |

The handler also writes `workmode=sta`, saves EasyFlash and calls the station
connect path. UART is not required for provisioning once the camera is reachable
on its AP/LAN Wi-Fi.

### Stream

| Field | Values | Effect | Confidence |
| --- | --- | --- | --- |
| `video` | `0` | stop/clear video stream flags | high |
| `video` | `1` | enable video, call video-param helper with mode `1` | high |
| `video` | `2` | enable video, call video-param helper with mode `2` | high |
| `audio` | `0`/`1` | stop/start audio stream | high |

The mode helper distinguishes `video=1` and `video=2`. Existing clients treat
them as VGA/QVGA respectively; live frame dimensions should still be verified
per camera.

### Image And Quality

Via `dev_control`:

| Field | Env/helper | Values | Notes |
| --- | --- | --- | --- |
| `rate_bit` | `bitrate_size` | positive integer | Stored as JPEG bitrate/quality bucket. Current defaults use `26`. |
| `bright` | `brightness` | `0..6` | Mapped to sensor offsets `-3..+3`. Default `4`. |
| `contrast` | `contrast` | non-negative byte | Calls sensor contrast helper. Default `2`. |
| `resetrb` | reset helper | `1` | Resets brightness to `4`, contrast to `2`, bitrate to `26`. |
| `rotmir` | sensor rotate/mirror | integer | Use with caution. |
| `icut` | IR-cut helper | integer | Use with caution. |
| `lamp` | light helper | integer | Behavior depends on `workmode`; not a generic safe button. |
| `anti_flicker` | sensor flicker helper | `0..2` | Advanced. |

### Alarms

`set_cyalarm/get_cyalarm`:

| Field | Env/helper | Values | Notes |
| --- | --- | --- | --- |
| `motionDetect` | motion detect state | `0`/`1` | May add camera CPU work. |
| `motionDelay` | motion delay | integer | Seconds-like value. |
| `audioDetect` | audio detect state | `0`/`1` | May add camera CPU work. |
| `audioDelay` | audio delay | integer | Seconds-like value. |

### System Params

`set_sysparms/get_sysparms`:

| JSON field | Env key | Semantics | Notes |
| --- | --- | --- | --- |
| `sleep_time` | `sleep_time` | stored value, getter returns seconds-like value multiplied by 10; default fallback `600` | advanced |
| `offline_time` | `offline_time` | stored value, getter returns value multiplied by 10; default fallback `100` | advanced |
| `limit_push` | `def_push_count` / `push_count` | daily/period push quota counter, not a hard cloud kill-switch | advanced |
| `environment` | `language_environ` | language/environment setting | advanced; rename in UI |

The setters ignore non-positive values for these fields.

### Second-Pass Findings

The dump was re-scanned after the initial command map for missed network,
provisioning and diagnostic paths.

Additional useful firmware evidence:

| Area | Evidence | Practical value |
| --- | --- | --- |
| Wi-Fi diagnostics | UART shell strings for `wifi status`, `wifi rssi`, `ifconfig`, `dns`, `list_tcps`, `list_udps`, `netstat` | Useful manual diagnostics when UART is available; not needed in daily UI. |
| Video diagnostics | `video_buffer open/read/close`, `read frame full`, `read frame timeout`, `video_transfer_init`, `sensor ppi:[%d] fps:[%d]` | Confirms that stalled frames can be diagnosed below the PPPP layer. |
| Audio diagnostics | `audio_dump`, `audio_device_mic_open`, sample-rate/channel/volume logs | Useful for debugging audio, not a user-facing control yet. |
| BLE provisioning | `ble_netconfig`, `ble ssid:%s, passwd:%s, did:%s`, `ble_send_wifi_connected_to_master_info` | Potential future provisioning path. Not implemented in the gateway. |
| AirKiss provisioning | `ie.airkiss`, `airkiss is not finish yet`, monitor-mode guard strings | Potential future provisioning path. Not implemented in the gateway. |
| TF-card provisioning | `TFCard router ssid:%s, passwd:%s` | Variant-specific/manual path; not useful for the no-SD A9 setup. |
| Power save | DTIM/deep-sleep strings and `jsonAppCmdForceDeepSleep` | Keep hidden; can break availability if exposed casually. |

Live UART on 2026-06-30 opened as `/dev/cu.usbserial-0001` at 115200 8N1 but
produced no passive data and did not answer shell commands without a power
cycle. The next UART diagnostic session should capture boot output after power
cycling, then run only read-only commands first: `version`, `list_device`,
`ifconfig`, `wifi status`, `wifi rssi`, `dns`, `list_tcps`, `list_udps`,
`video_buffer`, `free`, `ps`.

### Cloud, Push And P2P

There are three separate layers:

| Layer | Firmware evidence | What to do |
| --- | --- | --- |
| P2P/PPCS transport | `PPCS_Initialize`, `PPCS_NetworkDetectByServer`, `dev_p2p_did`, `dev_p2p_appkey`, `p2p_account_info` | Do not delete or overwrite identifiers from UI. LAN PPPP may rely on this stack. |
| Cloud subscription metadata | `user_cloud_info`, `set_cloudinfo`, `get_cloudinfo`, `del_cloudinfo` | Not useful for local streaming; hide. |
| Push/upload service | `set_cypush`, `push_ip`, `push_port`, `push_user`, `push_passwd`, `push_pic`, `push_video`, `/push/login`, `/push/send`, `/system/oss/uploadFile` | Safe first target for local/cloud-hardening mode. |

`set_cypush` confirmed fields:

| Field | Env key | Safe local value |
| --- | --- | --- |
| `pushIp` | `push_ip` | leave unchanged unless debugging |
| `pushPort` | `push_port` | leave unchanged unless debugging |
| `cyAdmin` | `push_user` | leave unchanged |
| `cyPwd` | `push_passwd` | leave unchanged |
| `isPushPic` | `push_pic` | `0` |
| `isPushVideo` | `push_video` | `0` |

The video/event paths check `push_pic` and `push_video` before HTTP upload.
Setting both to `0` should disable camera-originated push photo/video upload
without damaging LAN streaming. Changing `push_ip` to an invalid host is riskier:
it may cause retry/timeouts instead of saving CPU.

For actual privacy isolation, also block camera egress to WAN on the router and
allow only LAN traffic to the gateway. Local PPPP LAN streaming should continue
when the gateway and cameras are on the same LAN. NTP is optional; the firmware
contains China NTP defaults.

Plaintext external firmware targets found:

| Target | Source | Recommendation |
| --- | --- | --- |
| `120.79.76.240:9093` | hardcoded `http://120.79.76.240:9093/push/login` | block |
| `120.76.44.223:9093` | current EasyFlash `push_ip`/`push_port` on the test camera | block |
| `cn.ntp.org.cn` | NTP fallback | block or redirect to router NTP |
| `cn.pool.ntp.org` | NTP fallback | block or redirect to router NTP |
| `ntp1.aliyun.com` | NTP fallback | block or redirect to router NTP |
| `114.114.114.114` | appears only as a CLI DNS example | optional defensive block |

`ilnk.work` was checked explicitly and was not present in the de-CRC image or
raw flash dumps. Treat it as an app/PPCS ecosystem domain, not a confirmed
firmware string for this camera. It is still fine to block defensively at DNS or
by denying all camera WAN egress.

Older iLnk/cam-reverse APK notes also list P2P hello IPs `139.155.68.77`,
`119.45.114.92`, `162.62.63.154` and `3.132.215.40`; those are not plaintext
targets found in this firmware dump.

Private/local addresses and examples were also present (`192.168.10.1`,
`192.168.169.100`, `192.168.10.135`, `192.168.0.1`, `192.168.1.30`,
`192.168.1.1`) but are not Chinese cloud targets.

## Streaming Stability Root Cause

The firmware contains active PPCS buffer-full branches:

- `PPCS_Check_Buffer() close p2pstream[...]`
- `ERROR_PPCS_REMOTE_SITE_BUFFER_FULL`
- `aPPCS_Write REMOTE_SITE_BUFFER_FULL`
- `vPPCS_Write REMOTE_SITE_BUFFER_FULL`

These are referenced by audio/video playback write paths, not dead strings.
That matches observed stalls: with two weak A9 Wi-Fi links, VGA MJPEG and audio,
the internal PPCS/camera buffers can fill. Router quality matters, but the
camera/PPCS stack itself is also a bottleneck.

Practical mitigations:

- Prefer `video=2` / 320x240 for multi-camera dashboard and Frigate restreams.
- Keep one PPPP session per camera and avoid duplicate consumers directly
  hitting the camera.
- Keep `audio=1` only where needed; audio can sometimes make the firmware keep
  AV state more consistently, but it still consumes bandwidth.
- Disable push photo/video upload with `set_cypush`.
- Use fixed DHCP leases and strong 2.4 GHz RSSI.
- Block WAN egress for cameras at the router instead of trying to erase P2P
  credentials in flash.

## Implementation Guidance

The gateway UI should expose:

- local camera config: id, IP/discovery, width/height, AV request;
- setup wizard using `set_wifi` only, no UART references;
- camera readback: `get_parms`, `get_sysparms`, `get_cyalarm`,
  `get_cloudsupport`, `get_wifi`;
- camera settings: stream mode, audio, bitrate, brightness, contrast, alarms;
- cloud hardening: send `set_cypush` with `isPushPic=0` and `isPushVideo=0`,
  then explain router WAN blocking;
- RU/EN UI switch, default Russian.

The UI should hide or clearly mark as dangerous:

- `reset`, `reboot`, `upgrade`, `check_ota`;
- SD format and record deletion;
- cloudinfo mutation/deletion;
- direct P2P identifier changes.

## Test Plan For New Parameters

The new controls should be judged by measured benefit, not by assumption.

| Test | Change | Metric | Expected benefit |
| --- | --- | --- | --- |
| QVGA vs VGA | `stream.video=2` vs `stream.video=1` | gateway FPS, kbps, stale/offline transitions, restart count over 5-10 minutes | QVGA should reduce PPCS buffer pressure and improve multi-camera stability. |
| Audio on/off | `stream.audio=1` vs `0` | video FPS/stalls plus audio packet rate | Audio may keep AV state active, but increases bandwidth; keep only if net positive. |
| Bitrate buckets | `rate_bit` around `12`, `18`, `26`, `40` | JPEG size, kbps, frame drops | Lower values should reduce stalls; visual quality tradeoff must be visible. |
| Push hardening | `set_cypush isPushPic=0,isPushVideo=0` | router WAN counters, camera stability | Should remove cloud upload attempts; may not visibly change FPS unless events were triggering upload. |
| Detection off | `motionDetect=0`, `audioDetect=0` | FPS/stalls while scene/audio is active | May save CPU if detection was enabled. |
| Sleep/offline | leave unchanged unless testing power modes | uptime and reconnect behavior | Risky; no default optimization yet. |

### Local Test Results On 2026-06-30

Test context: one `a9_test` camera was powered and reachable on LAN; `doorcam`
was intentionally powered off/disconnected by the user.

Observed results:

- After a power cycle, PPPP transport connected but media often entered
  `video stale` after a short burst.
- Sending `stream.video=2`, `dev_control rate_bit=12` and
  `set_cypush isPushPic=0,isPushVideo=0` returned `result:0` once the camera was
  in a clean session and restored a usable stream.
- With that light profile, a 60 second status run held `online` without session
  restarts at roughly `8.5..10.2` FPS and about `0.9..1.1` Mbps.
- A direct 45 second MJPEG client received 401 complete JPEG frames.
- `device-refresh` readback commands are not safe as continuous polling on this
  firmware. In the unstable state they timed out; after a good stream they still
  caused a media stall and backend restart. Keep readback as diagnostics only.
- Audio was not proven to improve stability. During the test, active audio
  clients produced `audio stale` states and added pressure when no audio frames
  arrived.
- A backend media-nudge sequence (`stream.video=0`, then QVGA start) can create
  recovery bursts but did not replace full session restart in all cases.

UI/UX rule for these tests:

- daily dashboard shows previews, health, FPS/kbps and clear open-live/audio
  actions;
- local profile stays separate from firmware settings;
- firmware settings start with stream/audio/quality and hide alarms/power/cloud
  in advanced sections;
- readback results are shown as raw JSON for debugging, but dangerous write
  commands stay hidden;
- the setup wizard remains step-by-step and local-only, with no UART wording.

### Buffer-Stall Recheck On 2026-07-01

Runtime logs from the Go backend showed repeated `a9_test` sequences:

- `media nudge: video stale`;
- `restarting: video timeout` or `restarting: client requested stale video`;
- a new PPPP peer port after reconnect.

During those incidents the backend process stayed alive and the camera often
continued responding to PPPP reconnects. This points at media starvation inside
the camera/PPCS path rather than a web server crash. Occasional
`invalid JPEG format` overlay errors mean that some received frames were already
damaged or truncated before overlay rendering.

UART was checked again on `/dev/cu.usbserial-0001` at 115200 8N1. Passive
capture during an MJPEG request produced no bytes, and read-only shell probes
(`version`, `video_buffer`, `free`, `ps`) also produced no response. For live
`video_buffer` counters, capture boot output after a power cycle first.

Backend-side mitigations added after this recheck:

- healthy stream refresh was relaxed from 6 seconds to 30 seconds so the
  rate-sensitive `stream video=<mode>` handler is not poked constantly while
  frames are flowing;
- client-triggered recovery now prefers media nudge inside the short stale
  window and leaves full PPPP restart to the longer media timeout;
- audio-stale recovery no longer runs the video media-nudge path while video is
  active, because the old path sent `stream video=0` before restarting video and
  could create the very stall it was trying to heal;
- invalid JPEG frames are dropped before they reach Safari/VLC/Frigate;
- overlay/output fanout renders one outgoing frame and shares it across HTTP
  clients, avoiding repeated JPEG decode/encode work per Safari/VLC/Frigate
  consumer;
- live MJPEG and snapshots can reuse the last valid frame during a short
  reconnect, making a stall look like a temporary freeze instead of a broken
  response.

Remaining likely pressure sources, in order of practical impact:

- VGA MJPEG around 1.1-1.3 Mbps on the weak A9/PPCS stack;
- multiple browser/VLC/Frigate consumers making the gateway maintain active
  output clients;
- audio clients when no useful audio frames arrive; with the fixed gateway this
  should not reset video, but it still wastes camera/PPCS bandwidth;
- camera-originated push/upload work when events fire;
- weak 2.4 GHz RSSI/router airtime jitter.
