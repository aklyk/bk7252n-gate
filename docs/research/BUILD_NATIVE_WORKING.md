# Рабочий рецепт нативной сборки opencam (Ubuntu 24.04, без Docker) ✅

Проверено 2026-06-30: базовая сборка `bk7251` собирается end-to-end и выдаёт флешируемые бинари.
Окружение: WSL2 Ubuntu 24.04 (noble), python3.12, **без python2 и без Docker**.

## 1. Системные зависимости (root, ставится из archive)
```
apt-get install -y libc6-i386 lib32z1 libc6-dev-i386
```
(нужны только чтобы запускался 32-битный бандл-тулчейн GCC 5.4 из репозитория)

## 2. scons (без root)
```
python3 -m pip install --user --break-system-packages scons      # ставит scons 4.10
```

## 3. Порт build-системы python2 → python3 (УЖЕ СДЕЛАН в репозитории)
Скрипт-портер: `py2to3_porter.py` (print→print(), .has_key()→in, except,e→as e, file()→open()).
Что поправлено:
- `bdk_rtt/rtconfig.py` — print'ы.
- `bdk_rtt/rt-thread/tools/building.py` — print/has_key/`file()`→`open()`; **шим модуля `string`**
  (вернул py2-функции string.find/join/split/... — снял правки во всех SConscript); фикс scons-4
  `CPPDEFINES` (deque)→list в `local_group`; в POST_ACTION `python`→`python3` (стр. ~372).
- `bdk_rtt/rt-thread/tools/utils.py`, `mkdist.py` — has_key/file().
- 11 SConscript-ов (`beken378/.../SConscript`) — print'ы.
- `bdk_rtt/tools/scripts/post_action.py` — табы→пробелы (`expand -t 8`) + print.
- shim `python`→`python3`: положен в `gcc-arm-none-eabi-5_4-2016q3/bin/python` (он в scons-PATH).
- `.orig`-копии оригиналов лежат рядом (building.py.orig, post_action.py.orig).

## 4. Команда сборки
```
cd beken7252-opencam/bdk_rtt
TC="$(pwd)/../gcc-arm-none-eabi-5_4-2016q3/bin"
RTT_EXEC_PATH="$TC" ~/.local/bin/scons --beken=bk7251 -j4
```
clean: `~/.local/bin/scons -c`

## 5. Результат (в `bdk_rtt/out/`)
- `rtthread.bin` (738К, сырой app; начинается с ARM-векторов `0e0000ea 14f09fe5...` — валиден),
- `rtthread_ota.rbl` (OTA-пакет), `all_2M.1220.bin` (полный образ), `rtthread_uart_2M.1220.bin`.

## ⚠️ Это БАЗА для старого чипа (bk7251) — НЕ шить на BK7252N!
Доказывает только работоспособность пайплайна. Следующий шаг — интегрировать BK7252N:
ветку SoC, адреса камеры (`bk7252n_cam_regs.h`/`camera_intf_bk7252n.c`), app-слой (mjpeg/net_cfg),
карту памяти (app@0x10000), упаковку под разделы реальной камеры. Потом собрать и шить `app` через `fal write`.
