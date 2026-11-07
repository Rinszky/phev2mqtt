# Mitsubishi PHEV Vehicle Customization Settings (`0x16` / `0x0f` / `0x0e`)

During the initial status dump (and after any setting change), the PHEV Wi-Fi ECU transmits **20 frames** on **Read Register `0x16`**, each 8 bytes long. Together, these 20 frames encode up to **60 ETACS & Air-Conditioning customization settings** (`0x01`–`0x3c`), corresponding 1:1 to `setFunc01`–`setFunc60` in the official Mitsubishi *OUTLANDER PHEV Remote Ctrl* application.

---

## Wire Encoding

### Read `0x16` (8 bytes, Car -> Client)

```text
Byte 0       : Frame header (always 0x02)
Bytes 1..2   : Setting Word A (uint16 little-endian)
Bytes 3..4   : Setting Word B (uint16 little-endian)
Bytes 5..6   : Setting Word C (uint16 little-endian)
Byte 7       : Trailer / padding (0x00)
```

Each 16-bit little-endian `uint16` Setting Word (`w = lo | (hi << 8)`):

| Bits | Mask | Field | Description |
|------|------|-------|-------------|
| `0..5` | `w & 0x003f` | `SettingID` | Setting identifier `1..60` (`0x01..0x3c`) |
| `6..8` | `(w >> 6) & 0x07` | `CurrentValue` | Current active option index (`0..6`, or `7` if unsupported) |
| `9..15` | `(w >> 9) & 0x7f` | `SupportedMask` | 7-bit capability mask: bit `i` is `1` iff option index `i` (`0..6`) is supported on this vehicle trim (`0x00` = feature not equipped) |

#### Example Decoding
Bytes `47 3e` -> `w = 0x3e47`:
- `SettingID` = `0x3e47 & 0x3f` = `0x07` (*Charging Lid Light Auto Cut-out*)
- `CurrentValue` = `(0x3e47 >> 6) & 0x07` = `1` (`1 minute`)
- `SupportedMask` = `(0x3e47 >> 9) & 0x7f` = `0x1f` (`0b0011111` -> options `0, 1, 2, 3, 4` supported)

---

### Modifying & Resetting Settings (`0x0f` + `0x0e`, Client -> Car)

Modifying a customization setting is a two-step transaction:
1. **Stage update (`Write 0x0f`, 2 bytes):** `[SettingID, OptionIndex]`
   - Example: Set *Exterior Lights ON with Remote Unlock* (`0x2b`) to `Parking lights` (`0x01`) -> `0f : 2b01`
   - Factory Reset all settings -> `0f : fe00`
2. **Commit changes (`Write 0x0e`, 1 byte):** `00`
   - Example: `0e : 00`
3. The ECU applies the change over CAN to ETACS/AC, responds with `Read 0x0f = 00`, and emits the updated `Read 0x16` frame.

---

## Complete Catalog of Setting IDs (`0x01`–`0x3c`)

All slugs below match `protocol.SettingDefinitions` in `protocol/settings.go` (`phev/settings/<slug>` <-> `phev/set/settings/<slug>`). Any reserved/trim-specific slot outside the defined catalog automatically falls back to `setting_<hex>`.

