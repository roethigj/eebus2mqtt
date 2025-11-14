package main

import (
	"crypto/ecdsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/json" // CHANGE: added for reading config
	"encoding/pem"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/enbility/eebus-go/api"
	"github.com/enbility/eebus-go/service"
	ucapi "github.com/enbility/eebus-go/usecases/api"
	cslpc "github.com/enbility/eebus-go/usecases/cs/lpc"
	cslpp "github.com/enbility/eebus-go/usecases/cs/lpp"

	// eglpc "github.com/enbility/eebus-go/usecases/eg/lpc"
	// eglpp "github.com/enbility/eebus-go/usecases/eg/lpp"
	"github.com/enbility/eebus-go/usecases/ma/mgcp"
	shipapi "github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/cert"
	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/model"
)

// CHANGE: configuration struct for JSON config file

type Config struct {
	Hems HemsConfig `json:"hems"`
	Mqtt MqttConfig `json:"mqtt"`
}

type HemsConfig struct {
	CertFile    string `json:"certFile"`
	KeyFile     string `json:"keyFile"`
	RemoteSKI   string `json:"remoteSki"`
	Port        int    `json:"port"`
	InverterMax int    `json:"inverter_max"`
}
type MqttConfig struct {
	Broker   string `json:"mqttBroker"`
	Port     int    `json:"mqttPort"`
	Username string `json:"mqttUsername"`
	Password string `json:"mqttPassword"`
}

var config Config
var remoteSki string
var configfile string = "./config.json"
var logfile string = "./status.log"
var client mqtt.Client
var ekg time.Time = time.Now()
var isFailsafe bool = false

// CHANGE: attempt to read a JSON config file at startup and inject cert/key (and optional remote ski/port)
// If a config is present and command-line args do not contain cert/key, we patch os.Args so existing run() logic
// (which expects cert/key at os.Args[3]/os.Args[4] when len(os.Args) == 5) works unchanged.
//
// The config file path is "./config.json" by default. To change, set CONFIG_FILE env var.
func init() {
	var certificate tls.Certificate
	// check for log
	logPath, err := os.Open(logfile)
	if err != nil {
		log.Println("Info: no log file found at", logPath, "— I will generate it.")
	}

	// default config file path
	cfgPath, err := os.Open(configfile)
	if err != nil {
		// check for HA config
		if _, err := os.Stat("/config"); !os.IsNotExist(err) {
			configfile = "/config/config.json"
			cfgPath, err = os.Open(configfile)
			if err != nil {
				// HA first run?

				generateConfic(configfile)
			}
		} else {
			// not HA
			generateConfic(configfile)
		}

	}

	// cfgPath, err = os.Open(configfile)
	// if err != nil {
	// 	log.Fatalf("Could not open config file: %s", err)
	// }
	println(cfgPath.Name())
	// defer cfgPath.Close()

	decoder := json.NewDecoder(cfgPath)
	err = decoder.Decode(&config)
	if err != nil {
		log.Fatalf("1Unable to marshal JSON due to %s", err)
		//log.Println("Warning: failed to parse config.json:", err)
	}

	cfg := config.Hems

	if cfg.CertFile == "" || cfg.KeyFile == "" {

		// No config file present — not an error, just skip injection
		println("Info: no config file found at", configfile, "— I will generate it.")

		certificate, err = cert.CreateCertificate("eebus2mqtt", "eebus-go", "HEMS", "123456789")
		if err != nil {
			log.Fatal(err)
		}

		pemdata := pem.EncodeToMemory(&pem.Block{
			Type:  "CERTIFICATE",
			Bytes: certificate.Certificate[0],
		})
		cfg.CertFile = string(pemdata)

		b, err := x509.MarshalECPrivateKey(certificate.PrivateKey.(*ecdsa.PrivateKey))
		if err != nil {
			log.Fatal(err)
		}
		pemdata = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: b})
		cfg.KeyFile = string(pemdata)

		//cfg.RemoteSKI = "replace-with-remote-ski"
		// choose a starting port and find the next free one
		cfg.Port = availablePort(4713)
		config.Hems = cfg
		cfgPath, _ = os.Create(configfile)
		encoder := json.NewEncoder(cfgPath)
		encoder.SetIndent("", "  ")
		_ = encoder.Encode(config)
		cfgPath.Close()
		return

	}

	// if err := json.Unmarshal(data, &cfg); err != nil {
	// 	// malformed config — log and skip
	// 	fmt.Println("Warning: failed to parse config.json:", err)
	// 	return
	// }

	fmt.Println("Info: injected cert/key (and optional port/remoteSki) from config file:", cfgPath)

}

