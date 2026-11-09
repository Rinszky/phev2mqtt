# PHEV-Bridge LTE (ESP32-S3) — Teljes Összeszerelési és Gyártási Útmutató (Assembly Guide)

> **Verzió:** 1.0.0  
> **Céljármű:** Mitsubishi Outlander PHEV (2013–2024) / Eclipse Cross PHEV  
> **Cél:** Gyári szintű, plug-and-play, "out-of-the-box" hardvermodul építése, amely folyamatos távoli elérést és Home Assistant integrációt biztosít 4G LTE-n és WireGuard VPN-en keresztül.

---

## Tartalomjegyzék
1. [A rendszer működési elve](#1-a-rendszer-működési-elve)
2. [Anyagjegyzék (Bill of Materials - BOM)](#2-anyagjegyzék-bill-of-materials---bom)
3. [Autóipari tápellátás és védelmi áramkör](#3-autóipari-tápellátás-és-védelmi-áramkör)
4. [Hardver bekötési rajz és kapcsolás](#4-hardver-bekötési-rajz-és-kapcsolás)
5. [Összeszerelés lépésről lépésre (Step-by-Step)](#5-összeszerelés-lépésről-lépésre-step-by-step)
6. [Beszerelés az Outlander PHEV-be](#6-beszerelés-az-outlander-phev-be)
7. [Szoftveres felprogramozás (Provisioning)](#7-szoftveres-felprogramozás-provisioning)
8. [Üzembe helyezési és minőségellenőrzési lista (QA Checklist)](#8-üzembe-helyezési-és-minőségellenőrzési-lista-qa-checklist)
9. [Hibaelhárítási útmutató (Troubleshooting)](#9-hibaelhárítási-útmutató-troubleshooting)

---

## 1. A rendszer működési elve

Az Outlander PHEV gyári távvezérlése kizárólag a gépkocsi saját 2.4 GHz-es Wi-Fi Access Pointján (`REMOTExxxxxx`) keresztül működik, legfeljebb 10–20 méteres hatótávolságban.

A **PHEV-Bridge LTE** egy dobozolt, gépjárműbe rejtett átjáró, amely:
1. **Belső Wi-Fi rádión** folyamatosan az autó Wi-Fi AP-jára csatlakozik (`192.168.8.46:8080`).
2. **4G LTE modemen** keresztül mobilinternetet biztosít.
3. **Beépített WireGuard VPN alagutat** hoz létre az otthoni hálózatod felé, kikerülve a mobilnetes NAT-ot és tűzfalakat.
4. **Kétirányú MQTT átjáróként** publikálja az akkumulátor, hűtés/fűtés, ajtó- és töltésadatokat a Home Assistant felé, valamint fogadja a vezérlőparancsokat.
5. **Védi a 12V-os segédakkumulátort**: méri a feszültséget, és ha a jármű 3-4 napig nincs töltőn, automatikusan ultra-alacsony fogyasztású alvó módba (Duty-Cycle Deep Sleep, < 3 mA) kapcsol.

```mermaid
flowchart LR
    subgraph Car ["Mitsubishi Outlander PHEV"]
        ECU["Wi-Fi ECU\n(192.168.8.46)"]
        BAT12["12V Kisakku\n(Csomagtartó)"]
    end

    subgraph Bridge ["PHEV-Bridge LTE Doboz"]
        ESP["ESP32-S3\n(Dual Core / 16MB)"]
        MODEM["LTE Modem\n(SIM7600 / A7670)"]
        DCDC["Autóipari DC-DC\n12V -> 5V/3A"]
        SENSE["12V Feszültségosztó\n(100k / 10k)"]
    end

    subgraph CloudHome ["Otthoni Hálózat (Home LAN)"]
        WG["WireGuard Szerver\n(10.0.0.1)"]
        MQTT["Mosquitto MQTT Broker"]
        HA["Home Assistant"]
    end

    ECU <== "2.4 GHz Wi-Fi" ==> ESP
    BAT12 ==> DCDC ==> ESP & MODEM
    BAT12 -.-> SENSE -.-> ESP
    ESP <== "UART AT / PPP" ==> MODEM
    MODEM <== "4G LTE Cellular" ==> WG
    WG <==> MQTT <==> HA
```

---

## 2. Anyagjegyzék (Bill of Materials - BOM)

Két megvalósítási út lehetséges:
- **"A" Opció (Ajánlott "All-in-One"):** Gyárilag integrált ESP32-S3 + LTE lapka (pl. **LilyGO T-SIM7600E-H** vagy **LilyGO T-A7670E**). Kevesebb forrasztás, kompakt méret, gyári SIM foglalat.
- **"B" Opció (Moduláris diszkrét):** Különálló ESP32-S3 DevKit + különálló SIMCOM LTE modul. Olcsóbb, könnyen cserélhető komponensek.

### 2.1. Fő komponensek (BOM Lista)

| Tétel | Mennyiség | Megnevezés / Típus | Ajánlott forrás / Cikkszám | Becsült ár | Megjegyzés |
|---|---|---|---|---|---|
| **1** | 1 db | **ESP32-S3 Fejlesztői Kártya** | ESP32-S3-DevKitC-1-N16R8 (16MB Flash, 8MB PSRAM) | ~3 500 Ft | Csak a B opcióhoz kell |
| **2** | 1 db | **4G LTE Mobil Modem** | Waveshare SIM7600G-H vagy SIMCOM A7670E UART modul | ~11 000 Ft | Csak a B opcióhoz kell |
| **[Alt]**| 1 db | **All-in-One Integrált Lapka** | **LilyGO T-SIM7600E-H ESP32-S3** vagy **T-A7670E ESP32-S3** | ~14 000 - 18 000 Ft | **Erősen ajánlott!** Egyben van az ESP32-S3, LTE, SIM foglalat |
| **3** | 1 db | **Autóipari DC-DC Step-Down Konverter** | MP1584EN vagy LM2596 (In: 7–28V, Out: 5V 3A szinkron) | ~900 Ft | Alacsony üresjárati áramú (<2mA) típust válassz! |
| **4** | 1 db | **Mini Biztosíték Adapter (Fuse Tap)** | Low-Profile Mini (vagy Mini) Fuse Tap + 2A biztosíték | ~800 Ft | A jármű biztosítéktáblájához való biztonságos csatlakozáshoz |
| **5** | 1 db | **TVS Túlfeszültség Levezető Dióda** | SMAJ15A vagy SMBJ18A (Unidirectional TVS) | ~150 Ft | Védi a modult a generátor tranziensektől |
| **6** | 1 db | **Schottky Védődióda** | 1N5819 vagy SS34 (3A / 40V) | ~100 Ft | Fordított polaritás elleni védelem |
| **7** | 2 db | **Precíziós fémréteg ellenállások** | $R_1 = 100\ \text{k}\Omega\ (1\%)$ és $R_2 = 10\ \text{k}\Omega\ (1\%)$ | ~50 Ft | 12V feszültségmérés osztója (11:1) |
| **8** | 1 db | **Kerámia szűrőkondenzátor** | $C_1 = 100\ \text{nF}$ (50V, X7R) | ~30 Ft | ADC zajszűrés a mérőponton |
| **9** | 1 db | **4G LTE Flexibilis / Ragasztható Antenna** | FPC vagy botantenna SMA/IPEX csatlakozóval (LTE 700–2600MHz) | ~1 200 Ft | 3 dBi erősítésű ragasztható szélvédő/műanyag antenna |
| **10**| 1 db | **2.4 GHz Wi-Fi Antenna** | IPEX/U.FL -> SMA vagy belső FPC antenna | ~600 Ft | Ha külső antennás ESP32-t használsz |
| **11**| 1 db | **Műszerdoboz (Enclosure)** | ABS univerzális doboz (pl. 100x68x50mm) vagy 3D nyomtatott PETG ház | ~1 500 Ft | **NE PLA-ból nyomtasd!** Autóban nyáron 65°C is lehet! |
| **12**| 1 db | **M2 / M2.5 Távtartók és csavarok** | Réz/Nylon távtartó készlet | ~500 Ft | Lapkák stabil rögzítéséhez a dobozban |
| **13**| 1 db | **IoT / M2M SIM Kártya** | Telekom, Vodafone, Yettel vagy 1Komma5 / Things Mobile | ~500–1000 Ft/hó | Havi 50–100 MB adatforgalom bőségesen elég |

---

## 3. Autóipari tápellátás és védelmi áramkör

A gépjárművek 12V-os elektromos rendszere **nem tiszta 12V DC**:
- Nyugalomban: $12.4\text{V} - 12.8\text{V}$
- Menet közben / nagyfeszültségű akkuról töltve: $14.1\text{V} - 14.7\text{V}$
- Relék, mágnesszelepek kapcsolásakor induktív feszültségtüskék keletkezhetnek (akár $+40\text{V}$ - $+60\text{V}$ *load dump* tranziensek).

Ezért az áramkört megfelelő védelemmel kell ellátni:

```text
12V In (+) o──[ 2A Biztosíték ]──[ SS34 Dióda ]──┬──────────────────────┬──o DC-DC IN (+)
                                                 │                      │
                                            [ SMAJ15A TVS ]        [ 100k R1 ]
                                                 │                      │
                                                 │                      ├──o ADC (GPIO 1)
                                                 │                      │
                                                 │                 [ 10k R2 ] // [ 100nF C1 ]
                                                 │                      │
GND (-)    o─────────────────────────────────────┴──────────────────────┴──o DC-DC IN (-) & GND
```

### Az elemek szerepe:
1. **2A Lengő- / Panelbiztosíték:** Rövidzárlat esetén azonnal lekapcsol, megvédi az autó kábelkorbácsát.
2. **SS34 Schottky Dióda:** Meggátolja a fordított bekötésből adódó azonnali tönkremenetelt.
3. **SMAJ15A TVS Dióda:** Minden 15-18V feletti tranzienst azonnal a testre vezet.
4. **100k / 10k Feszültségosztó ($11:1$):**
   $$V_{\text{out}} = V_{\text{in}} \times \frac{10\,\text{k}\Omega}{100\,\text{k}\Omega + 10\,\text{k}\Omega} = \frac{V_{\text{in}}}{11}$$
   - $12.6\text{V}$ akkunál: $1.145\text{V}$ jut a GPIO 1 lábra.
   - $14.5\text{V}$ töltésnél: $1.318\text{V}$ jut a GPIO 1 lábra.
   - Bőségesen az ESP32-S3 ADC 0–3.1V biztonságos tartományán belül marad!
5. **100nF Szűrőkondenzátor:** Kiszűri a kapcsolóüzemű zajokat és a generátorhullámzást, stabil mérési eredményt nyújtva.

---

## 4. Hardver bekötési rajz és kapcsolás

### 4.1. Moduláris bekötés (ESP32-S3 + SIM7600/A7670)

```mermaid
graph TD
    subgraph PowerSystem ["Tápellátás & Védelem"]
        FUSE["12V Fuse Tap (2A)"] --> DIODE["SS34 Dióda"]
        DIODE --> DCDC["DC-DC Step-Down (5V 3A)"]
        DIODE --> R1["R1: 100k Ohm"]
        R1 --> ADC_NODE["ADC Mérőpont"]
        ADC_NODE --> R2["R2: 10k Ohm"]
        ADC_NODE --> C1["C1: 100nF Kondi"]
        R2 --> GND["GND Közös Test"]
        C1 --> GND
        DCDC --> GND
    end

    subgraph ESP32 ["ESP32-S3 DevKit"]
        V5["5V / VBUS"]
        GND_ESP["GND"]
        GPIO1["GPIO 1 (ADC1_CH0)"]
        GPIO17["GPIO 17 (TX1)"]
        GPIO18["GPIO 18 (RX1)"]
        GPIO4["GPIO 4 (PWRKEY)"]
        GPIO5["GPIO 5 (RESET)"]
        GPIO6["GPIO 6 (DTR)"]
        GPIO7["GPIO 7 (STATUS)"]
    end

    subgraph Modem ["LTE Modem (SIM7600 / A7670)"]
        MOD_VCC["VCC (5V / VBAT)"]
        MOD_GND["GND"]
        MOD_RX["RXD"]
        MOD_TX["TXD"]
        MOD_PWR["PWRKEY"]
        MOD_RST["RESET"]
        MOD_DTR["DTR (Sleep)"]
        MOD_STA["STATUS"]
    end

    DCDC -->|5V| V5
    DCDC -->|5V| MOD_VCC
    GND --> GND_ESP
    GND --> MOD_GND

    ADC_NODE --> GPIO1
    GPIO17 --> MOD_RX
    GPIO18 --> MOD_TX
    GPIO4 --> MOD_PWR
    GPIO5 --> MOD_RST
    GPIO6 --> MOD_DTR
    GPIO7 --> MOD_STA
```

---

## 5. Összeszerelés lépésről lépésre (Step-by-Step)

### 1. Lépés: A feszültségosztó és a táp előkészítése
1. Egy kisméretű próbapanelen (protoboard, 30x50 mm) forraszd össze a feszültségosztót:
   - Forraszd be a $100\ \text{k}\Omega$-os ellenállást a 12V bemenet és a mérőpont közé.
   - Forraszd be a $10\ \text{k}\Omega$-os ellenállást és a $100\ \text{nF}$-os kerámia kondenzátort párhuzamosan a mérőpont és a közös GND közé.
   - Vezess ki egy vezetéket a mérőpontról az ESP32-S3 `GPIO 1` lábára.
2. Állítsd be a DC-DC konverter kimeneti feszültségét:
   - Ha trimmeres LM2596-ot vagy MP1584-et használsz, **mielőtt az ESP32-re kötnéd**, labortápról / 12V-os akkuról adj neki tápot, és egy multiméterrel mérve állítsd be a kimenetet pontosan **$5.05\text{V} - 5.15\text{V}$** közé!
   - Rögzítsd a trimmerkondit egy csepp melegenyvvel vagy lakkal, hogy az autó rezgésétől ne állítódhasson el!

### 2. Lépés: A vezetékezés elkészítése
1. Kösse össze az ESP32-S3 és az LTE modul UART kommunikációs vonalait:
   - `ESP GPIO 17 (TX)` $\rightarrow$ `Modem RXD`
   - `ESP GPIO 18 (RX)` $\rightarrow$ `Modem TXD`
   - `ESP GND` $\rightarrow$ `Modem GND`
2. Kösse össze a vezérlő vonalakat:
   - `GPIO 4` $\rightarrow$ `Modem PWRKEY`
   - `GPIO 5` $\rightarrow$ `Modem RESET`
   - `GPIO 6` $\rightarrow$ `Modem DTR` (ez végzi a modem mélyalvásba küldését és felébresztését)
   - `GPIO 7` $\rightarrow$ `Modem STATUS`
3. Kösse össze a tápellátást:
   - A DC-DC konverter 5V kimenetét kösd az ESP32-S3 `5V / VBUS` lábára és a Modem `5V / VCC` lábára.
   - A GND vonalakat forraszd közös csillagpontba!

### 3. Lépés: Antennák és SIM kártya behelyezése
1. Helyezd be az aktív, PIN-kód mentes nanoSIM / microSIM kártyát a foglalatba.  
   *(Ellenőrizd előre telefonban, hogy a SIM kártya PIN kérése ki legyen kapcsolva!)*
2. Csatlakoztasd az LTE antennát a modem `MAIN` (vagy `LTE`) SMA/U.FL csatlakozójára.
3. Csatlakoztasd a Wi-Fi antennát az ESP32-S3 antennacsatlakozójára (ha külső antennás verzió).

### 4. Lépés: Beépítés a műszerdobozba
1. Rögzítsd a kártyákat műanyag (Nylon) M2.5 távtartókkal a doboz alsó feléhez.
2. Vezesd ki a tápkábelt a doboz oldalán egy gumitömítésen (kábelátvezetőn) keresztül.
3. Készíts egy USB-C nyílást a dobozon, hogy szükség esetén a kábelt közvetlenül rá tudd dugni anélkül, hogy szét kellene csavarozni a dobozt.
4. Ragaszd be az LTE antennát a doboz tetejére (belülről vagy kívülről, fém felülettől legalább 2-3 cm távolságra).

---

## 6. Beszerelés az Outlander PHEV-be

### Miért a csomagtartó jobb oldala a tökéletes beépítési hely?
A Mitsubishi Outlander PHEV-ben:
1. **A 12V-os segédakkumulátor a csomagtartó padlólemeze alatt, a jobb oldali rekeszben található!**
2. **A gyári Wi-Fi vezérlő ECU (EV Remote ECU) a hátsó üléssor mögött, a padlólemezen található!**

Ezért **NEM kell vezetékeket behúzni a műszerfalig**: a csomagtartó jobb oldali burkolata mögött minden adott:
- Maximális Wi-Fi térerő (a modul mindössze 50–80 cm-re lesz a gyári Wi-Fi antennától).
- Közvetlen, 12V-os tápellátási pont.
- Láthatatlan elhelyezés: a kárpit mögött vagy a jobb oldali rekeszben a doboz teljesen elrejthető.

### Bekötés az autó 12V-os hálózatára:
1. **Biztosíték leágazás:**
   - A csomagtartó jobb oldalában található a hátsó kiegészítő biztosítéktábla vagy a 12V-os szivargyújtó aljzat kábelkorbácsa.
   - A legtisztább megoldás a **Fuse Tap (rablóbiztosíték)** használata egy állandó 12V-os (BATT / +30) pozícióban egy 2A-es biztosítékkal.
   - Alternatívaként a 12V-os akkumulátor pozitív sarujának kiegészítő elosztójára köthető egy lengőbiztosítékházon keresztül.
2. **Test (GND) bekötése:**
   - A jobb oldali kerékdob belső felületén gyári M6-os testpont (földelőcsavar) található. Használj szemes sarut!
3. **Antenna elhelyezése:**
   - Az LTE antennát ragaszd a hátsó oldalablak műanyag kárpitja mögé (ne fémre!). Az üvegen és műanyagon a 4G jelek akadálytalanul átjutnak.

---

## 7. Szoftveres felprogramozás (Provisioning)

### 1. Konfiguráció beállítása
Nyisd meg az [`esp32-s3/include/config.h`](file:///Volumes/work/work/private/phev2mqtt/esp32-s3/include/config.h) fájlt, és add meg a saját járműved és otthoni rendszered adatait:

```cpp
// 1. Autó Wi-Fi adatai (a szervizkönyvben vagy az autón lévő matricán található)
#define DEFAULT_PHEV_WIFI_SSID       "REMOTE12a34b"
#define DEFAULT_PHEV_WIFI_PASSWORD   "abc123456789"

// 2. LTE Mobilnet APN
#define DEFAULT_LTE_APN              "net" // Telekom: "net" vagy "internet", Yettel: "net", Digi: "internet"

// 3. WireGuard VPN kulcsok (az otthoni WireGuard szerverről)
#define DEFAULT_WG_LOCAL_IP          "10.0.0.50"
#define DEFAULT_WG_PRIVATE_KEY       "eKv...=" 
#define DEFAULT_WG_ENDPOINT_HOST     "sajatdomain.duckdns.org"
#define DEFAULT_WG_ENDPOINT_PORT     51820
#define DEFAULT_WG_PEER_PUBKEY       "8Bf...="

// 4. MQTT Szerver (az otthoni szerver VPN IP címe)
#define DEFAULT_MQTT_SERVER          "10.0.0.1"
#define DEFAULT_MQTT_PORT            1883
#define DEFAULT_MQTT_USER            "homeassistant_mqtt"
#define DEFAULT_MQTT_PASS            "titkosjelszo"
#define DEFAULT_MQTT_PREFIX          "phev"
```

### 2. Fordítás és feltöltés USB-n keresztül
Csatlakoztasd az ESP32-S3 USB-C portját a számítógépedhez:

```bash
cd /Volumes/work/work/private/phev2mqtt/esp32-s3

# Fordítás ellenőrzése
pio run

# Feltöltés a mikrokontrollerre
pio run -t upload

# Soros monitor megnyitása (115200 baud)
pio device monitor
```

---

## 8. Üzembe helyezési és minőségellenőrzési lista (QA Checklist)

Mielőtt véglegesen lezárnád a dobozt és beépítenéd az autóba, végezd el az alábbi 6 pontos ellenőrzést:

- [ ] **1. Tápfeszültség és 12V mérés:**
  A soros monitoron ellenőrizd az indulási üzenetet:
  ```text
  [PWR] Power Manager initialized. 12V Battery Voltage: 12.64 V
  ```
  Ha az érték eltér a multiméteres méréstől, a `config.h`-ban lévő `BATTERY_12V_DIVIDER_RATIO` szorzót finomhangold!
- [ ] **2. LTE Mobilnet kapcsolat:**
  ```text
  [LTE] Modem Info: SIMCOM_SIM7600E, IMEI: 86...
  [LTE] Cellular network registered successfully!
  [LTE] Operator: Telekom HU, CSQ: 24 (RSSI: -65 dBm)
  [LTE] LTE Connected! Cellular IP: 10.xx.xx.xx
  ```
- [ ] **3. WireGuard VPN alagút:**
  ```text
  [VPN] Initializing WireGuard VPN tunnel...
  [VPN] WireGuard tunnel started successfully!
  ```
  Otthoni gépről ellenőrizd a pinget: `ping 10.0.0.50`.
- [ ] **4. Autó Wi-Fi kapcsolat és Rolling XOR kézfogás:**
  Állj az autó közelébe, kapcsold be a modult:
  ```text
  [PHEV] Wi-Fi connected to REMOTE12a34b (RSSI: -48 dBm)
  [PHEV] Car TCP connected successfully!
  [PHEV] Received security handshake request 0x5e, initializing rolling XOR
  [PHEV] Car VIN: JMBXDGG2W...
  [PHEV] Battery: 100% (raw 94%, maxSoc 94%), ParkingLights: OFF
  ```
- [ ] **5. Home Assistant Auto-Discovery:**
  Nyisd meg a Home Assistantot $\rightarrow$ Beállítások $\rightarrow$ Eszközök és szolgáltatások $\rightarrow$ MQTT.  
  Meg kell jelennie a **Mitsubishi Outlander PHEV** eszköznek az összes szenzorral és kapcsolóval (Akkumulátor %, Fűtés/Hűtés kapcsoló, Ajtók, 12V akku feszültség).
- [ ] **6. Távoli CLI parancssor és OTA teszt:**
  Az otthoni hálózatról lépj be:
  ```bash
  telnet 10.0.0.50 2222
  ```
  Írd be a jelszót (`phevadmin`), majd add ki a `status` és a `logs` parancsot!

---

## 9. Hibaelhárítási útmutató (Troubleshooting)

| Hibajelenség | Lehetséges ok | Megoldás |
|---|---|---|
| **Nem indul el az ESP32, villog vagy újraindul** | Tápellátási áramkorlát | Az LTE modem hálózatra csatlakozáskor csúcsban akár 1.5–2A áramot is felvehet. Győződj meg róla, hogy a DC-DC konverter legalább 3A terhelhetőségű! Tegyél egy 470µF – 1000µF elektrolit kondenzátort az 5V és a GND közé. |
| **A modem nem lát hálózatot (`waitForNetwork` timeout)** | Nincs térerő vagy hibás antenna / PIN kód | Ellenőrizd, hogy a SIM kártya PIN kérése ki van-e kapcsolva telefonban. Győződj meg róla, hogy az antenna az `LTE MAIN` csatlakozóra van tekerve, nem a `GNSS / GPS` aljzatra. |
| **Wi-Fi nem kapcsolódik az autóhoz (`WL_DISCONNECTED`)** | Túl sok regisztrált telefon az autóban | Az Outlander Wi-Fi modulja egyszerre maximum **2 regisztrált eszközt (MAC címet)** engedélyez. Ha már két telefon regisztrálva van, az autó gyári menüjében (vagy a távirányító gombjának 10-szeri megnyomásával) törölni kell a korábbi párosításokat! |
| **A Home Assistantban az akku 5%-ra ugrik** | Régi kódhiba | Ellenőrizd a `phev_client.cpp`-ben a `processBatteryGlitchFilter` meglétét: a modul automatikusan kiszűri a felébredéskor érkező téves 5%-os értékeket. |
| **A WireGuard nem csatlakozik (`Handshake timeout`)** | Helytelen porttovábbítás vagy kulcs | Ellenőrizd az otthoni routereden az 51820-as UDP port nyitását. Győződj meg róla, hogy az otthoni `AllowedIPs`-be bekerült a `10.0.0.50/32`. |
| **12V alacsony feszültség riasztás (< 11.9V)** | Rosszul kalibrált feszültségosztó | Mérd meg multiméterrel a valós feszültséget az akkumulátoron. A `config.h`-ban lévő `BATTERY_12V_DIVIDER_RATIO` értéket állítsd arányosan, amíg a szoftver által kiírt érték pontosan meg nem egyezik a multiméterrel. |

---

## 10. Összegzés és "Out-of-the-Box" Dobozolás

A fentiek alapján megépített hardver egy teljesen autonóm, zárt, megbízható rendszer:
- Nincs szükség telefonra a közelben.
- Nincs hatótávkorlát: az autó a világ bármely pontjáról elérhető és fűthető/hűthető mobilneten keresztül.
- Nem meríti le a 12V-os akkumulátort még hetekig tartó repülőtéri parkolás alatt sem.
- A firmware az otthoni fotelből, egyetlen paranccsal (`ota http://...`) frissíthető.
