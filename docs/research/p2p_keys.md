# P2P/PPCS ключи и идентификаторы камеры (из прошивки + конфига)

Протокол: **PPCS** (CS2 Network / "PPPP" / iLnkP2P) — то, что реализует cam-reverse.
Ниже — материал, достаточный, чтобы локальный клиент аутентифицировался и расшифровал поток.

## Идентификаторы устройства (из printenv / EasyFlash)
- DID: `EEE-314151-BKXXY`
- apiLicense: `VYUQCS`
- crckey: `SHIX`
- appkey: `FFD…` (усечён в конфиге)

## PPCS Init-строки (APILicense), вытащены из дампа
Это аргумент PPCS_Initialize — кодирует поддерживаемые префиксы DID и крипто. Для cam-reverse
/ кастомного клиента подставляется как init string:
```
EEGDFHBIKAJMGAJPEIGPFNEBHDNJDLNDGNEHBECHBPILKFKJDLAODKKNGIOFNHKFFDJHOGGCLCJMFKGINKJCMFENNALNAECM
EEGGFCBNKMJCGOJCEHGBFDEPHKMJHHNLGMFKBCCCAEJALKLHDLAOCNOPGJLNJALMALMNLGDCOCMPAOCCJHNJIOAE
EDHNFGBILOINGGIGFDHHFKFIGNNIHDMFHCEEABDGBAIHKPKCCMBLCPOJHBKGJGKABDMJLKCGPFMH
EBGDEJBJKGJLGHJIEJGMFOEGHFNPDINDHDABAEGJAFNJOJLKHLAKDMKNHCLMMILCFIMIPAHEPPICADCN
EIHGFNBBKAIEGEJLELHBFEELGDIHHPJHGLFNFDDKFLIFPIKDGMEGDPKEGDKPMJKMFKNMLAGJODJEBIHJJMIJMBAEJFPKAC
```
(Все начинаются с `EE…`, совпадает с префиксом DID `EEE`.)

## Что это даёт
Крипто/протокол-часть РЕШЕНА: с этими init-строками + DID локальный клиент (cam-reverse или свой
на PPCS) подключается к камере по LAN напрямую, **без облака**, и расшифровывает UDP-видео (порт 32108).
PPCS умеет LAN-режим (broadcast discovery) — облако не нужно, нужна лишь общая локалка с камерой.

## Чего НЕ хватает — сетевой доступ к камере
- Камера к домашней WiFi `<HOME_WIFI_SSID>` встаёт нестабильно: RSSI **-84/-85 dBm** (слабо), ассоциация
  рвётся (`status=18` / отвал на 4-way). Сигнал слишком слабый из текущего положения.
- На ПК **нет WiFi-адаптера**, чтобы подключиться к собственному AP камеры (`192.168.10.1`) напрямую.

## Разблокировка (любой из вариантов) → дальше всё автономно
1. **USB-WiFi-свисток в ПК (~3–5$)** — подключаюсь к AP камеры (рядом, сигнал сильный), запускаю
   cam-reverse с этими init-строками → локальное видео. Самый чистый путь, домашняя WiFi не нужна.
2. **Поднести камеру ближе к роутеру** (или наоборот) — тогда заработает путь через `<HOME_WIFI_SSID>`.