func generateConfic(conffile string) {
	log.Println("Info: no config file found at", conffile, "— I will generate it.")
	// Create basic config structure
	config = Config{
		Hems: HemsConfig{
			CertFile:    "",
			KeyFile:     "",
			RemoteSKI:   "",
			Port:        0,
			InverterMax: 10000,
		},
		Mqtt: MqttConfig{
			Broker:   "",
			Username: "",
			Port:     1883,
			Password: "",
		},
	}
	file, _ := os.Create(conffile)
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	_ = encoder.Encode(config)
	file.Close()
}

type hems struct {
	myService *service.Service

	uccslpc ucapi.CsLPCInterface
	uccslpp ucapi.CsLPPInterface
	//uceglpc   ucapi.EgLPCInterface
	//uceglpp   ucapi.EgLPPInterface
	ucmamgcp ucapi.MaMGCPInterface
}

func (h *hems) run() {
	var err error
	var certificate tls.Certificate
	var port int

	cfg := config.Hems

	if cfg.CertFile != "" && cfg.KeyFile != "" {

		remoteSki = cfg.RemoteSKI
		if remoteSki == "" || remoteSki == "replace-with-remote-ski" {
			log.Fatal("Please add Remote SKI to config.")
		}

		cert := []byte(cfg.CertFile)
		key := []byte(cfg.KeyFile)

		certificate, err = tls.X509KeyPair(cert, key)
		if err != nil {
			println("Error loading certificate and key:", err)
			log.Fatal(err)
		}
	} else {
		println("No config")
		log.Fatal(err)
	}

	port = availablePort(cfg.Port)

	configuration, err := api.NewConfiguration(
		"eebus2mqtt", "eebus2mqtt", "HEMS", "123456789",
		[]shipapi.DeviceCategoryType{shipapi.DeviceCategoryTypeEnergyManagementSystem},
		model.DeviceTypeTypeEnergyManagementSystem,
		[]model.EntityTypeType{model.EntityTypeTypeCEM},
		port, certificate, time.Second*4)
	if err != nil {
		println("Error creating configuration:", err)
		log.Fatal(err)
	}
	configuration.SetAlternateIdentifier("eebus2mqtt-HEMS-123456789")

	h.myService = service.NewService(configuration, h)
	h.myService.SetLogging(h)

	if err = h.myService.Setup(); err != nil {
		fmt.Println(err)
		return
	}

	localEntity := h.myService.LocalDevice().EntityForType(model.EntityTypeTypeCEM)
	h.uccslpc = cslpc.NewLPC(localEntity, h.OnLPCEvent)
	h.myService.AddUseCase(h.uccslpc)
	h.uccslpp = cslpp.NewLPP(localEntity, h.OnLPPEvent)
	h.myService.AddUseCase(h.uccslpp)
	// h.uceglpc = eglpc.NewLPC(localEntity, nil)
	// h.myService.AddUseCase(h.uceglpc)
	// h.uceglpp = eglpp.NewLPP(localEntity, nil)
	// h.myService.AddUseCase(h.uceglpp)
	h.ucmamgcp = mgcp.NewMGCP(localEntity, h.OnMGCPEvent)
	h.myService.AddUseCase(h.ucmamgcp)
	// h.uccemvabd = vabd.NewVABD(localEntity, h.OnVABDEvent)
	// h.myService.AddUseCase(h.uccemvabd)
	// h.uccemvapd = vapd.NewVAPD(localEntity, h.OnVAPDEvent)
	// h.myService.AddUseCase(h.uccemvapd)

	// Initialize local server data
	_ = h.uccslpc.SetConsumptionLimit(ucapi.LoadLimit{
		Value:        4200,
		IsChangeable: true,
		IsActive:     false,
	})
	_ = h.uccslpc.SetFailsafeConsumptionActivePowerLimit(4200, true)
	_ = h.uccslpc.SetFailsafeDurationMinimum(2*time.Hour, true)

	_ = h.uccslpp.SetProductionLimit(ucapi.LoadLimit{
		Value:        float64(cfg.InverterMax),
		IsChangeable: true,
		IsActive:     false,
	})
	_ = h.uccslpp.SetFailsafeProductionActivePowerLimit(4200, true)
	_ = h.uccslpp.SetFailsafeDurationMinimum(2*time.Hour, true)
	_ = h.uccslpp.SetProductionNominalMax(float64(cfg.InverterMax))

	if len(remoteSki) == 0 {
		fmt.Println("No Remote SKI provided, exiting.")
		os.Exit(0)
	}

	h.myService.RegisterRemoteSKI(remoteSki, "")
	// print("test ski ", h.myService.LocalService().SKI())

	h.myService.Start()
}

