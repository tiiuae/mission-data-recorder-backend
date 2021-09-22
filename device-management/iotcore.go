package main

import (
	"fmt"

	"google.golang.org/api/cloudiot/v1"
)

func listIotDevices(client *cloudiot.Service, tenantID string) ([]deviceInfo, error) {
	call := client.Projects.Locations.Registries.Devices.List(registryPath(tenantID))
	call.FieldMask("lastHeartbeatTime,lastEventTime,lastStateTime,metadata")
	devices, err := call.Do()
	if err != nil {
		return nil, err
	}

	result := make([]deviceInfo, 0)

	for _, device := range devices.Devices {
		result = append(result, deviceInfo{device.Id})
	}

	return result, nil
}

func deleteIotDevice(client *cloudiot.Service, tenantID string, deviceID string) error {
	call := client.Projects.Locations.Registries.Devices.Delete(fmt.Sprintf("%s/devices/%s", registryPath(tenantID), deviceID))
	_, err := call.Do()

	return err
}

func createIotDevice(client *cloudiot.Service, tenantID string, deviceID string, certificate []byte) error {
	cfg := defaultConfiguration()
	cfgStr, err := serializeConfig(cfg)
	if err != nil {
		return err
	}

	newDevice := &cloudiot.Device{
		Blocked: false,
		Config:  &cloudiot.DeviceConfig{BinaryData: cfgStr},
		Credentials: []*cloudiot.DeviceCredential{
			{
				ExpirationTime: "",
				PublicKey: &cloudiot.PublicKeyCredential{
					Format: "RSA_X509_PEM",
					Key:    string(certificate),
				},
			},
		},
		GatewayConfig: nil,
		Id:            deviceID,
	}

	call := client.Projects.Locations.Registries.Devices.Create(registryPath(tenantID), newDevice)
	_, err = call.Do()

	return err
}
