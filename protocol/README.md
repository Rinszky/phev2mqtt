# PHEV Remote Protocol (MY14 / MY18 / MY20 / MY24)

Comprehensive wire-protocol and register specification for the Mitsubishi Outlander PHEV / Eclipse Cross PHEV Wi-Fi Remote Control ECU (`192.168.8.46:8080`).

Cross-referenced against:
- Real MY18 packet captures (`buxtronix/phev2mqtt`)
- Inventec `com.inventec.iMobile` / Mitsubishi *OUTLANDER PHEV Remote Ctrl* (`ko_wf` memory map)
- `@zivillian`'s rolling-key security analysis and `phev-remote/phevcore`

See also [SETTINGS.md](SETTINGS.md) for the complete 60-item ETACS & Air-Conditioning customization catalog (`0x16` / `0x0f` / `0x0e`).

---

## Network Transport

The vehicle acts as an 802.11n Wi-Fi Access Point with a default SSID of `REMOTExxxxxx`.
- Vehicle ECU IP: `192.168.8.46`, TCP port `8080`
- DHCP client IP: `192.168.8.47` (max 2 registered client MAC addresses)

---

## Packet Framing

```text
|  1 byte  |  1 byte  | 1 byte |  1 byte    |  N bytes   |   1 byte   |
|  [Type]  | [Length] | [Ack]  | [Register] |   [Data]   | [Checksum] |
```

- **Type**: Packet command code (`0xf3`, `0x3f`, `0x6f`, `0xf6`, `0x4e`/`0xe4`, `0x5e`/`0xe5`, `0x6e`/`0xe6`, `0xbb`, `0xcc`).
- **Length**: `len(Data) + 3` (i.e. total packet byte count minus 2).
- **Ack**: `0x00` = Request / Notification (`REQ`), `0x01` = Acknowledgement (`ACK`).
- **Register**: 1-byte register ID (or ping sequence ID `0x00..0x63` on `0xf3`/`0x3f`).
- **Data**: Variable-length register payload.
- **Checksum**: Unsigned 8-bit sum (`sum(bytes[0 .. end-1]) & 0xff`) computed *before* XOR obfuscation.

### Rolling XOR Security (`0x4e` / `0x5e` / `0x6e`)

1. Shortly after TCP connect, the car sends an unencrypted 12-byte handshake request:
   - `0x4e` (`CmdInMy14StartReq`) -> client replies `0xe4` (`CmdOutMy14StartResp`) on MY14–MY17 (Layout B)
   - `0x5e` (`CmdInMy18StartReq`) -> client replies `0xe5` (`CmdOutMy18StartResp`) on MY18–MY21 (Layout A)
   - `0x6e` (`CmdInMy24StartReq`) -> client replies `0xe6` (`CmdOutMy24StartResp`) on MY22+/MY24 (Layout A)
2. From the handshake bytes, a 256-byte `key_map` is derived with two rolling cursors `r_num = 0` (car -> client) and `s_num = 0` (client -> car).
3. Every encrypted packet is byte-wise XORed with `key_map[r_num]` or `key_map[s_num]`.
4. `r_num` increments *after* each `0x6f` (`CmdInResp`) packet; `s_num` increments *after* each `0xf6` (`CmdOutSend`) packet.
5. If the car receives an unexpected XOR byte, it replies with `0xbb` (`CmdInBadEncoding`) whose data byte `Data[0]` reveals the exact XOR key the ECU expected.

### Packet Types Summary

| Code | Name | Direction | Description |
|------|------|-----------|-------------|
| `0xf3` | `PingReq` | client -> car | Keepalive ping (`seq` `0x00..0x63`) |
| `0x3f` | `PingResp` | car -> client | Keepalive ping response |
| `0x6f` | `RespCmd` | car -> client | `Ack=0`: Register notification; `Ack=1`: Write ACK |
| `0xf6` | `SendCmd` | client -> car | `Ack=0`: Write register; `Ack=1`: Notification ACK |
| `0x4e` / `0xe4` | `Start14` | car <-> client | Security handshake (MY14 / Layout B) |
| `0x5e` / `0xe5` | `Start18` | car <-> client | Security handshake (MY18 / Layout A) |
| `0x6e` / `0xe6` | `Start24` | car <-> client | Security handshake (MY24 / Layout A) |
| `0x2f` / `0xf2` | `StartSend` | car <-> client | Keepalive / session start probe |
| `0xbb` | `BadEncoding` | car -> client | XOR key mismatch (`Data[0]` = expected XOR byte) |
| `0xcc` | `SessionClose` | car -> client | ECU closing session (e.g. sleep or unregistered MAC) |
| `0x2e` | `Error2E` | car -> client | Protocol / command error |