// Find available port
func availablePort(port int) (hemsport int) {
	startPort := port
	maxPort := startPort + 100
	found := false
	for p := startPort; p <= maxPort; p++ {
		addr := fmt.Sprintf(":%d", p)
		ln, err := net.Listen("tcp", addr)
		if err == nil {
			_ = ln.Close()
			hemsport = p
			found = true
			break
		}
	}
	if !found {
		// fallback to 0 to let the OS pick a free port
		fmt.Println("Info: no free port in range, falling back to 0 (auto).")
		port = 0
	} else {
		fmt.Println("Info: using port", hemsport)
	}
	return hemsport

}

func EKG(h *hems) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	// wait := time.Now()
	allowedproduction, _ := h.uccslpp.ProductionNominalMax()
	limitactive := false
	for range ticker.C {
		wait := time.Now()
		since := wait.Sub(ekg)
		client.Publish("eebus2mqtt/hems/lpp/last_heartbeat", 1, false, fmt.Sprintf("%.f", since.Seconds()))
		client.Publish("eebus2mqtt/hems/lpp/allowed_production", 1, false, fmt.Sprintf("%.f", allowedproduction))
		client.Publish("eebus2mqtt/hems/lpp/limit_activ", 1, false, fmt.Sprintf("%.t", limitactive))

		if since.Seconds() > 120 && !isFailsafe {
			l, _, _ := h.uccslpp.FailsafeProductionActivePowerLimit()
			allowedproduction = l
			limitactive = true
			isFailsafe = true
			go FailsafeCountdown(h)
		}
		if since.Seconds() <= 120 {
			isFailsafe = false
			limit, _ := h.uccslpp.ProductionLimit()
			if limit.IsActive {
				allowedproduction = limit.Value
				limitactive = limit.IsActive
			} else {
				allowedproduction, _ = h.uccslpp.ProductionNominalMax()
				limitactive = false
			}

		}
	}

}

func FailsafeCountdown(h *hems) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	d, _, _ := h.uccslpp.FailsafeDurationMinimum()
	// d := 20 * time.Second
	start := time.Now()
	for range ticker.C {
		wait := time.Now()
		failsafe_time := wait.Sub(start)
		if failsafe_time <= d && isFailsafe {
			cd := d - failsafe_time
			client.Publish("eebus2mqtt/hems/lpp/FailsafeCountdown", 1, false, fmt.Sprintf("%.f", cd.Seconds()))
		} else {
			client.Publish("eebus2mqtt/hems/lpp/FailsafeCountdown", 1, false, fmt.Sprintf("%.f", d.Seconds()))
			ticker.Stop()
		}
	}

}

