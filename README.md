# eebus2mqtt – HEMS → MQTT Bridge

Dieses Projekt verbindet ein **EEBUS-kompatibles HEMS** (Energy Management System) mit einem MQTT-Broker und stellt Leistungsdaten, Grenzwerte und Failsafe-Informationen über MQTT bereit. Damit kann z. B. Home Assistant oder jede andere MQTT-basierte Automatisierung EEBUS-Geräte steuern und überwachen.

## ✨ Features

* Automatische Erstellung von Zertifikat & Schlüssel beim ersten Start
* Automatische Erstellung/Verwaltung von `config.json`
* MQTT-Passwort wird verschlüsselt gespeichert
* Verarbeitung und Bereitstellung folgender EEBUS-Use-Cases:

  * **LPP** (Load Production Prediction)

* Failsafe-Handling inkl. Countdown & Speicherung im Config-File
* Heartbeat-Überwachung → automatisches Umschalten in Failsafe-Modus
* MQTT-Output für alle relevanten Daten

---

## 📁 Projektstruktur

Wichtigste Dateien:

```
main.go
config.json (wird automatisch erzeugt)
status.log  (wird automatisch erzeugt)
```

---

## ⚙️ Konfiguration (`config.json`)

Die Datei wird beim ersten Start automatisch erzeugt.

### Beispiel:

```json
{
  "hems": {
    "certFile": "",
    "keyFile": "",
    "remoteSki": "",
    "port": 4713,
    "pv_max": 10000,
    "failsafe": "",
    "failsafe_duration": "",
    "serial_number": "1234567890"
  },
  "mqtt": {
    "mqttBroker": "192.168.1.10",
    "mqttPort": 1883,
    "mqttUsername": "user",
    "mqttPassword": "encrypted"
  }
}
```

### Bedeutung der Einstellungen

| Feld                | Beschreibung                                         |
| ------------------- | ---------------------------------------------------- |
| `remoteSki`         | SKI des EEBUS-Gerätes, mit dem gekoppelt werden soll |
| `port`              | Port auf dem gelauscht wird.                         |
| `pv_max`            | Maximale PV-Produktion (W)                           |
| `failsafe`          | Wird automatisch gesetzt: Failsafe-Grenze            |
| `failsafe_duration` | Wird automatisch gesetzt: Failsafe Dauer             |
| `serial_number`     | 10-stellige ID, wird automatisch generiert           |
| `mqttBroker`        | IP des Mqtt Brokers                                  |
| `mqttPort`          | Port des Mqtt Brockers                               |
| `mqttUsername`      | Benutzername für Mqtt Broker                         |
| `mqttPassword`      | Mqtt Passwort. Wird beim Start verschlüsselt.        |

---

## 🔒 Zertifikate

Beim ersten Start erzeugt das Programm automatisch:

* ein ECDSA-Zertifikat
* den Private Key
* speichert beide Base64-PEM-encoded in `config.json`

---

## 🔌 MQTT-Topics

Das Programm veröffentlicht u. a. folgende MQTT-Topics:

### LPP (Produktion)

| Topic                                    | Beispiel     | Beschreibung                          |
| ---------------------------------------- | ------------ | ------------------------------------- |
| `eebus2mqtt/hems/lpp/allowed_production` | `4200`       | Erlaubte Einspeiseleistung (W)        |
| `eebus2mqtt/hems/lpp/limit_activ`        | `true/false` | Aktiver Limitmodus                    |
| `eebus2mqtt/hems/lpp/LimitCountdown`     | `56`         | Countdown (s) für aktives Limit       |
| `eebus2mqtt/hems/lpp/FailsafeCountdown`  | `3600`       | Failsafe-Restdauer                    |
| `eebus2mqtt/hems/lpp/last_heartbeat`     | `3`          | Sekunden seit letztem EEBUS Heartbeat |


## 🧠 Failsafe-System

Das HEMS überwacht Heartbeats der EEBUS-Gegenstelle.

* Wenn **>120 Sekunden** kein Heartbeat kommt → **Failsafe aktiv**
* Limit & Dauer werden aus `config.json` entschlüsselt, diese dürfen vom Nutzer nicht geändert werden!
* Countdown wird ständig über MQTT ausgegeben
* Ende des Failsafe → erneuerter Heartbeat, oder Mindestdauer abgelaufen

Failsafe-Einstellungen kommen vom Netzbetreiber und werden automatisch verschlüsselt in `config.json` gespeichert.

---

## 🔑 Passwort- / Daten-Verschlüsselung

Es wird **AES-256-GCM** verwendet.


### Verschlüsselte Felder:

* MQTT-Passwort
* Failsafe-Wert
* Failsafe-Dauer

---

## 🚀 Starten

### Direkt:

```bash
go run ./devices/hems/main.go
```

### Docker:

```bash
docker build -t eebus2mqtt .
docker run -it --net=host -v $(pwd)/config.json:/config/config.json eebus2mqtt
```

---

## 📝 Logs

Datei: `status.log`
Wird automatisch generiert Speichert den System Status.

Beispiel:

```
2025-01-01 12:00:00 | limited | 4200 W
2025-01-01 12:10:00 | unlimitedControlled |
2025-01-01 12:12:30 | failsafe | 3000 W
```

---

## 🔌 Fallback-Port

Das Programm sucht automatisch einen freien Port ab dem konfigurierten Startport:

```
port → port+100
```

Falls keiner frei ist → OS wählt automatisch (`:0`).

---

## 🤝 Pairing

Das Remote-Gerät muss den Trust akzeptieren.
Bei "RemoteDeniedTrust" wird:

* Pairing abgebrochen
* SKI deregistriert
* Programm beendet

---

## 📦 Abhängigkeiten

* eebus-go
* spine-go
* ship-go
* Eclipse Paho MQTT
* Argon2id
* AES-GCM

---

## 📜 Lizenz




# Credits
All credits to https://github.com/enbility/eebus-go ! 99% of this project is their work.