---

## Read Registers (`0x6f`, Car -> Client)

Two firmware layouts exist: **Layout A** (MY18+ / `EV_ECU == 1`) and **Layout B** (MY14–MY17 / `EV_ECU == 0`). Where lengths differ, `A / B` is shown.

| Reg | Name | Length (A / B) | `ko_wf` Offset | Fields | Description |
|-----|------|----------------|----------------|--------|-------------|
| `0x01` | Pre-AC Notification | `2` | `0` | `[1,1]` | `b[0]`: Remote AC equipped (`1`); `b[1]`: Pre-AC stop/alert reason |
| `0x02` | Drive Battery Warning | `4` | `2` | `[1,1,1,1]` | `b[0]`: Gun status, `b[1]`: Plug sub-state, `b[2]`: Cold/warming warning (`low nibble` 0..6, `high nibble` warming active) |
| `0x03` | ECU Capabilities | `3` | `6` | `[1,1,1]` | `b[0]`: Rolling security (`1`); `b[1]&0x04`: WPA2 PSK change; `b[2]&0x10`: V2H external power |
| `0x04` | Charge Timer Schedule (MY18) / Charge Error (MY14) | `20 / 1` | `10 / 44` | `5×[3,1] / [1]` | MY18: 5 × 4-byte charge timers (`T1..T5`); MY14: 1B charge error flag |
| `0x05` | Climate Timer Schedule (MY18) / Reg Result (MY14) | `16 / 1` | `30 / 235` | `[1, 5×3] / [1]` | MY18: `b[0]` master (`1`=off, `2`=on) + 5 × 3-byte climate timers (`T1..T5`); MY14: registration result |
| `0x06` | Alternate VIN / Charge Cancel Ack | `20 / 1` | `74 / 46` | `[1,17,1,1] / [1]` | Layout A alt: `[valid, 17B VIN, pad, regCount]`; Layout B: timer cancel status |
| `0x07` | Wi-Fi Channel | `1` | `237` | `[1]` | Current 2.4 GHz AP channel (`1..13`) |
| `0x08` | Wi-Fi Password Change Result | `1` | `234` | `[1]` | Result code after committing new WPA2 passphrase (`0`=idle/ok, `1`=success, `2`=fail) |
| `0x09` | Timer Write Result | `1` | `49` | `[1]` | ECU response code for timer schedule write (`0`=ok) |
| `0x0a` | Pre-AC Write Result | `1` | `50` | `[1]` | ECU response code for immediate Pre-AC command (`0`=ok) |
| `0x0b` | Wi-Fi Communication Mode | `1` | `238 / 236` | `[1]` | Wireless LAN operating mode flag |
| `0x0c` | Quick Charger Sub-Status | `1` | `51` | `[1]` | CHAdeMO / charger sub-status (`3` = quick charger active, `1`/`4` = idle) |
| `0x0d` | Charge Timer Inhibition Flag | `1` | `52` | `[1]` | `1` = charging paused waiting for timer window; `2`/`4` = not inhibited |
| `0x0e` | Customization Read Status | `1` | `53` | `[1]` | ETACS customization readback status |
| `0x0f` | Customization Commit Result | `1` | `54` | `[1]` | Result of `0x0f`/`0x0e` ETACS setting update (`0`=ok) |
| `0x10` | Pre-AC Operating State & Stop Time | `3 / 1` | `55` | `[1,1,1] / [1]` | `b[0]`: `0`=off, `1`=starting, `2`=on, `3`=terminated; `b[1..2]`: target stop `(dow<<5)\|hour`, `minute` |
| `0x11` | SSID Change Result | `1` | `58` | `[1]` | Result of `0x14` Wi-Fi SSID change (`0`=ok) |
| `0x12` | Vehicle Real-Time Clock | `7` | `59` | `[7]` | `[YY, MM, DD, hh, mm, ss, dow]` (`YY` = year − 2000, `dow`: `0`=Sun..`6`=Sat) |
| `0x13` | Comm Start Notification | `1` | `66` | `[1]` | ECU startup / pending event notification flag (acked via Write `0x07`) |
| `0x14` | Theft Alarm History Event | `7` | `67` | `[6,1]` | `b[0..5]`: `[YY,MM,DD,hh,mm,ss]` timestamp; `b[6]`: trigger cause (`0`=none, `1`=intrusion, `2`=movement, `3`=door, `4`=key, `5`=vibration, `6`=access) |
| `0x15` | VIN & Registered Clients | `20` | `246 / 74` | `[1,17,1,1]` | `b[0]`: valid (`1`), `b[1..17]`: 17-char ISO VIN (ASCII), `b[18]`: `0`, `b[19]`: registered phone count (`1..2`) |
| `0x16` | ETACS / AC Customization Settings | `8` | `95` | `[1,2,2,2,1]` | Stream of 20 frames carrying up to 60 16-bit LE setting records (see [SETTINGS.md](SETTINGS.md)) |
| `0x17` | Charge Override / Timer Sub-State | `1` | `103` | `[1]` | Charge timer override status (`1`=overridden, `2`=timer active) |
| `0x18` | V2H External Power (MY18) / Charge Timers (MY14) | `4 / 16` | `276 / 104` | `[1,1,2] / [5,3,5,3]` | MY18: V2H/V2L `[active, err, pad, pad]`; MY14: Charge timers `T1..T2` |
| `0x19` | Charge Timer T3 & Flags (MY14) | `9` | `120` | `[1,5,3]` | MY14 Layout B charge timer 3 & enable bitmask |
| `0x1a` | Ignition & AC Operating Details | `5 / 2` | `129` | `[1,1,1,1,1] / [1,1]` | `b[0]`: Ignition (`0`=off, `3`=acc, `4`=on, `7`=ready); `b[1]`: AC running (`1`); `b[2]`: dur (`0/1/2`=10/20/30m); `b[3]`: delay (`0/1/2`=0/5/10m); `b[4]`: delay pending (`1`) |
| `0x1b` | Charge Timer Master Enable | `1` | `134` | `[1]` | Low nibble: `2` = charge timer master ON (e.g. `0x12`), `1` = OFF/cancelled (e.g. `0x11`) |
| `0x1c` | AirCon Mode & Duration | `1` | `136` | `[1]` | Bits `0..3`: `1`=cool, `2`=heat, `3`=windscreen; Bits `4..7`: `0`=10m, `1`=20m, `2`=30m |
| `0x1d` | Drive Battery SOC & Parking Lights | `4` | `137` | `[1,1,1,1]` | `b[0]`: SOC `0..100%` (`>100` invalid); `b[1]`: sub-state; `b[2]`: parking lights (`1`=on); `b[3]`: light ctrl inhibited |
| `0x1e` | Charge Plug Status | `2` | `141` | `[1,1]` | `b[0]`: power flow (`0`=idle, `1/2`=charging); `b[1]`: plug (`0/3`=unplugged, `1`=plugged waiting for timer, `2`=plugged ready) |
| `0x1f` | Charging Status & Time Remaining | `3` | `143` | `[1,2]` | `b[0]`: `0`=off, `1`=normal AC, `2/4/5`=quick DC; `b[1..2]`: uint16 LE remaining minutes (`0xffff` = N/A) |
| `0x20` | Climate Timers 1–3 (MY14) | `10` | `146` | `[2,2,2,2,2]` | MY14 Layout B climate schedule slots |
| `0x21` | Remote Light Control Status | `1` | `156` | `[1]` | Remote headlamp/position lamp operation status (`0`=normal) |
| `0x22` | Climate Timer Presets (MY14) | `6` | `157` | `[2,2,2]` | MY14 Layout B climate schedule bytes preserved in Write `0x02[0..5]` |
| `0x23` | Lights, Theft Alarm & Climate Timer Armed | `5` | `163` | `[1,1,1,1,1]` | `b[0]`: climate timer armed (`1`); `b[1]`: theft alarm (`1/6`=armed, `4/5`=triggered, `7/0`=disarmed); `b[3]`: hazard (`1`=on, `2`=off); `b[4]`: interior (`1`=on, `2`=off) |
| `0x24` | Doors, Locks & Headlights | `10` | `168` | `10×[1]` | `b[0]`: `1`=locked, `2`=unlocked; `b[3]`: driver, `b[4]`: passenger, `b[5]`: RR, `b[6]`: RL, `b[7]`: boot, `b[8]`: bonnet (`1`=open); `b[9]`: headlights (`1`=on) |
| `0x25` | Tyre /TPMS / Auxiliary Status | `3` | `178` | `[1,1,1]` | Auxiliary status bytes (typically `0e 00 ff`) |
| `0x26` | Theft Alarm Equip / Sub-Flag | `1` | `181` | `[1]` | Alarm sub-status byte |
| `0x27` | Wi-Fi Always-Communicate State | `1` | `182` | `[1]` | `1` = Keep Wi-Fi AP active when parked (`Always Communicate`), `0` = normal sleep |
| `0x28` | Wi-Fi SSID | `32` | `183` | `[32]` | Null/`0xff`-padded 32-byte ASCII SSID string |
| `0x29` | Vehicle Variant & Steering Side | `3 / 2` | `216` | `[1,1,1] / [1,1]` | `b[0]`: variant; `b[1]`: `1` = **RHD** (Right-Hand Drive), `2` = **LHD** (Left-Hand Drive) |
| `0x2a` | Client Registration State | `1` | `219` | `[1]` | `1` = vehicle is in registration mode / registration succeeded |
| `0x2b` | Short ECU Version | `10` | `220` | `[10]` | 10-byte ASCII firmware version |
| `0x2c` | Unregistration Status | `1` | `233` | `[1]` | Client deregistration result (`0`=idle, `1`=unregistered) |
| `0xaa` | ECU Ready Beacon | `1` | — | `[1]` | Sent when ECU is ready after wake/registration |
| `0xc0` | Full ECU Version | `13` | `220` | `[10,2,1]` | `b[0..9]`: 10-char ASCII version (e.g. `0052022000`); `b[10..11]`: protocol/subversion (`10 01` / `11 00`) |

