package provisioning

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"

	"github.com/pkg/errors"
)

type ProvisioningSettings struct {
	PrivateKey   []byte
	PublicCert   []byte
	CommlinkYaml string
	RecorderYaml string
	FogBash      string
}

func CreateTenant(tenantID string, header http.Header) error {
	baseURL := "https://devices.webapi.sacplatform.com"

	url := fmt.Sprintf("%s/tenants/%s", baseURL, tenantID)
	req, err := http.NewRequest("POST", url, nil)
	if err != nil {
		return err
	}
	req.Header = header
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}

	if resp.StatusCode != http.StatusCreated {
		return errors.Errorf("%s", resp.Status)
	}

	return nil
}

func DeleteTenant(tenantID string, header http.Header) error {
	baseURL := "https://devices.webapi.sacplatform.com"

	url := fmt.Sprintf("%s/tenants/%s", baseURL, tenantID)
	req, err := http.NewRequest("DELETE", url, nil)
	if err != nil {
		return err
	}
	req.Header = header
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}

	if resp.StatusCode != http.StatusNoContent {
		return errors.Errorf("%s", resp.Status)
	}

	return nil
}

func CreateDrone(tenantID string, deviceName string, header http.Header) (*ProvisioningSettings, error) {
	baseURL := "https://devices.webapi.sacplatform.com"

	initData, err := devicesGetInitData(baseURL, tenantID, deviceName, header)
	if err != nil {
		return nil, err
	}

	settings, err := parseInitData(initData)
	if err != nil {
		return nil, err
	}

	err = devicesPostCert(baseURL, tenantID, deviceName, settings.PublicCert, header)
	if err != nil {
		return nil, err
	}

	return settings, nil
}

func devicesGetInitData(baseURL string, tenantID string, deviceName string, header http.Header) ([]byte, error) {
	url := fmt.Sprintf("%s/device-settings/%s?tid=%s", baseURL, deviceName, tenantID)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header = header
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	return body, nil
}

func devicesPostCert(baseURL string, tenantID string, deviceName string, certData []byte, header http.Header) error {
	request := struct {
		DeviceID    string `json:"device_id"`
		Certificate []byte `json:"certificate"`
	}{
		deviceName,
		certData,
	}

	jsonBytes, err := json.Marshal(request)
	if err != nil {
		return err
	}

	r := bytes.NewReader(jsonBytes)
	url := fmt.Sprintf("%s/devices?tid=%s", baseURL, tenantID)
	req, err := http.NewRequest("POST", url, r)
	if err != nil {
		return err
	}
	req.Header = header
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}

	if resp.StatusCode != http.StatusCreated {
		return errors.Errorf("%s", resp.Status)
	}

	return nil
}
