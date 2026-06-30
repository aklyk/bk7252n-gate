# BK7252N A9 camera handoff for Codex / friend

Дата состояния: 2026-06-30.

Цель: дешёвые A9-камеры на Beken BK7252N отдают локальный видео- и аудиопоток без китайского облака. Рабочий путь на сейчас: не прошивать камеру, а подключить стоковую прошивку к Wi-Fi и забрать PPPP/PPCS-поток локальным клиентом.

## Что уже получилось

- Камера подключена к Wi-Fi и доступна по LAN.
- Стоковая прошивка не перепрошивалась.
- Видео перехвачено локально через UDP/PPPP и отдано как MJPEG HTTP.
- Аудио перехвачено из PPPP audio channel, декодировано из ADPCM в `pcm_s16le`, mono, 8000 Hz.
- Рабочий production-oriented сервер: `bkcam-server/`.
- Проверенная web-страница для Safari: `http://192.168.1.179:8088/`.
- Проверенные endpoints:
  - `http://192.168.1.179:8088/cam/a9_test/video.mjpg`
  - `http://192.168.1.179:8088/cam/a9_test/audio.wav`
  - `http://192.168.1.179:8088/cam/a9_test/audio.raw`
  - `http://192.168.1.179:8088/cam/a9_test/snapshot.jpg`
- Проверенный кадр: JPEG/JFIF 640x480, реальная картинка с камеры.

## Текущая камера

- Чип: Beken BK7252N.
- Прошивка: `CYCAM_T99_v32_0226`, RT-Thread shell по UART.
- UART: 115200 8N1.
- IP камеры в сети: `192.168.1.203`.
- IP ноутбука/сервера на момент теста: `192.168.1.179`.
- MAC Wi-Fi STA: `00:00:00:15:06:04`.
- UDP PPPP listener на камере: `32108`.
- Логин камеры: `admin`.
- Пароль камеры: `<redacted>`.
- DID: `<redacted>`.
- apiLicense из env: `<redacted>`.
- crckey / local PPPP PSK: `SHIX`.

Важно: пароль от ноутбука/пользователя в этот бандл не включён и не нужен.

## Почему обычные клиенты не увидели камеру

`cam-reverse`, `aiopppp` и оригинальный `a9serv` по умолчанию шлют encrypted LAN discovery:

```text
2c ba 5f 5d
```

Это `MSG_LAN_SEARCH`, зашифрованный ключом `camera` (под префиксы вроде DGOK/PTZA).

У этой камеры DID начинается с `EEE`, а для `EEE` нужен PSK `SHIX`. Правильный encrypted `MSG_LAN_SEARCH`:

```text
9f a1 ee b9
```

После отправки `9f a1 ee b9` камера отвечает `MSG_PUNCH`:

```text
decoded: f1 41 ... <redacted uid bytes>
```

Это даёт UID/DID:

```text
<redacted DID>
```

## BKCam server

Основной сервер лежит в:

```text
bkcam-server/
```

Что делает:

- держит один PPPP runtime на камеру;
- раздаёт один поток нескольким HTTP-клиентам без повторного подключения к камере на каждую вкладку;
- даёт dashboard для Safari/Chrome;
- раздаёт MJPEG video, WAV audio, raw PCM audio и snapshot;
- генерирует snippets для Frigate/go2rtc: `/frigate.yml`, `/go2rtc.yml`;
- поддерживает несколько камер через массив `cameras` в `config.json`.

Запуск:

```bash
cd bkcam-server
npm start
```

Открыть:

```text
http://192.168.1.179:8088/
```

Проверка:

```bash
curl -m 8 http://192.168.1.179:8088/cam/a9_test/video.mjpg -o /tmp/video.mjpg
curl -m 8 http://192.168.1.179:8088/cam/a9_test/audio.wav -o /tmp/audio.wav
curl -m 8 http://192.168.1.179:8088/cam/a9_test/audio.raw -o /tmp/audio.raw
ffprobe /tmp/video.mjpg
ffprobe /tmp/audio.wav
curl http://192.168.1.179:8088/api/status
```

На текущей проверке:

- video: `mjpeg`, 640x480;
- audio WAV: `pcm_s16le`, mono, 8000 Hz;
- за 8 секунд пришло 18 JPEG-кадров и около 145 KB PCM/WAV аудио;
- после закрытия клиентов счётчики `videoClients/audioClients` вернулись в `0/0`.

Safari audio: кнопка `Audio` запускает Web Audio после пользовательского клика и читает `/cam/<id>/audio.raw`. Это нужно из-за autoplay policy Safari.

Frigate/go2rtc:

```text
http://192.168.1.179:8088/frigate.yml
http://192.168.1.179:8088/go2rtc.yml
```

Сейчас Frigate export рассчитан на видео через go2rtc/ffmpeg. Аудио доступно отдельно как WAV/raw PCM.

## Патченный a9serv

`a9serv` оставлен как низкоуровневый fallback и минимальный пример PPPP/MJPEG. Для нормальной эксплуатации лучше использовать `bkcam-server`.

Рабочий клиент лежит в:

```text
a9serv/
```

Изменения относительно upstream `hyc/a9serv`:

1. `a9serv/cipher.c`

Было: hash PSK `camera`.

Стало: hash PSK `SHIX`.

```c
static const unsigned char key[] = {0x3c, 0xc4, 0x68, 0x0a};
```

2. `a9serv/pppp.c`

Было: encrypted `LanSearch` для PSK `camera`.

Стало: encrypted `LanSearch` для PSK `SHIX`.

```c
static const char probe[] = {0x9f, 0xa1, 0xee, 0xb9};
```