---

## Detailed Register Formats

### `0x04` (Read, 20B) & `0x19` (Write, 21B) — MY18+ Charge Timer Schedule

- **Read `0x04` (20 bytes):** 5 consecutive 4-byte timer slots (`Timer 1` .. `Timer 5`). Master enable is reported in Read `0x1b` (`(b[0] & 0x0f) >= 2`).
- **Write `0x19` (21 bytes):** 5 consecutive 4-byte timer slots (`bytes[0..19]`) + `byte[20]` (`0x02` if any slot is configured, `0x00` if all slots are unset). Immediately followed by **Write `0x17`** = `0x02` (if any slot is enabled) or `0x01` (if none enabled).

Each 4-byte Charge Timer slot (`b[0..3]`):

| Byte | Bits | Meaning |
|------|------|---------|
| `b[0]` | bit `0` | **Full Charge** (`1` = continue until 100%, `0` = stop strictly at end time) |
| `b[0]` | bits `1..7` | **Days of Week** 7-bit mask (`bit 1`=Sun, `bit 2`=Mon, `bit 3`=Tue, `bit 4`=Wed, `bit 5`=Thu, `bit 6`=Fri, `bit 7`=Sat; `0xfe` = `0x7f<<1` = Everyday) |
| `b[1]` | bits `0..2` | **Start Minute** / 10 (`0..5` -> `00..50`; `7` when unset) |
| `b[1]` | bits `3..7` | **Start Hour** (`0..23`; `31` when unset) |
| `b[2]` | bits `0..2` | **End Minute** / 10 (`0..5` -> `00..50`; `7` when unset) |
| `b[2]` | bits `3..7` | **End Hour** (`0..23`; `31` when unset) |
| `b[3]` | bits `0..1` | **Slot State**: `1` = Enabled, `2` = Disabled, `3` = Unset (`00 ff ff 03`) |

