# ESP32-S3 Outlander PHEV Remote Bridge (phev2mqtt)

Komplett, önálló beágyazott hardveres verzió a `phev2mqtt` alkalmazásból **ESP32-S3** mikrokontrollerre, beépített **LTE modemmel**, **WireGuard VPN alagúttal**, **12V-os akkumulátorvédelemmel** és **távoli karbantartási felülettel (Remote CLI + OTA)**.

---

## 1. Rendszerarchitektúra

```
┌────────────────────────────────────────────────────────────────────────┐
│                        MITSUBISHI OUTLANDER PHEV                       │
│                                                                        │
│   [ Wi-Fi AP: "REMOTExxxxxx" ]              [ 12V Kisakku: 35-45Ah ]  │
│          ▲                                             │               │
└──────────┼─────────────────────────────────────────────┼───────────────┘
           │ 2.4 GHz Wi-Fi STA                           │ 12V DC
           │ (192.168.8.46:8080)                         │
┌──────────▼─────────────────────────────────────────────▼──────────────┐
│  ESP32-S3 DUAL-NETWORK BRIDGE                                          │
│                                                                        │
│  ┌───────────────────────┐          ┌───────────────────────────────┐  │
│  │ Wi-Fi STA Interfész   │          │ DC-DC Step-Down (12V -> 5V/3A)│  │
│  │ (Autó kommunikáció)   │          └──────────────┬────────────────┘  │
│  └───────────────────────┘                         │                   │
│  ┌───────────────────────┐          ┌──────────────▼────────────────┐  │
│  │ Rolling XOR Titkosítás│          │ 12V Feszültségosztó (ADC1)   │  │
│  │ & Regiszter Feldolgozó│          │ (100k / 10k -> GPIO 1)       │  │
│  └──────────┬────────────┘          └──────────────┬────────────────┘  │
│             │                                      │                   │
│  ┌──────────▼────────────┐          ┌──────────────▼────────────────┐  │
│  │ Power Manager         ├──────────► 3-4 nap inaktivitás -> SLEEP │  │
│  │ 12V Akkumulátorvédelem│          │ <11.9V -> Vészhelyzeti Sleep  │  │
│  └───────────────────────┘          └───────────────────────────────┘  │
│             │                                                          │
│  ┌──────────▼────────────┐          ┌───────────────────────────────┐  │
│  │ UART1 Interfész       ◄──────────► LTE MODEM (SIM7600 / A7670)   │  │
│  │ (AT / TinyGSM / PPP)  │          │ Mobilinternet (4G / LTE)      │  │
│  └──────────┬────────────┘          └──────────────┬────────────────┘  │
│             │                                      │                   │
│  ┌──────────▼────────────┐                         │                   │
│  │ WireGuard VPN Alagút  ◄─────────────────────────┘                   │
│  │ (10.0.0.50 -> Home)   │                                             │
│  └──────────┬────────────┘                                             │
│             │                                                          │
│  ┌──────────▼────────────┐          ┌───────────────────────────────┐  │
│  │ MQTT Kliens           │          │ Remote CLI Shell (Port 2222)  │  │
│  │ & HA Auto-Discovery   │          │ Élő Logok, OTA, Újraindítás   │  │
│  └──────────┬────────────┘          └──────────────┬────────────────┘  │
└─────────────┼──────────────────────────────────────┼───────────────────┘
              │ WireGuard Titkosított Csatorna       │
              ▼                                      ▼
┌────────────────────────────────────────────────────────────────────────┐
│                          OTTHONI HÁLÓZAT (LAN)                         │
│                                                                        │
│   [ WireGuard Szerver ]  ◄───►  [ MQTT Broker ]  ◄───► [ Home Assist ] │
│        10.0.0.1                     10.0.0.1            192.168.1.100  │
└────────────────────────────────────────────────────────────────────────┘
```

---

## 2. Hardver és bekötési rajz (Pinout)

### Ajánlott hardverkomponensek:
1. **Mikrokontroller:** ESP32-S3-WROOM-1 / DevKitC-1 (16MB Flash, 8MB Octal PSRAM ajánlott).
2. **LTE Modem:** Waveshare SIM7600G-H / SIM7000G / SIMCOM A7670E (LTE Cat-1 vagy Cat-4, microSIM / nanoSIM foglalattal, külső 4G antennával).
3. **Tápellátás:** 12V $\rightarrow$ 5V/3A nagy hatásfokú DC-DC szinkron step-down konverter (pl. MP1584EN vagy LM2596, alacsony készenléti árammal).
4. **12V Akkumulátormérő feszültségosztó:** 
   - $R_1 = 100\ \text{k}\Omega$ (12V felőli oldal)
   - $R_2 = 10\ \text{k}\Omega$ (GND felőli oldal)
   - $C_1 = 100\ \text{nF}$ kerámia szűrőkondenzátor a mérőpont és a GND közé.
   - Osztásarány: $11:1$ (14.4V töltésnél $\approx 1.30\text{V}$ jut az ADC lábra).

### Lábkiosztás (ESP32-S3):

| ESP32-S3 Pin | Funkció | Csatlakozás |
|---|---|---|
| **GPIO 17** | `MODEM_TX` | Modem RXD |
| **GPIO 18** | `MODEM_RX` | Modem TXD |
| **GPIO 4** | `MODEM_PWRKEY` | Modem PWRKEY (bekapcsoló impulzus) |
| **GPIO 5** | `MODEM_RESET` | Modem RESET |
| **GPIO 6** | `MODEM_DTR` | Modem DTR (alvó/ébrenléti vezérlés) |
| **GPIO 7** | `MODEM_STATUS`| Modem STATUS láb |
| **GPIO 1** | `ADC1_CH0` | 12V feszültségosztó mérőpont ($R_1 / R_2$) |
| **5V / VBUS** | Tápellátás | DC-DC Step-down 5V kimenet |
| **GND** | Közös föld | DC-DC GND, Modem GND, Akku negatív |