| Hex ID | Dec | Slug (`phev/settings/<slug>`) | Category | Official Setting Name | Options (`index: label`) |
|--------|-----|-------------------------------|----------|-----------------------|--------------------------|
| `0x01` | 1 | `wipers_key_position` | Wipers | Wipers Operation in Key Position | `0`: Ignition On or ACC, `1`: Ignition On |
| `0x02` | 2 | `lane_change_3flash` | Turn Signal | Lane-change 3-Flash Turn Signal | `0`: Off, `1`: On |
| `0x03` | 3 | `turn_signal_answerback` | Keyless Entry | Turn Signal Lights Answerback | `0`: Lock: Once / Unlock: Twice, `1`: Lock: Once / Unlock: Off, `2`: Lock: Off / Unlock: Twice, `3`: Lock: Twice / Unlock: Once, `4`: Lock: Off / Unlock: Once, `5`: Lock: Twice / Unlock: Off, `6`: Off |
| `0x04` | 4 | `front_wiper_intermittent` | Wipers | Windshield Wipers Intermittent Operation | `0`: Fixed (4 seconds), `1`: Adjustable, `2`: Adjustable & Speed Sensitive, `3`: Adjustable & Rain Sensitive |
| `0x05` | 5 | `wipers_linked_to_washer` | Wipers | Wipers Linked to Washer | `0`: Off, `1`: On, `2`: On with Delayed Finishing Wipe |
| `0x06` | 6 | `rear_wiper_interval` | Wipers | Rear Wiper Intermittent Interval | `0`: 0 seconds, `1`: 4 seconds, `2`: 8 seconds, `3`: 16 seconds |
| `0x07` | 7 | `charging_lid_light_cutout` | Lights | Charging Lid Light Auto Cut-out | `0`: Off, `1`: 1 minute, `2`: 3 minutes, `3`: 5 minutes, `4`: 10 minutes |
| `0x08` | 8 | `auto_fold_mirrors` | Mirrors | Auto Fold Mirrors | `0`: Off, `1`: Open by Vehicle Speed, `2`: Open/Close by IG On/Off & Driver Door, `3`: Open/Close by Keyless Lock/Unlock |
| `0x09` | 9 | `auto_light_sensitivity` | Lights | Sensitivity for Auto Light | `0`: Early, `1`: Somewhat Early, `2`: Normal, `3`: Somewhat Late, `4`: Late |
| `0x0a` | 10 | `auto_light_linked_wiper` | Lights | Auto Light Linked to Wiper | `0`: Off, `1`: On |
| `0x0b` | 11 | `dome_light_delay` | Lights | Dome Light Duration After Door Closed | `0`: 0 seconds, `1`: 7.5 seconds, `2`: 15 seconds, `3`: 30 seconds, `4`: 1 minute, `5`: 2 minutes, `6`: 3 minutes |
| `0x0c` | 12 | `headlight_auto_cutout` | Lights | Headlight Auto-cutout Function | `0`: Off, `1`: Tail/Head Lamp: Cutout Always, `2`: On, `3`: On, `4`: Tail: Cutout Once / Head: Cutout Always, `5`: On |
| `0x0d` | 13 | `interior_light_auto_cutout` | Lights | Interior Light Auto-cutout Time | `0`: Off, `1`: 3 minutes, `2`: 30 minutes, `3`: 60 minutes |
| `0x0e` | 14 | `lane_change_activation_time` | Turn Signal | Activation Time for Lane-change Signals | `0`: Short, `1`: Long |
| `0x0f` | 15 | `comfort_washer` | Wipers | Comfort Washer | `0`: Off, `1`: On |
| `0x10` | 16 | `automatic_door_unlock` | Door Locks | Automatic Door Unlock | `0`: Off, `1`: When Shifted to Park, `2`: When Shifted to Park (Windows Unlocked), `3`: When Ignition Turned to Lock, `4`: When Ignition Turned to Lock (Windows Unlocked) |
| `0x11` | 17 | `selective_door_unlock` | Door Locks | Unlock Operation | `0`: All Doors, `1`: Only Driver Door |
| `0x13` | 19 | `deadlock_button_pushes` | Door Locks | Lock Button Pushes for Dead Lock | `0`: 2 times, `1`: 1 time |
| `0x14` | 20 | `horn_answerback_lock` | Keyless Entry | Horn Answerback at Keyless Lock | `0`: Off, `1`: One Button Push, `2`: One Button Push (Daytime), `3`: Two Button Pushes, `4`: Two Button Pushes (Daytime) |
| `0x15` | 21 | `kos_answerback_beep` | Keyless Operation | Keyless Answerback Beep | `0`: All Systems Off, `1`: Sound at Keyless Operation, `2`: Sound at Keyless Entry, `3`: All Systems On |
| `0x16` | 22 | `auto_relock_time` | Keyless Entry | Automatic Relocking After Remote Unlock | `0`: 30 seconds, `1`: 1 minute, `2`: 2 minutes, `3`: 3 minutes |
| `0x18` | 24 | `power_window_keyoff_timer` | Power Windows | Power Window Timer After IG Off | `0`: 0 seconds, `1`: 30 seconds, `2`: 3 minutes, `3`: 10 minutes |
| `0x1a` | 26 | `multi_mode_keyless` | Keyless Entry | Multi Mode Keyless Entry System | `0`: Off, `1`: Window & Mirror: Open & Close, `2`: Power Window: Open & Close, `3`: Window: Close / Mirror: Open & Close, `4`: Door Mirror: Open & Close, `5`: Power Window: Close |
| `0x1c` | 28 | `horn_answerback_duration` | Keyless Entry | Duration of Horn Answerback | `0`: Short, `1`: Long |
| `0x1d` | 29 | `rear_wiper_in_reverse` | Wipers | Rear Wiper Activated in Reverse | `0`: When Rear Wiper is On, `1`: When Front or Rear Wiper is On |
| `0x20` | 32 | `remote_unlock_inactivation` | Keyless Entry | Remote Unlock Inactivation After Locking | `0`: Off, `1`: 3 seconds, `2`: 5 seconds |
| `0x21` | 33 | `horn_remote_start` | Keyless Entry | Horn Answerback at Remote Start | `0`: Off, `1`: On |
| `0x22` | 34 | `window_open_after_ig_off` | Power Windows | Window Open Operation After IG Off | `0`: On, `1`: Off |
| `0x23` | 35 | `window_lock_switch_scope` | Power Windows | Power Window Lock-switch Scope | `0`: All Doors, `1`: Only Driver Door |
| `0x24` | 36 | `acc_power_auto_cutout` | Other | Auto Cut of ACC Power | `0`: Off, `1`: 30 minutes, `2`: 60 minutes |
| `0x2a` | 42 | `headlights_on_exit` | Lights | Headlights ON When Exiting Vehicle | `0`: Off, `1`: 15 seconds, `2`: 30 seconds, `3`: 1 minute, `4`: 3 minutes |
| `0x2b` | 43 | `exterior_lights_remote_unlock` | Lights | Exterior Lights ON with Remote Unlock | `0`: Off, `1`: Parking lights, `2`: Head lights |
| `0x32` | 50 | `ac_recirculation_control` | Air Conditioner | Recirculation Control | `0`: Manual, `1`: Automatic |
| `0x33` | 51 | `ac_switch_control` | Air Conditioner | A/C Switch Control | `0`: Manual, `1`: Automatic |
| `0x35` | 53 | `ac_eco_mode` | Air Conditioner | A/C Eco Mode | `0`: Eco, `1`: Comfort |
| `0x36` | 54 | `foot_defroster_airflow_ratio` | Air Conditioner | Foot / Defroster Airflow Ratio | `0`: Normal, `1`: More Airflow to Foot, `2`: More Airflow to Defroster |
| `0x37` | 55 | `face_foot_airflow_ratio` | Air Conditioner | Face / Foot Airflow Ratio | `0`: Normal, `1`: More Airflow to Face, `2`: More Airflow to Foot |
| `0x38` | 56 | `auto_rear_defrost_at_start` | Air Conditioner | Automatic Rear Defrost at Start | `0`: Off, `1`: On |
| `0x39` | 57 | `start_ac_at_door_unlock` | Air Conditioner | Start A/C or Heater at Door Unlock | `0`: Off, `1`: On |
| `0x3a` | 58 | `foot_mode_defroster_bleed` | Air Conditioner | Slight Defroster Airflow in Foot Mode | `0`: Off, `1`: On |
| `0x3c` | 60 | `fast_idle_heater_boost` | Air Conditioner | Fast Idle for Higher Heater Performance | `0`: Off, `1`: On |
