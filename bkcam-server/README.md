# BKCam server

Локальный dashboard и stream-export для A9/BK7252N камер со стоковой прошивкой PPPP/PPCS.

## Что даёт

- Web UI для Safari/Chrome: `http://<server-ip>:8088/`
- Компактные snapshot-превью на главной странице, без множества долгих MJPEG-соединений.
- Пошаговый wizard отдельно: `/setup`.
- Live MJPEG на странице конкретной камеры: `/cam/<id>`.
- Offline setup wizard по Wi-Fi/PPPP для режима, когда ноутбук подключен к AP камеры и интернета нет.
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
  "discovery": "192.168.1.203",
  "psk": "SHIX",
  "username": "admin",
  "password": "6666",
  "width": 640,
  "height": 480,
  "avStream": true,
  "enabled": true
}
```

Для нескольких камер добавь элементы в `cameras`. Лучше закрепить IP камер через DHCP lease и ставить `discovery` равным конкретному IP камеры, а не `192.168.1.255`. Так PPPP-сессия будет привязана к ожидаемому peer и не поймает соседнюю камеру с тем же PSK.

`id` должен состоять из латиницы, цифр, `_` или `-`. Это имя используется в URL и в именах stream для Frigate/go2rtc.

`ackRepeats` регулирует количество повторов PPPP ACK на каждый DRW packet. Значение `3` сейчас дефолт: оно заметно мягче старого агрессивного ACK flood, но устойчивее на слабом Wi-Fi, чем `1..2`. Если сеть чистая, можно снизить; если есть потери, поднять до `4..5`.

`avStream` по умолчанию включён. В этом режиме сервер запрашивает у камеры AV stream даже для video-only просмотра: звук не проигрывается без клика пользователя, но у этих камер такой запрос часто держит видеопоток бодрее и стабильнее.

## API

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

Пароль камеры не возвращается через API; наружу отдаётся только `hasPassword`.

`/api/setup/provision` делает первичную настройку без UART: временно подключается к камере по её текущему Wi-Fi/AP адресу, отправляет `set_wifi`, затем сохраняет финальные `ip/discovery` в `config.json`.

Пример добавления:

```bash
curl -X POST http://127.0.0.1:8088/api/cameras \
  -H 'content-type: application/json' \
  --data '{"id":"a9_front","name":"Front","ip":"192.168.1.203","discovery":"192.168.1.255","psk":"SHIX","username":"admin","password":"6666","enabled":true}'
```

Пример отправки Wi-Fi настроек в камеру:

```bash
curl -X POST http://127.0.0.1:8088/api/cameras/a9_front/wifi \
  -H 'content-type: application/json' \
  --data '{"ssid":"<SSID>","password":"<PASSWORD>","reboot":true}'
```

Пример первичной настройки через wizard API:

```bash
curl -X POST http://127.0.0.1:8088/api/setup/provision \
  -H 'content-type: application/json' \
  --data '{"id":"doorcam","name":"Doorcam","setupIp":"192.168.4.1","setupDiscovery":"255.255.255.255","ssid":"<SSID>","wifiPassword":"<PASSWORD>","finalIp":"192.168.1.162","finalDiscovery":"192.168.1.162","psk":"SHIX","username":"admin","password":"6666","reboot":true}'
```

## Safari/audio

Видео в UI идет обычным MJPEG через `<img>`, поэтому открывается напрямую в Safari без MSE/WebRTC.

Звук в UI запускается кнопкой `Audio`: Safari требует пользовательский жест перед воспроизведением. После клика страница читает `/cam/<id>/audio.raw` как `pcm_s16le`, mono, 8000 Hz, и проигрывает через Web Audio. `/cam/<id>/audio.wav` оставлен как совместимый endpoint для `ffprobe`, VLC и внешних клиентов.

## Health status

`connected` в `/api/status` теперь означает, что камера недавно присылала трафик, а не просто когда-то прошла PPPP handshake. Для диагностики есть:

- `healthState`: `online`, `stale`, `offline`, `connecting`, `disabled`
- `transportConnected`: низкоуровневая PPPP-сессия была установлена
- `expectedAddress`: IP, к которому должна привязаться PPPP-сессия
- `peerAddress` / `peerPort`: фактический UDP peer текущей сессии
- `lastTrafficAt` / `lastTrafficAgeMs`: свежесть реальных пакетов от камеры

Если камера выключена или перестала слать пакеты, она больше не должна оставаться визуально online.

## Frigate

Открой:

```text
http://<server-ip>:8088/frigate.yml
```

И перенеси блоки `ffmpeg`, `go2rtc` и `cameras` в конфиг Frigate. Сервис отдаёт MJPEG и WAV/PCM, go2rtc/ffmpeg превращает это в один RTSP stream с H.264 видео и AAC audio:

```yaml
ffmpeg:
  output_args:
    record: preset-record-generic-audio-aac

go2rtc:
  streams:
    a9_test:
      - 'ffmpeg:http://192.168.1.179:8088/cam/a9_test/video.mjpg#video=h264'
      - 'ffmpeg:http://192.168.1.179:8088/cam/a9_test/audio.wav#audio=aac#audio=opus'

cameras:
  a9_test:
    ffmpeg:
      inputs:
        - path: 'rtsp://127.0.0.1:8554/a9_test?video=h264&audio=aac'
          input_args: preset-rtsp-restream
          roles:
            - detect
            - record
    detect:
      width: 640
      height: 480
```

Для Frigate audio events можно дополнительно добавить role `audio` и включить `audio.enabled`, но для live/recordings это не обязательно: audio track уже есть в RTSP stream. Сырые endpoints остаются доступны для проверки:

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
