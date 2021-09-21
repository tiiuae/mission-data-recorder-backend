package provisioning

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/pkg/errors"
	"gopkg.in/yaml.v2"
)

type deviceSettings struct {
	DeviceID         string           `json:"device_id"`
	CommlinkSettings commlinkSettings `json:"communication_link"`
	RecorderSettings recorderSettings `json:"mission_data_recorder"`
	RtspSettings     rtspSettings     `json:"rtsp"`
}

type commlinkSettings struct {
	MqttServer   string `json:"mqtt_server"`
	MqttClientID string `json:"mqtt_client_id"`
	MqttAudience string `json:"mqtt_audience"`
}

type recorderSettings struct {
	Audience      string `json:"audience"`
	BackendURL    string `json:"backend_url"`
	Topics        string `json:"topics"`
	DestDir       string `json:"dest_dir"`
	SizeThreshold int    `json:"size_threshold"`
	ExtraArgs     string `json:"extra_args"`
}

type rtspSettings struct {
	RtspServer string `json:"rtsp_server"`
}

type commlinkYaml struct {
	DeviceID     string `yaml:"device_id"`
	MqttServer   string `yaml:"mqtt_server"`
	MqttClientID string `yaml:"mqtt_client_id"`
	MqttAudience string `yaml:"mqtt_audience"`
}

type recorderYaml struct {
	DeviceID      string `yaml:"device_id"`
	Audience      string `yaml:"audience"`
	BackendURL    string `yaml:"backend_url"`
	Topics        string `yaml:"topics"`
	DestDir       string `yaml:"dest_dir"`
	SizeThreshold int    `yaml:"size_threshold"`
	ExtraArgs     string `yaml:"extra_args"`
}

func CreateDroneStandalone(deviceName string, recordTopics []string, recordSizeThreshold int, rtspServer string) (*ProvisioningSettings, error) {
	settings := deviceSettings{
		DeviceID: deviceName,
		CommlinkSettings: commlinkSettings{
			MqttServer:   "tcp://mqtt-server-svc:8883",
			MqttClientID: fmt.Sprintf("projects/auto-fleet-mgnt/locations/europe-west1/registries/fleet-registry/devices/%s", deviceName),
			MqttAudience: "auto-fleet-mgnt",
		},
		RecorderSettings: recorderSettings{
			Audience:      "auto-fleet-mgnt",
			BackendURL:    "http://mission-data-recorder-backend-svc",
			Topics:        strings.Join(recordTopics, ","),
			DestDir:       "/fog-drone/mission-data",
			SizeThreshold: recordSizeThreshold,
			ExtraArgs:     "",
		},
		RtspSettings: rtspSettings{
			RtspServer: rtspServer,
		},
	}

	return parseSettings(settings)
}

func parseInitData(initData []byte) (*ProvisioningSettings, error) {
	var settings deviceSettings
	err := json.Unmarshal(initData, &settings)
	if err != nil {
		return nil, err
	}

	return parseSettings(settings)
}

func parseSettings(settings deviceSettings) (*ProvisioningSettings, error) {
	var err error
	var res ProvisioningSettings

	key, cert, err := createKeys()
	if err != nil {
		return nil, err
	}

	res.PrivateKey = key
	res.PublicCert = cert
	res.CommlinkYaml, err = parseCommunicationLinkConfig(settings)
	if err != nil {
		return nil, err
	}
	res.RecorderYaml, err = parseRecorderConfig(settings)
	if err != nil {
		return nil, err
	}
	res.FogBash, err = parseFogEnvBash(settings)
	if err != nil {
		return nil, err
	}

	return &res, nil
}

func parseCommunicationLinkConfig(request deviceSettings) (string, error) {
	config := commlinkYaml{
		request.DeviceID,
		request.CommlinkSettings.MqttServer,
		request.CommlinkSettings.MqttClientID,
		request.CommlinkSettings.MqttAudience,
	}

	yamlBytes, err := yaml.Marshal(config)
	if err != nil {
		return "", errors.WithMessagef(err, "Could not marshal configuration")
	}

	return string(yamlBytes), nil
}

func parseRecorderConfig(request deviceSettings) (string, error) {
	config := recorderYaml{
		request.DeviceID,
		request.RecorderSettings.Audience,
		request.RecorderSettings.BackendURL,
		request.RecorderSettings.Topics,
		request.RecorderSettings.DestDir,
		request.RecorderSettings.SizeThreshold,
		request.RecorderSettings.ExtraArgs,
	}

	yamlBytes, err := yaml.Marshal(config)
	if err != nil {
		return "", errors.WithMessagef(err, "Could not marshal configuration")
	}

	return string(yamlBytes), nil
}

func parseFogEnvBash(request deviceSettings) (string, error) {
	script := fmt.Sprintf(
		`DRONE_DEVICE_ID=%s
RTSP_SERVER_ADDRESS=%s
`,
		request.DeviceID,
		request.RtspSettings.RtspServer)

	return script, nil
}

func createKeys() ([]byte, []byte, error) {
	privateKey := make([]byte, 0)
	publicCert := make([]byte, 0)

	// Generate key
	privatekey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, err
	}
	publickey := &privatekey.PublicKey

	// Save private key
	var privateKeyBytes []byte = x509.MarshalPKCS1PrivateKey(privatekey)
	privateKeyBlock := &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: privateKeyBytes,
	}

	privateKey = pem.EncodeToMemory(privateKeyBlock)

	// Create x509 certificate
	template := &x509.Certificate{
		IsCA:                  true,
		BasicConstraintsValid: true,
		SubjectKeyId:          []byte{1, 2, 3},
		SerialNumber:          big.NewInt(1234),
		Subject: pkix.Name{
			Country:      []string{"Finland"},
			Organization: []string{"Solita"},
		},
		NotBefore:   time.Now(),
		NotAfter:    time.Now().AddDate(5, 5, 5),
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
	}
	var parent = template
	cert, err := x509.CreateCertificate(rand.Reader, template, parent, publickey, privatekey)
	if err != nil {
		fmt.Println(err)
	}

	var pemkey = &pem.Block{
		Type:  "CERTIFICATE",
		Bytes: cert,
	}

	publicCert = pem.EncodeToMemory(pemkey)

	return privateKey, publicCert, nil
}
