# phev2mqtt — Mitsubishi Outlander PHEV to MQTT & Home Assistant Gateway

Full-featured Go utility and MQTT / Home Assistant bridge for the Mitsubishi Outlander PHEV (MY14–MY24) Wi-Fi Remote Control protocol (`192.168.8.46:8080`).

Inspired by [phev-remote](https://github.com/phev-remote/) and expanded with complete register decoding, **5-slot Climate (Heating/Cooling/Defrost) Timer scheduling**, **5-slot Charge Timer scheduling**, **dynamic ETACS/AC vehicle customization settings**, theft alarm & ignition telemetry, RTC clock sync, and automatic Home Assistant MQTT Discovery.

- Protocol & register reference: [protocol/README.md](protocol/README.md)
- ETACS / AC customization settings catalog (`0x01`–`0x3c`): [protocol/SETTINGS.md](protocol/SETTINGS.md)
- Sample Home Assistant dashboard: [lovelace.yaml](lovelace.yaml)

![Home Assistant Screenshot](phev-ha.png)

---

## Supported Features

- **MQTT Gateway & Home Assistant Auto-Discovery** (automatic re-announcement on broker or HA restart via `homeassistant/status` birth messages)
- **Immediate Pre-Conditioning (Pre-AC):**
  - Heat, Cool, and Windscreen Defrost modes
  - Selectable Duration (`10`, `20`, `30` minutes) and Start Delay (`0`, `5`, `10` minutes)
  - Live target stop time (`HH:MM`) and early-termination error reset (`0x13`)
  - Full MY14 (`0x02`/`0x04`) and MY18–MY24 (`0x1b`) support
- **5-Slot Climate / Heating Schedule (`0x05` / `0x1a` / `0x03`):**
  - Master schedule switch + scheduled mode (`heat`, `cool`, `windscreen`)
  - Per-slot Enable, Start Time (`HH:MM`), Duration (`10`/`20`/`30` min), and Days of Week (English & Hungarian presets/names) or JSON payload
- **5-Slot Battery Charging Schedule (`0x04` / `0x19` / `0x17`):**
  - Master charge timer switch + immediate *Charge Now (Override Timer)* button
  - Per-slot Enable, Start Time, End Time, Full Charge toggle, and Days of Week
  - Detailed plug sub-states (`unplugged`, `connected_waiting`, `connected`, `charging`) and charge type (`off`, `normal`, `quick`)
- **Dynamic ETACS & AC Vehicle Customization (`0x16` / `0x0f` / `0x0e`):**
  - Automatically discovers every customization option supported by your vehicle trim (auto-fold mirrors, auto-relock timer, horn/turn-signal answerback, wiper intervals, headlight cut-out, AC eco/recirculation, etc.) as Home Assistant `select` (`config`) entities!
- **Security, Ignition, Battery Health, Clock & Wi-Fi:**
  - Ignition state (`off`, `acc`, `on`, `ready`)
  - Theft alarm state (`disarmed`, `armed`, `triggered`), last alarm trigger event & timestamp (`0x14`), and clear alarm button (`0x08`)
  - Drive battery SOC (`0–100%`), cold/thermal warnings, and battery warming indicator (`0x02`)
  - Parking & headlight switches + *Locate Car (Flash Lights)* button
  - Vehicle real-time clock sensor (`0x12`), *Sync Car Clock* button (`0x05`), and optional `--auto_sync_time`
  - Wi-Fi SSID (`0x28`), Wi-Fi Channel selector (`1–11`), Keep Wi-Fi Always Active switch (`0x27` / `0x11`), LHD/RHD automatic door side mapping (`0x29`), and V2H external power status (`0x18`)
- **CLI Tools & Interactive Vehicle Emulator:**
  - `phev2mqtt client register`, `watch`, `set`, `synctime`, `timers`, `settings`, `mqtt`
  - `phev2mqtt emulator` (full interactive MY14/MY18/MY24 virtual car)
  - `phev2mqtt decode file` / `phev2mqtt decode pcap` (with `-tags pcap`)

---

## Building & Running

### Build from Source

```sh
go build -o phev2mqtt .
./phev2mqtt --help
```

*(Optional)* For live/offline PCAP decoding with `libpcap`:
```sh
go build -tags pcap -o phev2mqtt .
```

### Build with Docker

```sh
docker build -t phev2mqtt .
docker run --rm --net=host phev2mqtt client mqtt --mqtt_server tcp://192.168.1.10:1883
```

---

## CLI Commands

### Register Client MAC with Vehicle
Put the vehicle into Wi-Fi registration mode, connect to its `REMOTExxxxxx` AP (`192.168.8.47`), and run:
```sh
./phev2mqtt client register
```

### Manage Climate & Charge Timers from CLI
```sh
# List all 5 climate and 5 charge timers
./phev2mqtt client timers list

# Set Climate Timer 1 to 07:10 for 20 minutes on weekdays in Heat mode
./phev2mqtt client timers set-climate --slot 1 --time 07:10 --duration 20 --days Weekdays --mode heat --enabled

# Clear Climate Timer 2
./phev2mqtt client timers set-climate --slot 2 --clear

# Set Charge Timer 1 from 23:00 to 06:00 every day with Full Charge
./phev2mqtt client timers set-charge --slot 1 --start 23:00 --end 06:00 --days Everyday --full-charge --enabled
```

### Inspect & Modify ETACS / AC Vehicle Settings from CLI
```sh
# List all customization settings supported by your vehicle
./phev2mqtt client settings list

# Change auto-fold mirrors or exterior lights on remote unlock
./phev2mqtt client settings set auto_fold_mirrors "Open/Close by Keyless Lock/Unlock"
./phev2mqtt client settings set exterior_lights_remote_unlock "Parking lights"
```

### Synchronize Vehicle Clock
```sh
./phev2mqtt client synctime
```

---

## MQTT Gateway Reference

Start the gateway:
```sh
./phev2mqtt client mqtt \
  --mqtt_server tcp://192.168.1.10:1883 \
  --mqtt_username user \
  --mqtt_password secret \
  --auto_sync_time=true
```

Key gateway flags:
- `--mqtt_topic_prefix` (default `phev`): Base MQTT topic prefix
- `--mqtt_retain` (default `true`): Retain semantic state topics on broker
- `--ha_discovery` (default `true`): Publish Home Assistant MQTT Auto-Discovery configs
- `--ha_discovery_prefix` (default `homeassistant`): HA discovery prefix
- `--ha_keep_sensors_available` (default `false`): Keep read-only HA sensors online when car Wi-Fi sleeps
- `--auto_sync_time` (default `false`): Auto-sync vehicle RTC if drift exceeds 60s
- `--update_interval` (default `5m`): Periodic full status refresh interval

### Published State Topics (`<prefix>/...`)

| Topic | Values / Description |
|-------|----------------------|
| `phev/bridge/available` | Bridge daemon status (`online` / `offline`, LWT) |
| `phev/available` | Vehicle Wi-Fi connection status (`online` / `offline`) |
| `phev/last_seen` | UTC RFC3339 timestamp of last packet from vehicle |
| `phev/battery/level` | Drive battery SOC (`0`..`100`) |
| `phev/battery/warning` | Thermal/cold status (`normal`, `warming_up`, `extreme_cold_cannot_charge`, etc.) |
| `phev/battery/warning_active` | `on` / `off` |
| `phev/battery/warming` | Battery heater active (`on` / `off`) |
| `phev/charge/plug` | `connected` / `unplugged` |
| `phev/charge/plug_state` | `unplugged`, `connected_waiting`, `connected`, `charging` |
| `phev/charge/charging` | `on` / `off` |
| `phev/charge/type` | `off`, `normal`, `quick` |
| `phev/charge/remaining` | Minutes remaining (`0` when idle) |
| `phev/charge/timer_inhibited` | `on` when plugged in and waiting for timer window |
| `phev/charge/timer/master` | Master charge timer schedule (`on` / `off`) |
| `phev/charge/timer/<1..5>` | JSON object (`enabled`, `start`, `end`, `full_charge`, `days`) + subtopics `/enabled`, `/start`, `/end`, `/full_charge`, `/days`, `/summary` |
| `phev/climate/state` | `off`, `starting`, `delayed`, `heat`, `cool`, `windscreen`, `terminated` |
| `phev/climate/mode` | `off`, `heat`, `cool`, `windscreen` |
| `phev/climate/heat`, `/cool`, `/windscreen` | `on` / `off` |
| `phev/climate/duration` | Selected duration (`10`, `20`, `30`) |
| `phev/climate/delay` | Selected start delay (`0`, `5`, `10`) |
| `phev/climate/stop_time` | Target Pre-AC stop time (`HH:MM` or `--:--`) |
| `phev/climate/timer/master` | Master climate timer schedule (`on` / `off`) |
| `phev/climate/timer/mode` | Scheduled climate mode (`heat`, `cool`, `windscreen`) |
| `phev/climate/timer/<1..5>` | JSON object (`enabled`, `time`, `duration`, `days`) + subtopics `/enabled`, `/time`, `/duration`, `/days`, `/summary` |
| `phev/ignition/state`, `/on` | `off` / `acc` / `on` / `ready`, and `on` / `off` |
| `phev/alarm/state`, `/triggered` | `disarmed` / `armed` / `triggered`, and `on` / `off` |
| `phev/alarm/last_event` | Last theft alarm trigger reason and timestamp |
| `phev/door/locked` | `closed` (locked) / `open` (unlocked) |
| `phev/door/driver`, `/front_passenger`, `/front_left`, `/front_right`, `/rear_left`, `/rear_right`, `/bonnet`, `/boot` | `open` / `closed` (`front_left`/`front_right` automatically adapt to LHD vs RHD!) |
| `phev/lights/parking`, `/head`, `/interior`, `/hazard` | `on` / `off` |
| `phev/clock` | Vehicle RTC (`YYYY-MM-DD HH:MM:SS`) |
| `phev/wifi/ssid`, `/channel`, `/always_on` | AP SSID, channel (`1..13`), always-active mode (`on`/`off`) |
| `phev/vin`, `/vin_full`, `/ecuversion`, `/registrations`, `/variant/steering` | Vehicle identification & diagnostics |
| `phev/settings/<slug>` | Current option label of each supported ETACS/AC setting |
| `phev/register/<hex>` | Raw hex bytes of each incoming register |

### Subscribed Command Topics (`<prefix>/...`)

| Topic | Accepted Payloads |
|-------|-------------------|
| `phev/connection` | `on`, `off`, `restart` |
| `phev/set/refresh` | `press` (forces full register & settings poll `0x06=03`) |
| `phev/set/synctime` | `now` / `press` or `YYYY-MM-DD HH:MM:SS` |
| `phev/set/climate/mode` | `off`, `heat`, `cool`, `windscreen` |
| `phev/set/climate/heat`, `/cool`, `/windscreen` | `on`, `off`, `10`, `20`, `30` |
| `phev/set/climate/duration` | `10`, `20`, `30` |
| `phev/set/climate/delay` | `0`, `5`, `10` |
| `phev/set/climate/reset_error` (or `/state`=`reset`) | Clears Pre-AC `terminated` alert (`0x13=01`) |
| `phev/set/climate/timer/master` | `on` / `off` |
| `phev/set/climate/timer/mode` | `heat`, `cool`, `windscreen` |
| `phev/set/climate/timer/<1..5>` | JSON `{"enabled":true,"time":"07:10","duration":20,"days":"Weekdays"}` or `clear` |
| `phev/set/climate/timer/<1..5>/enabled` | `on` / `off` |
| `phev/set/climate/timer/<1..5>/time` | `HH:MM` (rounded to 10-min step) or `--:--` to clear |
| `phev/set/climate/timer/<1..5>/duration` | `10`, `20`, `30` |
| `phev/set/climate/timer/<1..5>/days` | `Everyday`, `Weekdays`, `Weekend`, `Mon,Wed,Fri`, `H,K,Sze,Cs,P`, `None` |
| `phev/set/charge/now` (or `/set/cancelchargetimer`) | Overrides charge timer to start charging immediately |
| `phev/set/charge/timer/master` | `on` / `off` |
| `phev/set/charge/timer/<1..5>` | JSON `{"enabled":true,"start":"23:00","end":"06:00","full_charge":true,"days":"Everyday"}` or `clear` |
| `phev/set/charge/timer/<1..5>/enabled` | `on` / `off` |
| `phev/set/charge/timer/<1..5>/start`, `/end` | `HH:MM` or `--:--` |
| `phev/set/charge/timer/<1..5>/full_charge` | `on` / `off` |
| `phev/set/charge/timer/<1..5>/days` | Day names/presets (English or Hungarian) |
| `phev/set/parkinglights`, `/set/headlights` | `on` / `off` |
| `phev/set/lights/locate` | `press` (flashes parking lights for 2 seconds) |
| `phev/set/alarm/clear` | `press` (clears theft alarm notification) |
| `phev/set/wifi/channel` | `1`..`13` |
| `phev/set/wifi/always_on` | `on` / `off` |
| `phev/set/settings/<slug>` | Option label or numeric index (`0`..`6`); `/set/settings/factory_reset` = `reset` |
| `phev/set/register/<hex>` | Raw hex bytes (unless `--mqtt_disable_register_set_command`) |

---

## License

Licensed under the GPLv2. Original work Copyright © 2021 Ben Buxton (`bbuxton@gmail.com`).
