# Звуки кабины v2 — источники

Собрано 14.10.2026 для экрана хода работы (RUSL-1786). Все файлы взяты из открытых моделей самолётов для симулятора FlightGear, лицензия **GPL-2.0**. Громкость выровнена (loudnorm −16 LUFS, пик ≤ −1.5 dB), формат mp3 44.1 кГц: v1 был в Ogg, а его iPhone не воспроизводит. Короткие циклы (CRC, пожарный звонок, сверчок, тряска штурвала, «Traffic») повторены, как в кабине.

| Префикс файла | Репозиторий | Ветка |
|---|---|---|
| airbus_*, gpws_* (кроме bank_angle/minimums), retard | github.com/legoboyvdlp/A320-family | dev |
| boeing_master_warning, boeing_fire_warning, gpws_bank_angle, gpws_minimums, gpws_approaching_minimums | github.com/IskenderWang/787-family | master |
| boeing_master_caution, boeing_ap_disconnect_wailer, boeing_stick_shaker, boeing_takeoff_config, tcas_* | github.com/franck-vmd/Boeing-777-Flightgear | main |
| boeing_fire_bell_737, boeing_altitude_alert | github.com/YV3399/737-800YV | master |
| overspeed_clacker | github.com/Octal450/MD-11 | master |

Используем только внутри, на личном экране. Если когда-нибудь будем распространять, по GPL-2.0 нужно приложить этот файл и ссылки на исходники.

Качество исходников: большинство файлов 44.1 кГц; stick shaker (11 кГц), пожарный звонок 737 (11 кГц) и TCAS (24 кГц) — ниже, но лучше доступных вариантов.

Так и не нашлось: «Windshear» (ни в одном из открытых самолётов).
