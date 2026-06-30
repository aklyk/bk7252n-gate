# bk7252n-opencam

Открытая прошивка для дешёвых мини-камер «A9» на чипе **Beken BK7252N** — чтобы они отдавали
видео в локальную сеть напрямую, без китайского облака, без приложения и без SD-карты.

*Open firmware for the cheap "A9" mini Wi-Fi cameras built on the **Beken BK7252N** — so they
stream video straight to your LAN, with no Chinese cloud, no phone app and no SD card. English below.*

---

## По-русски

Купил пачку этих копеечных камер-«таблеток» с AliExpress (те самые «A9 mini», ~3$), и оказалось,
что без их кривого приложения и сервера где-то в Китае они бесполезны. Трафик идёт наружу, что
оно туда шлёт — непонятно. Захотелось ровно одного: чтобы камера отдавала картинку мне в локалку,
и точка.

Внутри — Beken BK7252N (Wi-Fi + BLE, аппаратный JPEG-энкодер и интерфейс камеры). Готового
open-source решения именно под него нет: есть отличный проект [daniel-dona/beken7252-opencam](https://github.com/daniel-dona/beken7252-opencam)
под старый BK7252, и есть OpenBeken, но у того нет драйвера камеры. У BK7252N к тому же **другая
карта периферии** — камера висит в регионе `0xA0xxxx`, которого на старом чипе нет, поэтому старая
прошивка просто так не заводится.

Что уже сделано:

- Сдампил стоковую прошивку прямо через UART-консоль RT-Thread (`fal read`), без перевода в bootrom.
- Разобрал формат флеша (Beken-овский CRC-интерлив 32+2), де-интерлейснул, дизассемблировал.
- **Восстановил карту регистров камеры BK7252N** и проверил её на двух разных прошивках на этом чипе:
  JPEG-энкодер `0xA03000`, захват DVP/CIS `0xA04000`, I2C/SCCB сенсора `0x802410`, тактирование
  `0x800180`, выходной DMA `0x809000`. Подробности — в [docs/RE_findings.md](docs/RE_findings.md).
- Завёл сборку старого SDK (python2 + scons-3) заново на современном Linux (Ubuntu 24.04, python3,
  scons 4) — рецепт в [docs/BUILD_NATIVE_WORKING.md](docs/BUILD_NATIVE_WORKING.md).
- Написал прикладной слой: HTTP-MJPEG-сервер (смотреть в браузере/VLC) + Wi-Fi STA/SoftAP + конфиг
  в EasyFlash вместо облака.

Что ещё в работе: довести битовые поля камерных регистров и получить первую живую картинку на железе.
Это итеративная штука (собрал → прошил → посмотрел лог → поправил), фундамент уже есть.

Сенсор в моих экземплярах — GC0311 (VGA), но автоопределение оставлено на все, что поддерживает сток
(GC0311/GC0312/GC0328, HI704, OV7670/7673, SP0A38).

Если коротко — это рабочий журнал реверса и порта, а не готовый «прошей и работает». Но если у тебя
такие же камеры и надоело облако — тебе сюда.

**Если репо пригодилось или просто хочешь следить за движухой — кинь звезду ⭐. Это правда помогает
и показывает, что тема живая.**

### Внимание

Прошивка чужого железа может его окирпичить. Перед любыми записями снимай полный бэкап (см.
[docs/backup.md](docs/backup.md)) и не трогай разделы `bootloader`, `rfparam` (RF-калибровка) и
`netparam` (MAC). Всё, что тут есть, — как есть, без гарантий.

---

## In English

I grabbed a handful of those ~$3 "A9 mini" spy-cams off AliExpress and realised they're useless
without the sketchy vendor app and some server in China. They phone home, and what exactly they
send is anyone's guess. I wanted one thing: the camera handing me a video stream on my own LAN,
nothing else.

The chip inside is a Beken BK7252N (Wi-Fi + BLE, hardware JPEG encoder and a camera interface).
There's no open firmware aimed at it yet: the great [daniel-dona/beken7252-opencam](https://github.com/daniel-dona/beken7252-opencam)
targets the older BK7252, and OpenBeken runs but has no camera driver. On top of that the BK7252N
has a **different peripheral map** — the camera block lives in the `0xA0xxxx` range that doesn't
exist on the old part, so old firmware won't just run.

Done so far:

- Dumped the stock firmware straight over the RT-Thread UART console (`fal read`), no bootrom mode.
- Worked out the flash format (Beken's 32+2 CRC interleave), de-interleaved it and disassembled.
- **Recovered the BK7252N camera register map** and confirmed it against two different firmwares on
  the chip: JPEG encoder `0xA03000`, DVP/CIS capture `0xA04000`, sensor I2C/SCCB `0x802410`, clocking
  `0x800180`, output DMA `0x809000`. See [docs/RE_findings.md](docs/RE_findings.md).
- Got the old SDK building again on a modern box (Ubuntu 24.04, python3, scons 4) — recipe in
  [docs/BUILD_NATIVE_WORKING.md](docs/BUILD_NATIVE_WORKING.md).
- Wrote the application side: an HTTP MJPEG server (view it in a browser or VLC), Wi-Fi STA/SoftAP,
  and config kept in EasyFlash instead of the cloud.

Still cooking: nailing the camera register bit-fields and getting the first live frame on hardware.
That part is iterative (build → flash → read the log → fix), but the groundwork is in place.

My units use a GC0311 (VGA) sensor; auto-detect is kept for everything the stock supports
(GC0311/GC0312/GC0328, HI704, OV7670/7673, SP0A38).

So this is a working logbook of the reverse-engineering and the port, not a flash-and-go release yet.
But if you've got the same cameras and you're done with the cloud, you're in the right place.

**If this saved you time or you just want to follow along, drop a star ⭐ — it genuinely helps and
tells me the topic is worth pushing.**

### Heads-up

Reflashing someone else's hardware can brick it. Take a full backup before writing anything
(see [docs/backup.md](docs/backup.md)) and leave the `bootloader`, `rfparam` (RF calibration) and
`netparam` (MAC) partitions alone. Everything here is provided as-is, no warranties.

---

## Thanks / спасибо

- [daniel-dona/beken7252-opencam](https://github.com/daniel-dona/beken7252-opencam) — the RT-Thread A9 project this builds on
- [OpenBeken](https://github.com/openshwprojects/OpenBK7231T_App) and the openshwprojects flash dumps
- The teardown threads on elektroda.com that mapped out these boards
