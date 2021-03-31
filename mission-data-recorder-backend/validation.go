package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/dgrijalva/jwt-go"
	"google.golang.org/api/cloudiot/v1"
)

// This is set to another function in tests to provide deterministic results.
var timeNow = time.Now

type gcpAPI interface {
	GetProjectID() string
	GetDeviceCredentials(ctx context.Context, deviceID string) ([]*cloudiot.DeviceCredential, error)
}

type gcpConfig struct {
	ProjectID  string `yaml:"projectId"`
	Region     string `yaml:"region"`
	RegistryID string `yaml:"registryId"`
	iotService *cloudiot.Service
}

func (g *gcpConfig) GetProjectID() string {
	return g.ProjectID
}

func (g *gcpConfig) GetDeviceCredentials(
	ctx context.Context,
	deviceID string,
) ([]*cloudiot.DeviceCredential, error) {
	device, err := g.iotService.Projects.Locations.Registries.Devices.Get(
		fmt.Sprintf(
			"projects/%s/locations/%s/registries/%s/devices/%s",
			g.ProjectID, g.Region, g.RegistryID, deviceID,
		),
	).Context(ctx).FieldMask("credentials").Do()
	if err != nil {
		return nil, err
	}
	return device.Credentials, nil
}

type invalidTokenErr struct {
	Err error
}

func (err invalidTokenErr) Error() string {
	if err.Err == nil {
		return "invalid token"
	}
	return "invalid token: " + err.Err.Error()
}

func parsePublicKey(
	rawkey *cloudiot.PublicKeyCredential,
) (key interface{}, alg string, err error) {
	switch rawkey.Format {
	case "RSA_X509_PEM":
		key, err := jwt.ParseRSAPublicKeyFromPEM([]byte(rawkey.Key))
		return key, "RS256", err
	case "ES256_X509_PEM":
		key, err := jwt.ParseECPublicKeyFromPEM([]byte(rawkey.Key))
		return key, "ES256", err
	default:
		return nil, "", errors.New("unsupported format: " + rawkey.Format)
	}
}

func validateDeviceCredential(
	cred *cloudiot.DeviceCredential,
	keyAlgorithm string,
) (pubKey interface{}, err error) {
	expires, err := time.Parse(time.RFC3339, cred.ExpirationTime)
	if err != nil {
		return nil, errors.New("expiry time is invalid: " + cred.ExpirationTime)
	}
	// A non-expiring credential has an expiry time equal to Unix zero time.
	if expires.Unix() != 0 && timeNow().After(expires) {
		return nil, errors.New("expired at " + expires.String())
	}
	pubKey, alg, err := parsePublicKey(cred.PublicKey)
	if err != nil {
		return nil, err
	}
	if alg != keyAlgorithm {
		return nil, nil
	}
	return pubKey, nil
}

type jwtClaims struct {
	DeviceID string `json:"deviceId"`
	jwt.StandardClaims
}

func validateJWT(ctx context.Context, gcp gcpAPI, rawToken string) (string, error) {
	var deviceID string
	// Automatic validation makes unit testing harder so it is skipped and
	// manual validation is used instead.
	parser := jwt.Parser{SkipClaimsValidation: true}
	token, err := parser.ParseWithClaims(
		rawToken,
		&jwtClaims{},
		func(t *jwt.Token) (interface{}, error) {
			claims, ok := t.Claims.(*jwtClaims)
			if !ok {
				return nil, invalidTokenErr{errors.New("invalid claims")}
			}
			deviceID = claims.DeviceID
			now := timeNow().Unix()
			if !claims.VerifyAudience(gcp.GetProjectID(), true) {
				return nil, invalidTokenErr{fmt.Errorf("invalid audience: %s", claims.Audience)}
			}
			if !claims.VerifyExpiresAt(now, true) {
				return nil, invalidTokenErr{fmt.Errorf("expired at %d", claims.ExpiresAt)}
			}
			if !claims.VerifyIssuedAt(now, true) {
				return nil, invalidTokenErr{fmt.Errorf("invalid issue date: %d", claims.IssuedAt)}
			}
			creds, err := gcp.GetDeviceCredentials(ctx, deviceID)
			if err != nil {
				return nil, invalidTokenErr{err}
			}
			for i, cred := range creds {
				pubKey, err := validateDeviceCredential(cred, t.Method.Alg())
				if err != nil {
					log.Printf(
						"a non-fatal error occurred when validating credential number %d for device '%s': %s",
						i, deviceID, err.Error(),
					)
				} else if pubKey != nil {
					return pubKey, nil
				}
			}
			return nil, invalidTokenErr{errors.New("unauthorized device: " + deviceID)}
		},
	)
	if !token.Valid {
		return "", err
	}
	return deviceID, nil
}
