package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v4"
	"google.golang.org/api/cloudiot/v1"
)

// This is set to another function in tests to provide deterministic results.
var timeNow = time.Now

type gcpAPI interface {
	GetDeviceCredentials(ctx context.Context, tenantID, deviceID string) ([]*cloudiot.DeviceCredential, error)
}

type gcpConfig struct {
	ProjectID  string `config:"projectId"`
	Region     string `config:"region"`
	iotService *cloudiot.Service
}

func (g *gcpConfig) GetDeviceCredentials(
	ctx context.Context,
	tenantID string,
	deviceID string,
) ([]*cloudiot.DeviceCredential, error) {
	device, err := g.iotService.Projects.Locations.Registries.Devices.Get(
		fmt.Sprintf(
			"projects/%s/locations/%s/registries/%s/devices/%s",
			g.ProjectID, g.Region, tenantID, deviceID,
		),
	).Context(ctx).FieldMask("credentials").Do()
	if err != nil {
		return nil, err
	}
	return device.Credentials, nil
}

type invalidTokenError struct {
	Err error
}

func (err invalidTokenError) Error() string {
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
		return nil, fmt.Errorf("failed to parse public key: %w", err)
	}
	if alg != keyAlgorithm {
		return nil, nil
	}
	return pubKey, nil
}

type jwtClaims struct {
	DeviceID string `json:"deviceId"`
	TenantID string `json:"tenantId"`
	BagName  string `json:"bagName"`
	jwt.RegisteredClaims
}

func validateJWT(ctx context.Context, gcp gcpAPI, defaulTenantID, rawToken string) (*jwtClaims, error) {
	// Automatic validation makes unit testing harder so it is skipped and
	// manual validation is used instead.
	parser := jwt.Parser{SkipClaimsValidation: true}
	var claims jwtClaims
	token, err := parser.ParseWithClaims(
		rawToken,
		&claims,
		func(t *jwt.Token) (interface{}, error) {
			if claims.TenantID == "" {
				claims.TenantID = defaulTenantID
			}
			now := timeNow()
			if !claims.VerifyExpiresAt(now, true) {
				return nil, invalidTokenError{fmt.Errorf("expired at %v", claims.ExpiresAt)}
			}
			if !claims.VerifyIssuedAt(now, true) {
				return nil, invalidTokenError{fmt.Errorf("invalid issue date: %v", claims.IssuedAt)}
			}
			creds, err := gcp.GetDeviceCredentials(ctx, claims.TenantID, claims.DeviceID)
			if err != nil {
				return nil, invalidTokenError{err}
			}
			for i, cred := range creds {
				pubKey, err := validateDeviceCredential(cred, t.Method.Alg())
				if err != nil {
					logWarnf(
						"a non-fatal error occurred when validating credential number %d for device '%s/%s': %s",
						i, claims.TenantID, claims.DeviceID, err.Error(),
					)
				} else if pubKey != nil {
					return pubKey, nil
				}
			}
			return nil, invalidTokenError{fmt.Errorf("unauthorized device: %s/%s", claims.TenantID, claims.DeviceID)}
		},
	)
	if !token.Valid {
		return nil, fmt.Errorf("failed to validate token: %w", err)
	}
	return &claims, nil
}

func getClaimsWithoutValidation(rawToken string) (*jwtClaims, error) {
	var claims jwtClaims
	_, _, err := (&jwt.Parser{}).ParseUnverified(rawToken, &claims)
	if err != nil {
		return nil, err
	}
	return &claims, nil
}