Example from real MY18 capture: `7d 38 b0 01` -> `FullCharge=1`, `Days=0x3e` (Mon–Fri), `Start=07:00`, `End=22:00`, `Enabled=true`.

---

### `0x05` (Read, 16B) & `0x1a` (Write, 16B) — MY18+ Climate Timer Schedule

> **Important Asymmetry:** Read `0x05` puts the Master Enable byte at `byte[0]` followed by 5 × 3B timers (`bytes[1..15]`), whereas Write `0x1a` puts the 5 × 3B timers first (`bytes[0..14]`) and the scheduled **Climate Mode** at `byte[15]`!

- **Read `0x05` (16 bytes):**
  - `byte[0]`: `0x02` = Master Climate Timer ON, `0x01` = Master Climate Timer OFF.
  - `bytes[1..15]`: 5 × 3-byte timer slots (`Timer 1` .. `Timer 5`).
- **Write `0x1a` (16 bytes):**
  - `bytes[0..14]`: 5 × 3-byte timer slots (`Timer 1` .. `Timer 5`).
  - `byte[15]`: Scheduled AC Mode (`0x01` = `cool`, `0x02` = `heat`, `0x03` = `windscreen`).
  - Immediately followed by **Write `0x03`** = `0x02` (enable master schedule if any timer is enabled) or `0x01` (disable master schedule).