---

## 3. WireGuard VPN beállítás

Az LTE modem NAT mögött van (általában nincs nyilvános IP címe). A beépített WireGuard modul egy biztonságos, kétirányú alagutat nyit az otthoni hálózatod felé:
- Az otthoni WireGuard szervereden (pl. Home Assistant WireGuard addon vagy Mikrotik/pfSense) adj hozzá egy új klienst:
  ```ini
  [Peer]
  PublicKey = <ESP32_PUBLIC_KEY>
  AllowedIPs = 10.0.0.50/32
  PersistentKeepalive = 25
  ```
- Az `include/config.h` fájlban add meg a kulcsokat:
  ```cpp
  #define DEFAULT_WG_LOCAL_IP       "10.0.0.50"
  #define DEFAULT_WG_PRIVATE_KEY    "aA11...=" // ESP32 privát kulcsa
  #define DEFAULT_WG_ENDPOINT_HOST  "otthon.dinamikusdns.hu"
  #define DEFAULT_WG_ENDPOINT_PORT  51820
  #define DEFAULT_WG_PEER_PUBKEY    "bB22...=" // Otthoni szerver publikus kulcsa
  ```

---

## 4. 12V-os akkumulátorvédelem és energiatakarékos mód

Az Outlander PHEV 12V-os segédakkumulátora viszonylag kis kapacitású (~35–45 Ah). Hosszabb parkolás esetén a folyamatosan működő Wi-Fi és 4G kapcsolat lemeríthetné.

A firmware háromlépcsős intelligens védelmet valósít meg:

1. **Normál aktív mód (Active Mode):**
   - Amikor az autó töltőn van, vagy az elmúlt 3 napban töltötték / használták.
   - Folyamatos Wi-Fi és LTE kapcsolat, 15 másodpercenkénti adatfrissítés az MQTT-re.
2. **Időszakos energiatakarékos alvás (Duty-Cycle Sleep Mode):**
   - **Feltétel:** Ha az autó **3-4 napja (72 órája)** nincs töltőn, és nincs csatlakoztatva a töltőkábel.
   - A mikrokontroller lekapcsolja a Wi-Fi-t, a modemet alvó állapotba küldi (`DTR HIGH` + `AT+CSCLK=1`), és az ESP32-S3 **Deep Sleep** módba lép **30 percre**.
   - 30 percenként felébred **45 másodpercre**: felcsatlakozik az LTE-re és az MQTT-re, ellenőrzi, hogy érkezett-e távoli parancs (pl. előfűtés indítása), jelenti az akkumulátor állapotát, majd visszamegy aludni.
   - Átlagos áramfelvétel ebben a módban: **< 3 mA** (egy 40Ah-s akkut több mint 6 hónap alatt sem merítene le!).
3. **Vészhelyzeti lekapcsolás (Emergency Low-Voltage Cutoff):**
   - **Feltétel:** Ha a 12V-os feszültség **11.90V alá** esik.
   - A rendszer azonnal leállítja a modemet és a rádiókat, és hosszú, 2 órás mélyalvásba lép, megakadályozva a 12V-os akkumulátor tönkremenetelét.

---

## 5. Távoli elérés és karbantartás (Remote CLI & OTA)

Nem szükséges kiszerelni a modult az autóból hibakereséshez vagy frissítéshez! A WireGuard VPN-en keresztül közvetlenül elérhető:

### 1. Távoli parancssor (Telnet / CLI over VPN):
```bash
telnet 10.0.0.50 2222
```
A jelszó megadása után elérhető parancsok:
- `status`: Részletes állapotjelentés (12V feszültség, hajtásakku %, LTE térerő CSQ, autó Wi-Fi kapcsolat, memória).
- `logs`: Az ESP32 belső memóriájában lévő **valós idejű ring-buffer napló kiírása**.
- `reboot`: Az ESP32-S3 biztonságos újraindítása.
- `climate heat 20`: Előfűtés kézi indítása 20 percre.
- `doors lock` / `doors unlock`: Ajtók zárása/nyitása.
- `ota <http://szerver/firmware.bin>`: **Távoli firmware frissítés indítása HTTP/HTTPS linken keresztül!**

### 2. Távoli OTA frissítés MQTT-n keresztül:
Küldj egy MQTT üzenetet a `phev/system/ota/set` témakörbe a firmware URL-jével:
```text
http://10.0.0.1:8080/firmware_v1.1.bin
```
Az ESP32 letölti a fájlt a másik OTA partícióra (`ota_1`), ellenőrzi a checksumot, átállítja a boot partíciót és újraindul.

---

## 6. Fordítás és feltöltés (PlatformIO)

A projekt a szabványos PlatformIO eszközkészlettel fordul:

```bash
# Lépj be a könyvtárba
cd esp32-s3

# Fordítás
pio run

# Első feltöltés USB-n keresztül (ESP32-S3 USB Type-C port)
pio run -t upload

# Soros monitor megtekintése
pio device monitor
```

A kezdeti beállítás után minden további frissítés már távolról, az autóban lévő modul kiszerelése nélkül, OTA-n keresztül végezhető!