// Controllable System LPC Event Handler
// output only mqqt for now
func (h *hems) OnLPCEvent(ski string, device spineapi.DeviceRemoteInterface, entity spineapi.EntityRemoteInterface, event api.EventType) {
	// 	switch event {
	// 	case cslpc.WriteApprovalRequired:
	// 		// get pending writes
	// 		pendingWrites := h.uccslpc.PendingConsumptionLimits()

	// 		// approve any write
	// 		for msgCounter, write := range pendingWrites {
	// 			fmt.Println("Approving LPC write with msgCounter", msgCounter, "and limit", write.Value, "W")
	// 			h.uccslpc.ApproveOrDenyConsumptionLimit(msgCounter, true, "")
	// 		}
	// 	case cslpc.DataUpdateLimit:
	// 		if currentLimit, err := h.uccslpc.ConsumptionLimit(); err == nil {
	// 			//			fmt.Println("New LPC Limit set to", currentLimit.Value, "W. Is Active:", currentLimit.IsActive)
	// 			client.Publish("eebus2mqtt/hems/lpc/limit", 1, false, fmt.Sprintf("%.f", currentLimit.Value))
	// 			client.Publish("eebus2mqtt/hems/lpc/active", 1, false, fmt.Sprintf("%t", currentLimit.IsActive))
	// 		}
	// 	case cslpc.DataUpdateHeartbeat:
	// 		// publish everything on heartbeat
	// 		cnm, _ := h.uccslpc.ConsumptionNominalMax()
	// 		client.Publish("eebus2mqtt/hems/lpc/NominalMax", 1, false, fmt.Sprintf("%.f", cnm))
	// 		if currentLimit, err := h.uccslpc.ConsumptionLimit(); err == nil {

	// 			client.Publish("eebus2mqtt/hems/lpc/limit", 1, false, fmt.Sprintf("%.f", currentLimit.Value))
	// 			client.Publish("eebus2mqtt/hems/lpc/active", 1, false, fmt.Sprintf("%t", currentLimit.IsActive))
	// 		}
	// 		if currentLimit, isChangeable, err := h.uccslpc.FailsafeConsumptionActivePowerLimit(); err == nil {
	// 			//			fmt.Println("New LPP Failsafe Production Active Power Limit set to", currentLimit, "W")
	// 			//			fmt.Println("Is Changeable:", isChangeable)
	// 			client.Publish("eebus2mqtt/hems/lpc/failsafe_limit", 1, false, fmt.Sprintf("%.f", currentLimit))
	// 			client.Publish("eebus2mqtt/hems/lpc/failsafe_changeable", 1, false, fmt.Sprintf("%t", isChangeable))
	// 		}
	// 		if duration, isChangeable, err := h.uccslpc.FailsafeDurationMinimum(); err == nil {
	// 			//			fmt.Println("New LPC Failsafe Duration Minimum set to", duration)
	// 			//			fmt.Println("Is Changeable:", isChangeable)
	// 			client.Publish("eebus2mqtt/hems/lpc/failsafe_duration", 1, false, fmt.Sprintf("%.f", duration.Seconds()))
	// 			client.Publish("eebus2mqtt/hems/lpc/failsafe_changeable", 1, false, fmt.Sprintf("%t", isChangeable))
	// 		}
	// 	case cslpc.DataUpdateFailsafeConsumptionActivePowerLimit:
	// 		if currentLimit, isChangeable, err := h.uccslpc.FailsafeConsumptionActivePowerLimit(); err == nil {
	// 			//			fmt.Println("New LPP Failsafe Production Active Power Limit set to", currentLimit, "W")
	// 			//			fmt.Println("Is Changeable:", isChangeable)
	// 			client.Publish("eebus2mqtt/hems/lpc/failsafe_limit", 1, false, fmt.Sprintf("%.f", currentLimit))
	// 			client.Publish("eebus2mqtt/hems/lpc/failsafe_changeable", 1, false, fmt.Sprintf("%t", isChangeable))
	// 		}
	// 	case cslpc.DataUpdateFailsafeDurationMinimum:
	// 		if duration, isChangeable, err := h.uccslpc.FailsafeDurationMinimum(); err == nil {
	// 			//			fmt.Println("New LPC Failsafe Duration Minimum set to", duration)
	// 			//			fmt.Println("Is Changeable:", isChangeable)
	// 			client.Publish("eebus2mqtt/hems/lpc/failsafe_duration", 1, false, fmt.Sprintf("%.f", duration.Seconds()))
	// 			client.Publish("eebus2mqtt/hems/lpc/failsafe_changeable", 1, false, fmt.Sprintf("%t", isChangeable))
	// 		}

	// 	}
}