Each 3-byte Climate Timer slot (`b[0..2]`, 24-bit little-endian integer `u24 = b[0] | (b[1]<<8) | (b[2]<<16)`):

| Bits (LE) | Byte / Mask | Meaning |
|-----------|-------------|---------|
| `0..1` | `b[0] & 0x03` | **Duration**: `0` = 10 min, `1` = 20 min, `2` = 30 min |
| `2..8` | `(b[0]>>2) \| ((b[1]&0x01)<<6)` | **Days of Week**: `bit 0`=Sun, `bit 1`=Mon .. `bit 6`=Sat (`0x7f` = Everyday) |
| `9..11` | `(b[1]>>1) & 0x07` | **Start Minute** / 10 (`0..5` -> `00..50`; `7` when unset) |
| `12..16` | `(b[1]>>4) \| ((b[2]&0x01)<<4)` | **Start Hour** (`0..23`; `31` when unset) |
| `17..18` | `(b[2]>>1) & 0x03` | **Slot State**: `1` (`0x02`) = Enabled, `2` (`0x04`) = Disabled, `3` (`0x07`, bytes `00 fe 07`) = Unset |

Examples verified against MY18 captures:
- `04 c0 02`: `dur=0` (10m), `days=0x01` (Sun), `min=0` (`:00`), `hour=12`, `state=1` (Enabled) -> **Sun 12:00, 10 min, ON**.
- `1d 7a 02`: `dur=1` (20m), `days=0x07` (Sun,Mon,Tue), `min=5` (`:50`), `hour=7`, `state=1` (Enabled) -> **Sun,Mon,Tue 07:50, 20 min, ON**.
- `00 fe 07`: Unset slot.

---

### `0x10` — Pre-AC Operating State & Target Stop Time (3B)

| Byte | Meaning |
|------|---------|
| `0` | `0x00` = Off, `0x01` = Starting, `0x02` = Operating, `0x03` = Terminated early (door opened / low SOC) |
| `1` | Bits `0..4`: Target stop **Hour** (`0..23`); Bits `5..7`: Day of week (`0..6`) |
| `2` | Target stop **Minute** (`0..59`) |

Example: `02 b0 20` -> `State=2` (Operating), `0xb0 & 0x1f = 0x10 = 16`, `0x20 = 32` -> stops at **16:32** (`0xb0 >> 5 = 5` = Friday).

---

### `0x02` — Drive Battery Warning & Cold Warming (4B)

