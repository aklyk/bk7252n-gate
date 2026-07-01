<p align="right"><a href="README.en.md"><kbd>English</kbd></a></p>

# BK7252N Gate

Локальный шлюз для дешевых BK7252N/A9 PPPP-камер. Он держит соединение с камерами в локальной сети и отдает понятный веб-интерфейс, MJPEG-видео, WAV/raw PCM-аудио, snapshot, статус API и готовые конфиги для go2rtc/Frigate.

Прошивать камеру не нужно. UART тоже не нужен для обычной настройки: мастер работает по Wi-Fi через штатный PPPP-протокол камеры.

![Интерфейс BKCam](docs/assets/bkcam-dashboard.png)

<sub>Скриншот показывает демонстрационный вид интерфейса. Реальные превью и FPS зависят от камер, Wi-Fi и выбранного профиля потока.</sub>

## Что Уже Умеет

- Обзор нескольких камер с компактными превью.
- Live-страница камеры, которая открывается прямо в Safari/Chrome.
- Звук через браузер и отдельные WAV/raw PCM endpoints.
- Пошаговый мастер добавления камеры, который работает без интернета, когда ноутбук подключен к AP камеры.
- Запись Wi-Fi настроек в камеру по Wi-Fi, без UART.
- Простые пресеты потока: стабильный 320 и качественный 640.
- Настройки приватности: отключение push фото/видео в камере.
- Оверлеи на картинку: имя камеры, дата/время, FPS/поток.
- Экспорт `/frigate.yml` и `/go2rtc.yml`.
- Статус API для диагностики, рестартов, FPS, bitrate и fresh/stale состояния.

## Быстрый Старт

Нужен Go 1.24 или новее.

```bash
git clone https://github.com/aklyk/bk7252n-gate.git
cd bk7252n-gate
cp bkcam-server/config.example.json bkcam-server/config.json
```

Отредактируйте `bkcam-server/config.json`: укажите IP камер, имена и нужное разрешение. Для камер с DID-префиксом `EEE` локальный PPPP PSK обычно `SHIX`.

Запуск:

```bash
./start.sh
```

Остановка:

```bash
./stop.sh
```

После запуска откройте:

```text
http://<server-ip>:8088/
```

`start.sh` сам соберет Go backend при необходимости. Если установлен `screen`, сервис стартует в `screen`. Если `screen` нет, будет использован `nohup` и pidfile `.bkcam.pid`. `stop.sh` умеет гасить оба режима и дополнительно ищет процесс по порту.

Для разработки можно запускать напрямую:

```bash
cd bkcam-go
go run .
```

## Как Добавить Камеру

Если камера уже подключена к вашей Wi-Fi сети:

1. Откройте веб-интерфейс.
2. Нажмите `Открыть мастер`.
3. Укажите ID, имя, текущий IP камеры и финальный LAN IP.
4. Сохраните профиль.

Если камера новая и поднимает собственную AP:

1. Откройте эту страницу с локального сервера.
2. Подключите ноутбук к Wi-Fi сети камеры.
3. В мастере укажите текущий адрес камеры, вашу домашнюю Wi-Fi сеть и пароль.
4. Мастер отправит `set_wifi` в камеру и сохранит локальный профиль.
5. Верните ноутбук в домашнюю Wi-Fi сеть и откройте главную страницу.

Рекомендуется закрепить DHCP lease для каждой камеры и использовать unicast `discovery`, равный IP камеры. Так одна камера не сможет случайно занять карточку другой камеры.

Удаление камеры находится на странице камеры в секции `Сервис` -> `Опасная зона`. Это удаляет только локальный профиль из BKCam и не сбрасывает настройки, записанные в самой камере.

## Полезные URL

```text
/                    веб-интерфейс
/setup               мастер добавления камеры
/api/status          JSON-статус сервиса
/cam/<id>            страница камеры
/cam/<id>/video.mjpg MJPEG-видео
/cam/<id>/audio.wav  WAV-аудио
/cam/<id>/audio.raw  raw PCM-аудио
/cam/<id>/snapshot.jpg
/frigate.yml         конфиг для Frigate
/go2rtc.yml          конфиг для go2rtc
```

API для удаления профиля:

```text
DELETE /api/cameras/<id>
```

## Frigate И go2rtc

Откройте:

```text
http://<server-ip>:8088/frigate.yml
http://<server-ip>:8088/go2rtc.yml
```

Шлюз отдает MJPEG-видео и PCM/WAV-аудио. go2rtc/ffmpeg могут рестримить это в H.264/AAC/Opus, чтобы Frigate получил один RTSP-поток с видео и звуком.

Для нескольких камер обычно стабильнее начинать с профиля `320x240`. Если Wi-Fi хороший, можно переключать отдельные камеры на `640x480`.

## Приватность И Китайское Облако

Главная рекомендация: заблокировать камерам WAN-доступ на роутере и разрешить им только LAN-доступ к этому шлюзу.

Из дампа прошивки и локальных параметров были найдены такие внешние цели:

```text
120.79.76.240:9093   hardcoded cypush login endpoint
120.76.44.223:9093   push_ip/push_port, замеченный на тестовой камере
cn.ntp.org.cn        firmware NTP fallback
cn.pool.ntp.org      firmware NTP fallback
ntp1.aliyun.com      firmware NTP fallback
114.114.114.114      DNS CLI example, можно блокировать защитно
```

`ilnk.work` не был найден plaintext-строкой в проверенном дампе этой камеры, но его можно блокировать защитно для iLnk/PPCS-клиентов или app-трафика.

Пример OpenWrt-правила для полного запрета WAN с камер:

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

Если нужен только DNS-level block:

```sh
uci add_list dhcp.@dnsmasq[0].address='/cn.ntp.org.cn/0.0.0.0'
uci add_list dhcp.@dnsmasq[0].address='/cn.pool.ntp.org/0.0.0.0'
uci add_list dhcp.@dnsmasq[0].address='/ntp1.aliyun.com/0.0.0.0'
uci add_list dhcp.@dnsmasq[0].address='/ilnk.work/0.0.0.0'
uci commit dhcp
/etc/init.d/dnsmasq restart
```

Подробности по прошивке и найденным параметрам: [`docs/research/a9_stock_firmware_dump.md`](docs/research/a9_stock_firmware_dump.md).

## Структура Репозитория

- `bkcam-go/` - основной Go backend, веб-интерфейс и PPPP gateway.
- `bkcam-server/` - legacy Node.js gateway и локальный config path.
- `PPPP/` - JavaScript PPPP-клиент на базе A9_PPPP.
- `a9serv/` - маленький C MJPEG/WAV fallback proxy.
- `aiopppp/` - Python PPPP reference implementation.
- `pppp-dissector/` - Wireshark dissector и заметки по PPPP.
- `docs/` - handoff, reverse engineering и research notes.

Локальные конфиги, дампы прошивок, capture-файлы и архивы намеренно не хранятся в git.

## Безопасность

Используйте проект только с камерами, которыми вы владеете и управляете в своей сети. Это исследовательский/практический локальный gateway для замены облачной зависимости, а не инструмент для доступа к чужим устройствам.

## Лицензии

В репозитории есть измененный и reference-код из нескольких upstream-проектов. Перед распространением производных работ проверьте `NOTICE.md` и license/readme файлы в отдельных папках.