// Controllable System LPP Event Handler

func (h *hems) OnLPPEvent(ski string, device spineapi.DeviceRemoteInterface, entity spineapi.EntityRemoteInterface, event api.EventType) {
	switch event {
	case cslpp.WriteApprovalRequired:
		// get pending writes
		pendingWrites := h.uccslpp.PendingProductionLimits()

		// approve any write
		for msgCounter, write := range pendingWrites {
			fmt.Println("Approving LPP write with msgCounter", msgCounter, "and limit", write.Value, "W")
			h.uccslpp.ApproveOrDenyProductionLimit(msgCounter, true, "")
		}
	case cslpp.DataUpdateLimit:
		if currentLimit, err := h.uccslpp.ProductionLimit(); err == nil {
			fmt.Println("New LPP Limit set to", currentLimit.Value, "W. Is Active:", currentLimit.IsActive)
			// client.Publish("eebus2mqtt/hems/lpp/limit", 1, false, fmt.Sprintf("%.f", currentLimit.Value))
			// client.Publish("eebus2mqtt/hems/lpp/active", 1, false, fmt.Sprintf("%t", currentLimit.IsActive))
		}
	case cslpp.DataUpdateHeartbeat:
		ekg = time.Now()
	// 	// publish everything on heartbeat
	// 	pnm, _ := h.uccslpp.ProductionNominalMax()
	// 	client.Publish("eebus2mqtt/hems/lpp/NominalMax", 1, false, fmt.Sprintf("%.f", pnm))
	// 	if currentLimit, err := h.uccslpp.ProductionLimit(); err == nil {
	// 		client.Publish("eebus2mqtt/hems/lpp/limit", 1, false, fmt.Sprintf("%.f", currentLimit.Value))
	// 		client.Publish("eebus2mqtt/hems/lpp/active", 1, false, fmt.Sprintf("%t", currentLimit.IsActive))
	// 	}
	// 	if currentLimit, isChangeable, err := h.uccslpp.FailsafeProductionActivePowerLimit(); err == nil {
	// 		fmt.Println("New LPP Failsafe Production Active Power Limit set to", currentLimit, "W")
	// 		fmt.Println("Is Changeable:", isChangeable)
	// 		client.Publish("eebus2mqtt/hems/lpp/failsafe_limit", 1, false, fmt.Sprintf("%.f", currentLimit))
	// 		client.Publish("eebus2mqtt/hems/lpp/failsafe_changeable", 1, false, fmt.Sprintf("%t", isChangeable))
	// 	}
	// 	if duration, isChangeable, err := h.uccslpp.FailsafeDurationMinimum(); err == nil {
	// 		fmt.Println("New LPP Failsafe Duration Minimum set to", duration)
	// 		fmt.Println("Is Changeable:", isChangeable)get
	// 		client.Publish("eebus2mqtt/hems/lpp/failsafe_duration", 1, false, fmt.Sprintf("%.f", duration.Seconds()))
	// 		client.Publish("eebus2mqtt/hems/lpp/failsafe_changeable", 1, false, fmt.Sprintf("%t", isChangeable))
	// 	}
	case cslpp.DataUpdateFailsafeProductionActivePowerLimit:
		if currentLimit, isChangeable, err := h.uccslpp.FailsafeProductionActivePowerLimit(); err == nil {
			fmt.Println("New LPP Failsafe Production Active Power Limit set to", currentLimit, "W")
			fmt.Println("Is Changeable:", isChangeable)
			client.Publish("eebus2mqtt/hems/lpp/failsafe_limit", 1, false, fmt.Sprintf("%.f", currentLimit))
			// client.Publish("eebus2mqtt/hems/lpp/failsafe_changeable", 1, false, fmt.Sprintf("%t", isChangeable))
		}
	case cslpp.DataUpdateFailsafeDurationMinimum:
		if duration, isChangeable, err := h.uccslpp.FailsafeDurationMinimum(); err == nil {
			fmt.Println("New LPP Failsafe Duration Minimum set to", duration)
			fmt.Println("Is Changeable:", isChangeable)
			client.Publish("eebus2mqtt/hems/lpp/failsafe_duration", 1, false, fmt.Sprintf("%.f", duration.Seconds()))
			// client.Publish("eebus2mqtt/hems/lpp/failsafe_changeable", 1, false, fmt.Sprintf("%t", isChangeable))
		}

	}
}