- `b[2] & 0x0f` (Warning code):
  - `0`: Normal
  - `1`: Low ambient temperature — plug-in reminder
  - `2`: Battery warming stopped (charger unplugged)
  - `3`: Extreme cold — unable to charge
  - `4`: Very cold — reduced drive output
  - `5`: Battery warming stopping (low SOC)
  - `6`: Battery warming stopped (low SOC)
- `b[2] & 0xf0 != 0`: Battery heater / warming system currently active.

---

## Write Registers (`0xf6`, Client -> Car)

| Reg | Name | Length | Payload & Description |
|-----|------|--------|-----------------------|
| `0x01` | Charge Timer Schedule (MY14) | `14` | Layout B (MY14) charge schedule write |
| `0x02` | Climate Mode & Schedule (MY14) | `15` | `b[0..5]` from `0x22`, `b[6] = mode \| (durIdx << 4)`, `b[7..14]` from `0x19[1..8]` |
| `0x03` | Climate Timer Master Enable | `1` | `0x02` = Enable climate schedule, `0x01` = Disable climate schedule |
| `0x04` | Immediate AC Enable (MY14) | `1` | `0x02` = Start Pre-AC, `0x01` = Stop Pre-AC |
| `0x05` | Sync Real-Time Clock | `8` | `[YY, MM, DD, hh, mm, ss, dow, rootedFlag(0x00)]` |
| `0x06` | Force Status Refresh | `1` | `0x03` = Request full register & settings dump |
| `0x07` | Ack Comm Start Notification | `1` | `0x01` = Acknowledge Read `0x13` |
| `0x08` | Clear Theft Alarm Event | `1` | `0x01` = Clear theft alarm notification (`0x14`) |
| `0x0a` | Control Headlights | `1` | `0x01` = ON, `0x02` = OFF |
| `0x0b` | Control Parking Lights | `1` | `0x01` = ON, `0x02` = OFF |
| `0x0e` | Commit ETACS Settings | `1` | `0x00` = Commit staged `0x0f` setting(s) to ETACS/AC ECU |
| `0x0f` | Stage ETACS Setting | `2` | `[settingID, optionIndex]` (`fe 00` = factory reset all settings) |
| `0x10` | Register Smartphone / Client | `1` | `0x01` = Register client MAC (car must be in Registration Mode) |
| `0x11` | Keep Wi-Fi Always Active | `1` | `0x01` = Always-on Wi-Fi ON, `0x00` = Normal Wi-Fi sleep |
| `0x12` | Clear Charge Error | `1` | `0x01` = Acknowledge charge error notification |
| `0x13` | Clear Pre-AC Terminated State | `1` | `0x01` = Reset `0x10` `PreACTerminated` (`0x03`) state |
| `0x14` | Change Wi-Fi SSID | `32` | 32-byte null-padded ASCII SSID |
| `0x15` | Unregister Client | `1` | `0x01` = Remove current client MAC registration |
| `0x17` | Charge Timer Master / Override | `1` | `0x02` = Enable charge timer; `0x01` then `0x11` = Cancel/override timer & charge now |
| `0x18` | Set Wi-Fi Comm Mode | `1` | Wireless LAN mode configuration |
| `0x19` | Set Charge Timers (MY18+) | `21` | `5 × 4B` charge timers + `1B` mode (`0x02` if any configured, else `0x00`) |
| `0x1a` | Set Climate Timers (MY18+) | `16` | `5 × 3B` climate timers + `1B` mode (`1`=cool, `2`=heat, `3`=windscreen) |
| `0x1b` | Immediate Climate Control (MY18+) | `4` | `[state(2=on,1=off), mode(1=cool,2=heat,3=windscreen), dur(0=10m,1=20m,2=30m), delay(0=0m,1=5m,2=10m)]` |
| `0x1c` | Set Wi-Fi Channel | `1` | `0x01..0x0d` (Wi-Fi channel `1..13`) |
| `0x1d` | Stage Wi-Fi WPA2 Password | `var` | ASCII passphrase bytes |
| `0x1e` | Commit Wi-Fi WPA2 Password | `1` | `0x01` = Apply new WPA2 passphrase |
