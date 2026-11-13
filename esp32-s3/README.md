# PHEV-Bridge LTE (ESP32-S3)

Az ESP32-S3 firmware az Outlander saját Wi-Fi hálózatán keresztül olvassa és vezérli az autót, a SIM-kártyás LTE modem pedig közvetlenül az MQTT brokerhez csatlakozik. Az első indításkor megjelenő beállító hotspoton csak a jármű VIN-jét és az admin felületen létrehozott pairing code-ot kell megadni. A párosító oldal lekéri a járműhöz tartozó autó-Wi-Fi adatokat és eszközspecifikus MQTT-hitelesítést, majd elmenti őket az ESP32 NVS tárába.

## Első beállítás

1. Helyezz a modembe aktivált, mobilinternetre képes SIM-kártyát. A SIM PIN-kérés legyen kikapcsolva.
2. Kapcsold be a modult, majd csatlakozz a `PHEV-Bridge-Setup` Wi-Fi hálózathoz.
3. Nyisd meg a telefonon felugró captive portált, vagy böngészőben a `http://192.168.4.1` címet.
4. Add meg a jármű 17 karakteres VIN-jét és az admin felületen kapott pairing code-ot.
5. A telefonon legyen elérhető internet a mobilhálózaton, hogy a beállító oldal elérje az API-t. Sikeres párosítás után az eszköz újraindul.

Az ESP32 LTE-kapcsolaton át MQTT-t használ WebSocket Secure (WSS) felett, így a routeren elérhető HTTPS 443-as port elegendő. A firmware TLS-sel ellenőrzi a broker tanúsítványát, majd WebSocketen továbbítja az MQTT-kereteket.

A jelenlegi nyilvános MQTT URL: `wss://mqtt.lorinszkym.synology.me:443/mqtt`. A Synology reverse proxy HTTPS forrásának a 443-as porton kell fogadnia a `mqtt.lorinszkym.synology.me` hostnevet, engedélyezett WebSocket upgrade-del, majd HTTP-n a NAS `localhost:8083` címére kell továbbítania a kérést. Az MQTT WebSocket listener útvonala `/mqtt`. A backend `.env` `PUBLIC_MQTT_URL` értékét, illetve egyedi járműrekordok `mqtt_broker_url` mezőjét is erre a WSS URL-re állítsd.

## Működés

- **Autó kapcsolat:** helyi 2,4 GHz-es Wi-Fi az Outlander `REMOTE...` hozzáférési pontjához.
- **Internet/MQTT:** SIM-kártyás LTE adatkapcsolat, MQTT WSS a backendtől kapott broker címre, device ID felhasználónévvel és eszközspecifikus secrettel.
- **Mobilhálózati APN:** ha nincs backend beállítás, a firmware a szolgáltató neve alapján választ (`net` Telekom/Yettel/Telenor esetén, egyébként `internet`). Ettől eltérő szolgáltatónál az APN-t a jármű `settings_json` beállításában `lteApn` mezőként lehet megadni.
- **Időszinkron:** az eszköz az LTE modem NTP funkciójával beállítja az órát, hogy a TLS tanúsítvány ellenőrzése sikerüljön.
- **Home Assistant:** MQTT auto-discovery.
- **Energiagazdálkodás:** a firmware alvó módja a modem/LTE újracsatlakozás viselkedését is befolyásolja; a pontos fogyasztási érték hardverfüggő.

## Fordítás és feltöltés

```bash
cd esp32-s3
pio run
pio run -t upload
pio device monitor
```

## Fontos korlátozások

- A SIM-kártyán a PIN-kérést előre ki kell kapcsolni; a párosító oldal nem kér SIM PIN-t.
- A firmware jelenlegi TinyGSM/A7670 kliensén az MQTT TCP kapcsolat TLS nélkül működik. Nyilvános, titkosítatlan brokerport használatakor a forgalom és a device secret nem védett az útvonalon. Éles telepítéshez TLS-képes modem kliens és MQTTS brokerport szükséges.
- A telefon captive portalja csak akkor tudja lekérni a backend API-t, ha a telefon operációs rendszere engedi a mobiladat használatát a beállító Wi-Fi mellett. Hiba esetén nyisd meg a portált normál böngészőben, és ellenőrizd a telefon mobiladat-kapcsolatát.