// Monitoring Appliance MGCP Event Handler

func (h *hems) OnMGCPEvent(ski string, device spineapi.DeviceRemoteInterface, entity spineapi.EntityRemoteInterface, event api.EventType) {
	switch event {
	case mgcp.DataUpdatePowerLimitationFactor:
		if factor, err := h.ucmamgcp.PowerLimitationFactor(entity); err == nil {
			fmt.Println("New MGCP Power Limitation Factor set to", factor)
		}
	case mgcp.DataUpdatePower:
		if power, err := h.ucmamgcp.Power(entity); err == nil {
			fmt.Println("New MGCP Power set to", power, "W")
		}
	case mgcp.DataUpdateEnergyFeedIn:
		if energy, err := h.ucmamgcp.EnergyFeedIn(entity); err == nil {
			fmt.Println("New MGCP Energy Feed-In set to", energy, "Wh")
		}
	case mgcp.DataUpdateEnergyConsumed:
		if energy, err := h.ucmamgcp.EnergyConsumed(entity); err == nil {
			fmt.Println("New MGCP Energy Consumed set to", energy, "Wh")
		}
	case mgcp.DataUpdateCurrentPerPhase:
		if current, err := h.ucmamgcp.CurrentPerPhase(entity); err == nil {
			fmt.Println("New MGCP Current per Phase set to", current, "A")
		}
	case mgcp.DataUpdateVoltagePerPhase:
		if voltage, err := h.ucmamgcp.VoltagePerPhase(entity); err == nil {
			fmt.Println("New MGCP Voltage per Phase set to", voltage, "V")
		}
	case mgcp.DataUpdateFrequency:
		if frequency, err := h.ucmamgcp.Frequency(entity); err == nil {
			fmt.Println("New MGCP Frequency set to", frequency, "Hz")
		}
	}
}

// EEBUSServiceHandler

func (h *hems) RemoteSKIConnected(service api.ServiceInterface, ski string) {
	time.AfterFunc(1*time.Second, func() {
		_ = h.uccslpc.SetConsumptionNominalMax(32000)
		_ = h.uccslpp.SetProductionNominalMax(10000)
		println("starte ekg")
		go EKG(h)
	})
}

func (h *hems) RemoteSKIDisconnected(service api.ServiceInterface, ski string) {}

func (h *hems) VisibleRemoteServicesUpdated(service api.ServiceInterface, entries []shipapi.RemoteService) {
}

func (h *hems) ServiceShipIDUpdate(ski string, shipdID string) {}

func (h *hems) ServicePairingDetailUpdate(ski string, detail *shipapi.ConnectionStateDetail) {
	if ski == remoteSki && detail.State() == shipapi.ConnectionStateRemoteDeniedTrust {
		fmt.Println("The remote service denied trust. Exiting.")
		h.myService.CancelPairingWithSKI(ski)
		h.myService.UnregisterRemoteSKI(ski)
		h.myService.Shutdown()
		os.Exit(0)
	}
}

