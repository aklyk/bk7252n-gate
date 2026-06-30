# BKCam server

Локальный dashboard и stream-export для A9/BK7252N камер со стоковой прошивкой PPPP/PPCS.

## Что даёт

- Web UI для Safari/Chrome: `http://<server-ip>:8088/`
- MJPEG видео: `/cam/<id>/video.mjpg`
- WAV/PCM аудио: `/cam/<id>/audio.wav`
- Raw PCM аудио для браузерного Web Audio: `/cam/<id>/audio.raw`
- JPEG snapshot: `/cam/<id>/snapshot.jpg`
- JSON status: `/api/status`
- Frigate/go2rtc snippets: `/frigate.yml`, `/go2rtc.yml`
- Несколько камер через `config.json`

## Запуск

```bash
cd bkcam-server
npm start
```

Текущий тестовый адрес:

```text
http://192.168.1.179:8088/
```

## Config

Смотри `config.example.json`.

Камера с DID-префиксом `EEE` использует PSK `SHIX`:

```json
{
  "id": "a9_test",
  "name": "A9 Test",
  "ip": "192.168.1.203",
  "discovery": "192.168.1.255",
  "psk": "SHIX",
  "username": "admin",
  "password": "6666",
  "width": 640,
  "height": 480,
  "enabled": true
}
```

Для нескольких камер добавь элементы в `cameras`. Лучше закрепить IP камер через DHCP lease.

`id` должен состоять из латиницы, цифр, `_` или `-`. Это имя используется в URL и в именах stream для Frigate/go2rtc.

## Safari/audio

Видео в UI идет обычным MJPEG через `<img>`, поэтому открывается напрямую в Safari без MSE/WebRTC.

Звук в UI запускается кнопкой `Audio`: Safari требует пользовательский жест перед воспроизведением. После клика страница читает `/cam/<id>/audio.raw` как `pcm_s16le`, mono, 8000 Hz, и проигрывает через Web Audio. `/cam/<id>/audio.wav` оставлен как совместимый endpoint для `ffprobe`, VLC и внешних клиентов.

## Frigate

Открой:

```text
http://<server-ip>:8088/frigate.yml
```

И перенеси блоки `go2rtc` и `cameras` в конфиг Frigate. Сервис отдаёт MJPEG, go2rtc/ffmpeg превращает его в RTSP/H.264 stream:

```yaml
go2rtc:
  streams:
    a9_test: ffmpeg:http://192.168.1.179:8088/cam/a9_test/video.mjpg#video=h264

cameras:
  a9_test:
    ffmpeg:
      inputs:
        - path: rtsp://127.0.0.1:8554/a9_test
          input_args: preset-rtsp-restream
          roles:
            - detect
            - record
    detect:
      width: 640
      height: 480
```

Frigate/go2rtc export сейчас рассчитан на видео-поток. Аудио доступно отдельно:

```text
http://<server-ip>:8088/cam/a9_test/audio.wav
http://<server-ip>:8088/cam/a9_test/audio.raw
```

## Проверка

```bash
curl -m 6 http://192.168.1.179:8088/cam/a9_test/video.mjpg -o /tmp/video.mjpg
curl -m 6 http://192.168.1.179:8088/cam/a9_test/audio.wav -o /tmp/audio.wav
curl -m 6 http://192.168.1.179:8088/cam/a9_test/audio.raw -o /tmp/audio.raw
ffprobe http://192.168.1.179:8088/cam/a9_test/video.mjpg
ffprobe http://192.168.1.179:8088/cam/a9_test/audio.wav
```

Ожидаемо:

- video: `mjpeg`, 640x480
- audio: `pcm_s16le`, mono, 8000 Hz

## Запуск через screen на macOS

```bash
screen -dmS bkcam_server bash -lc 'cd /Users/klykov/projects/bk7252n-handoff/bkcam-server && npm start'
screen -ls
screen -S bkcam_server -X quit
```