## Сборка

На macOS/Linux:

```bash
cd a9serv
gcc -O2 -Wall -o pppp pppp.c
```

Предупреждения компилятора у upstream-кода есть, но бинарь рабочий.

## Запуск одной камеры

Пример с текущей сетью:

```bash
cd a9serv
./pppp -b 192.168.1.255 -l 192.168.1.179 -p 3001
```

Открыть:

```text
http://192.168.1.179:3001/
http://192.168.1.179:3001/v.mjpg
```

Для фонового запуска на macOS удобно:

```bash
screen -dmS bk7252n_a9serv bash -lc 'cd /path/to/a9serv && ./pppp -b 192.168.1.255 -l <server-ip> -p 3001'
```

Остановить:

```bash
screen -S bk7252n_a9serv -X quit
```

Проверить:

```bash
screen -ls
lsof -nP -iTCP:3001
curl -m 6 http://<server-ip>:3001/v.mjpg -o /tmp/a9-test.mjpg
```

## Несколько камер

Для нескольких камер использовать `bkcam-server/config.json`:

```json
{
  "cameras": [
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
  ]
}
```

Добавить ещё камеры можно вторым/третьим объектом в `cameras`. `id` должен состоять из латиницы, цифр, `_` или `-`.

Лучше закрепить DHCP lease на роутере для каждой камеры, чтобы IP не менялся.

`a9serv` лучше считать one-camera-per-process fallback:

```bash
./pppp -b 192.168.1.203 -l <server-ip> -p 3001
./pppp -b 192.168.1.204 -l <server-ip> -p 3002
```

## Нужно ли вскрывать каждую камеру

Для получения потока вскрытие не нужно, если камера уже подключена к вашей Wi-Fi сети.

Для первичной настройки без китайского приложения самый надёжный известный путь сейчас - UART. То есть новую камеру, которую надо посадить на Wi-Fi без облака, обычно придётся открыть и подключить UART хотя бы один раз.

Если удастся настроить Wi-Fi штатным приложением или через AP provisioning без облака, UART для этой камеры не нужен.

## UART и настройка Wi-Fi

UART:

```text
115200 8N1
GND общий
TX/RX крест-накрест
питание штатно через micro-USB
не питать камеру от 3V3 CP2102
```

Проверенные команды RT-Thread shell:

```text
help
printenv
getvalue <key>
setenv <key> <value>
saveenv
reboot
ifconfig
netstat
set_log off
```

Настройка Wi-Fi в стоковой прошивке:

```text
setenv workmode sta
setenv ssid0 <SSID>
setenv passwd0 <PASSWORD>
saveenv
reboot
```

После `saveenv` настройки хранятся в EasyFlash и переживают reboot/отключение питания.

На проверенной камере уже сохранено значение конкретной локальной сети. В публичном handoff оно намеренно обезличено:

```text
workmode=sta
ssid0=<SSID>
passwd0=<stored on camera>
```

Не нужно заново писать пароль, если камера уже получает IP.

Проверка после reboot:

```text
ifconfig
netstat
getvalue dev_p2p_did
getvalue p2p_account_info
```

Ожидаемо:

```text
w0 LINK_UP
ip address: 192.168.1.xxx
UDP 0.0.0.0:32108
```

## Аудио

В `bkcam-server` звук уже работает:

- PPPP channel 2;
- исходный формат камеры: ADPCM;
- сервер декодирует в `pcm_s16le`;
- параметры: mono, 8000 Hz, 16-bit;
- endpoints:
  - `/cam/<id>/audio.wav`
  - `/cam/<id>/audio.raw`

В `a9serv` тоже есть реализация `/a.wav`, но для web UI и нескольких камер лучше использовать `bkcam-server`.

## Что не делать без необходимости

- Не шить прошивку в камеру ради текущей задачи.
- Не трогать bootloader/rfparam/netparam.
- Не писать во flash, если цель только получить поток.
- Не использовать стандартный `2c ba 5f 5d` probe для DID `EEE`, он не подходит.

## Полезные файлы в бандле

- `codex.md` - этот handoff.
- `bkcam-server/` - основной web/server для Safari, audio, multi-camera и Frigate/go2rtc snippets.
- `bk7252n-handoff/` - исходный RE/handoff, документы, де-интерлейснутый firmware dump и черновой порт прошивки.
- `a9serv/` - низкоуровневый MJPEG/WAV proxy, уже пропатчен под `EEE` / `SHIX`.
- `pppp-dissector/PPPP.md` - справка по PPPP, включая таблицу PSK по DID-префиксам.
- `PPPP/` - JS-реализация A9_PPPP, патчена под PSK `SHIX`, username/password options, audio и более надёжную сборку JPEG по длине кадра.
- `aiopppp/` и `cam-reverse/` - родственные клиенты, сейчас не являются основным рабочим путём для этой камеры без патча под `SHIX`.

## Быстрый чек-лист для друга

1. Подключить камеру к Wi-Fi и узнать её IP.
2. Проверить `ping <camera-ip>`.
3. Если DID начинается с `EEE`, использовать PSK `SHIX`.
4. Настроить `bkcam-server/config.json`.
5. Запустить:

```bash
cd bkcam-server
npm start
```

6. Открыть:

```text
http://<server-ip>:8088/
```

7. Для Frigate взять:

```text
http://<server-ip>:8088/frigate.yml
```

Fallback через `a9serv`:

```bash
cd a9serv
gcc -O2 -Wall -o pppp pppp.c
./pppp -b <camera-ip-or-broadcast> -l <server-ip> -p 3001
```

Открыть:

```text
http://<server-ip>:3001/v.mjpg
```
