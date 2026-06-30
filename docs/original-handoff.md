# BK7252N A9-камера — полный контекст и состояние работы (handoff)

Цель: дешёвые мини-камеры «A9» на чипе **Beken BK7252N** должны отдавать видео **в локалку, без
китайского облака**. Изначально — через свою прошивку; по ходу нашёлся и безопасный путь без прошивки.
Этот файл — чтобы любой (человек или Claude Code) мог продолжить с нуля контекста.

## TL;DR — где мы

Две дороги к цели, обе доведены далеко:

**A. Своя прошивка (opencam-порт).** Реверсом полностью разобрана камерная подсистема BK7252N,
собрана рабочая тулчейн-сборка, наш app-слой (MJPEG+WiFi) компилируется. НО: чистая загрузка на
BK7252N требует вендорского **Beken SDK 3.0.76** (на нём работает сток) — публично его нет; а надёжного
способа прошить/восстановить (bootrom) для этого чипа у нас нет (риск кирпича). Поэтому прошивку НЕ лили.

**B. Без прошивки — забрать сток-поток локально (cam-reverse).** Сток отдаёт видео по протоколу
**PPCS/PPPP** (UDP 32108). Открытый клиент **cam-reverse** умеет подключаться к таким камерам по LAN
без облака и отдавать JPEG в браузер. Собран, ключи камеры вытащены. Упёрлись ТОЛЬКО в сеть:
камеру не свести с ПК в одну локалку (см. «Блокер»).

## Железо (установлено по факту)
- Чип **BK7252NQN481** (QFN48), сенсор **GC0311** (VGA). bootrom `BK7252N_1.0.14`.
- Прошивка стока: `CYCAM_T99_v32_0226`, **RT-Thread 3.1.0**, **Beken SDK Rev 3.0.76**. SD НЕТ (конфиг в EasyFlash).
- Флеш 2 МБ. Разделы (FAL): bootloader `0x0` | **app `0x10000`** (1152K) | download `0x143000` |
  EasyFlash `0x1FD000` | rfparam `0x1FE000` (RF-калибровка) | netparam `0x1FF000` (MAC). НЕ трогать boot/rfparam/netparam.
- MAC `<redacted>`. P2P DID `<redacted>`.

## Живой доступ к камере (как управлять)
- UART-переходник CP210x, **COM3**, 115200 8N1. Распайка: P11=TX чипа, P10=RX, GND=корпус USB; питание micro-USB.
- Из WSL читать порт через `powershell.exe ... System.IO.Ports.SerialPort 'COM3',115200`. RT-Thread shell `msh />` доступен.
- Полезные команды стока: `fal probe/read/write`, `printenv/setenv/saveenv`, `reboot`, `set_log off`,
  `video_buffer open/read/close`, `wifi_demo sta <ssid> <key>`, `ifconfig`, `getvalue <k>`.
- **workmode**: `ap` = софт-AP; `router` = станция к роутеру (AP гаснет). `setenv workmode router; saveenv; reboot`.

## Реверс камеры BK7252N (карта регистров — подтверждена на железе)
Периферия камеры BK7252N в НОВОМ регионе `0xA0xxxx` (на старом BK7252 её нет → старый opencam не работает as-is):
- JPEG-энкодер `0xA03000` (enable `0xA03034` бит `0x10`), DVP/CIS захват `0xA04000`, I2C/SCCB `0x802410`,
  тактирование SCTRL `0x800180`, выходной DMA `0x809000`. Подробно — `docs/RE_findings.md`, `src/bk7252n_cam_regs.h`.
- Дамп шифр-формата: Beken CRC-interleave (32+2); анализировать де-интерлейснутый образ.

## Ключи P2P (для cam-reverse / своего PPCS-клиента)
DID `<redacted>`, crckey `SHIX`, apiLicense `<redacted>`, + PPCS init-строки (`EE...`) — в `docs/p2p_keys.md`.
PPCS-«шифрование» — известный XOR, cam-reverse его умеет. Облако НЕ нужно (PPCS LAN-режим).

## Сборка своей прошивки (РАБОТАЕТ, рецепт)
Ubuntu 24.04 без Docker/python2: `apt install libc6-i386 lib32z1 libc6-dev-i386` + `pip install --user
--break-system-packages scons`. Build-систему portнули на python3 (порт-скрипт `tools/py2to3_porter.py`).
Команда: `cd bdk_rtt && RTT_EXEC_PATH=<tc>/bin scons --beken=bk7251 -j4` → `out/*.bin` + `.rbl`. Детали — `docs/BUILD_NATIVE_WORKING.md`.
⚠️ Это сборка под СТАРЫЙ чип (bk7251). Под BK7252N нужна ветка SoC + платформа из SDK 3.0.76.

## Блокер пути B (cam-reverse): сеть
Нужно свести камеру (только WiFi) и ПК с cam-reverse в одну локалку. На этом ПК **нет WiFi-адаптера**
(только Ethernet, `192.168.1.x`). Камера к домашней WiFi `<HOME_WIFI_SSID>` цепляется на **RSSI -84…-86** —
слишком слабо (нужно лучше -65). Варианты разблокировки:
1. **USB-WiFi-свисток в ПК** → вернуть камере AP (`setenv workmode ap; reboot`) → ПК на AP камеры (рядом, сильно)
   → `node cam-reverse/dist/bin.cjs http_server` → видео на `http://localhost:5000/`. Самый надёжный.
2. **Камеру вплотную к роутеру** (1–2 м) → станция встанет → cam-reverse по локалке.
3. **Хотспот телефона** + WiFi-устройство (ноут) с cam-reverse на нём.

## Как запустить cam-reverse (когда сеть решена)
`cd cam-reverse && npm install && npm run build` → `node dist/bin.cjs http_server --config_file cfg.yml`.
В cfg.yml `discovery_ips` указать на сеть камеры (broadcast вида `192.168.x.255` или IP камеры). JPEG на `:5000`.
Если встроенная крипта не подойдёт под этот DID — подставить наши init-строки из `docs/p2p_keys.md`.

## Что в этом бандле
- `README.md` — публичное описание (ру/англ).
- `HANDOFF.md` — этот файл (полный контекст).
- `docs/` — RE-находки, карта регистров, рецепт сборки, ключи P2P, бэкап/прошивка, доступ.
- `src/` — наш порт: драйвер камеры BK7252N (черновик), MJPEG-сервер, WiFi/EasyFlash, точка входа.
- `tools/py2to3_porter.py` — портер build-системы.
- `firmware_deinterleaved.bin` — де-интерлейснутый дамп СТОКА (для реверса). ⚠️ содержит MAC/P2P-данные ЭТОЙ камеры;
  у друга — пусть снимет дамп СВОЕЙ (`fal read` по консоли или bk7231tools, если bootrom поймается).

## Следующий разумный шаг для друга
Самый быстрый результат — **путь B с WiFi-свистком**: поднять cam-reverse, свести камеру и ПК/ноут в одну
сеть, забрать поток. Если хочется именно свою прошивку — искать **Beken SDK 3.0.76** (gitee/Beken BBS) и/или
решать bootrom-прошивку (на запасной камере, не на единственной).