func (h *hems) AllowWaitingForTrust(ski string) bool {
	return ski == remoteSki
}

// UCEvseCommisioningConfigurationCemDelegate

// handle device state updates from the remote EVSE device
func (h *hems) HandleEVSEDeviceState(ski string, failure bool, errorCode string) {
	fmt.Println("EVSE Error State:", failure, errorCode)
}

var messagePubHandler mqtt.MessageHandler = func(client mqtt.Client, msg mqtt.Message) {
	fmt.Printf("Received message: %s from topic: %s\n", msg.Payload(), msg.Topic())
}
var connectHandler mqtt.OnConnectHandler = func(client mqtt.Client) {
	fmt.Println("MQTT Connected")
	sub(client)
}

var connectLostHandler mqtt.ConnectionLostHandler = func(client mqtt.Client, err error) {
	fmt.Printf("MQTT Connect lost: %v", err)
}

func sub(client mqtt.Client) {
	topic := "eebus2mqtt/hems/#"
	token := client.Subscribe(topic, 1, nil)
	token.Wait()
	fmt.Printf("Subscribed to topic: %s\n", topic)
}

func mqttConnect() {
	var broker = config.Mqtt.Broker
	var port = config.Mqtt.Port
	opts := mqtt.NewClientOptions()
	opts.AddBroker(fmt.Sprintf("tcp://%s:%d", broker, port))
	opts.SetClientID(fmt.Sprintf("eebus2mqtt-hems-%d", time.Now().UnixNano()))
	opts.SetUsername(config.Mqtt.Username)
	opts.SetPassword(config.Mqtt.Password)
	opts.SetDefaultPublishHandler(messagePubHandler)
	opts.OnConnect = connectHandler
	opts.OnConnectionLost = connectLostHandler
	client = mqtt.NewClient(opts)
	if token := client.Connect(); token.Wait() && token.Error() != nil {
		println("MQTT failed:", token.Error())
	}
}

// main app
func main() {
	mqttConnect()
	h := hems{}
	h.run()

	// Clean exit to make sure mdns shutdown is invoked
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	// User exit
}

// Logging interface

func (h *hems) Trace(args ...interface{}) {
	if len(os.Args) > 1 && os.Args[1] == "debug" {
		h.print("TRACE", args...)
	}
}

func (h *hems) Tracef(format string, args ...interface{}) {
	if len(os.Args) > 1 && os.Args[1] == "debug" {
		h.printFormat("TRACE", format, args...)
	}
}

func (h *hems) Debug(args ...interface{}) {
	if len(os.Args) > 1 && os.Args[1] == "debug" {
		h.print("DEBUG", args...)
	}
}

func (h *hems) Debugf(format string, args ...interface{}) {
	if len(os.Args) > 1 && os.Args[1] == "debug" {
		h.printFormat("DEBUG", format, args...)
	}
}

func (h *hems) Info(args ...interface{}) {
	h.print("INFO ", args...)
}

func (h *hems) Infof(format string, args ...interface{}) {
	h.printFormat("INFO ", format, args...)
}

func (h *hems) Error(args ...interface{}) {
	h.print("ERROR", args...)
}

func (h *hems) Errorf(format string, args ...interface{}) {
	h.printFormat("ERROR", format, args...)
}

func (h *hems) currentTimestamp() string {
	return time.Now().Format("2006-01-02 15:04:05")
}

func (h *hems) print(msgType string, args ...interface{}) {
	value := fmt.Sprintln(args...)
	fmt.Printf("%s %s %s", h.currentTimestamp(), msgType, value)
}

func (h *hems) printFormat(msgType, format string, args ...interface{}) {
	value := fmt.Sprintf(format, args...)
	fmt.Println(h.currentTimestamp(), msgType, value)
}
