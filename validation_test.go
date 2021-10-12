package main

import (
	"context"
	"errors"
	"log"
	"os"
	"testing"
	"time"

	"github.com/dgrijalva/jwt-go"
	"github.com/stretchr/testify/assert"
	"google.golang.org/api/cloudiot/v1"
)

func TestMain(m *testing.M) {
	timeNow = func() time.Time {
		t, err := time.Parse("2006-01-02 15:04", "2021-03-26 11:26")
		if err != nil {
			panic(err)
		}
		return t
	}
	log.SetOutput(os.Stdout)
	os.Exit(m.Run())
}

func TestValidateCredential(t *testing.T) {
	testCases := []struct {
		desc  string
		alg   string
		valid bool
		cred  cloudiot.DeviceCredential
	}{{
		desc:  "valid RSA_X509_PEM",
		alg:   "RS256",
		valid: true,
		cred: cloudiot.DeviceCredential{
			ExpirationTime: time.Unix(0, 0).Format(time.RFC3339),
			PublicKey: &cloudiot.PublicKeyCredential{
				Format: "RSA_X509_PEM",
				Key: `-----BEGIN CERTIFICATE-----
MIICnjCCAYYCCQDNlT9aSLGSlDANBgkqhkiG9w0BAQsFADARMQ8wDQYDVQQDDAZ1
bnVzZWQwHhcNMjEwMzI1MTQ1ODI1WhcNMjEwNDI0MTQ1ODI1WjARMQ8wDQYDVQQD
DAZ1bnVzZWQwggEiMA0GCSqGSIb3DQEBAQUAA4IBDwAwggEKAoIBAQDRv5rVOBbD
JuNKlR4JK9zW0+kn1W+/r7SnjzkWyv0/tJlFMmXHC65fSKgyZmE4sGWkfmUtJ0jT
5uIWqX3EhZ9cinBeEeZQT2Q0jXGdB6d+V4ymcFfsHsRUaLOZJrJrUiQ4nPUPfScA
lBgnMoDHefLwFiwbA+JOWWPT+kVfP2j0IkbmklhqW/gXJIYoRWd/kf0tJj/zBBFi
TaNB/bZvAAIlLmN57jMwqMhocXnXuZlObYZzMeWLCVqN0lSzv4anrL9ggxHb0XNy
zpmHXk+D5N/WNvNfvk23ibLQ6XJRPZGSKYH3YFeAFjBaNF7jkCjjG0JGRbAN4r0C
TyYThDNzUP+7AgMBAAEwDQYJKoZIhvcNAQELBQADggEBACaTOWVouFxoAy4AQ+j0
9VZiiEeNDCtFDviW+n+zMfc14WNsKJ3Iejgd2FKicK/MAQr+GLEGc34MXHnisKkZ
wWDbaiGQ7/MzVT7PkKAc/iawQCobBm6tSvv7Ajd3a7wEM82v7iBWTph1msJpdsS5
bw7T8/WUGGTTLcOJacgoHB607KDtU4hp+5vqhkm02BLyWxMGxMjOtgKbtIo/6i6Y
pAgY1r/cpJX1CMUxrEtUQjWK2hN+wIh5xpk9+SW39xEBlLmWRpqtQqvDGZ9rQESC
LcGHp2ydpEaGPe8Ue2zu0gAbz9j8dvPXbafrrGoVenR13RjXc0TqKo9733BKfNsB
gZo=
-----END CERTIFICATE-----
`,
			},
		},
	}, {
		desc:  "valid ES256_X509_PEM",
		alg:   "ES256",
		valid: true,
		cred: cloudiot.DeviceCredential{
			ExpirationTime: timeNow().Add(3 * time.Hour).Format(time.RFC3339),
			PublicKey: &cloudiot.PublicKeyCredential{
				Format: "ES256_X509_PEM",
				Key: `-----BEGIN CERTIFICATE-----
MIIBETCBuAIJAKdL4R/jQjJzMAoGCCqGSM49BAMCMBExDzANBgNVBAMMBnVudXNl
ZDAeFw0yMTAzMjYwODA1MjhaFw0yMTA0MjUwODA1MjhaMBExDzANBgNVBAMMBnVu
dXNlZDBZMBMGByqGSM49AgEGCCqGSM49AwEHA0IABCfIe3oJBIO692y2fg13dMPo
AkUpiVqDNuHsJvLoapJ7hAUmjG9C9lM4Wp6yF/6nHCaEmSBOkD/6Zde0AUB2zG4w
CgYIKoZIzj0EAwIDSAAwRQIgQKJvL+i+23DrZqussgSc1XxEwfKCtt0tnhdtk2X0
nbECIQCWeaUHLBAKxiRXiqfk+JNZvFrkKdFQFk77/x3ADrAjfw==
-----END CERTIFICATE-----
`,
			},
		},
	}, {
		desc:  "invalid ES256_X509_PEM",
		alg:   "ES256",
		valid: false,
		cred: cloudiot.DeviceCredential{
			ExpirationTime: time.Unix(0, 0).Format(time.RFC3339),
			PublicKey: &cloudiot.PublicKeyCredential{
				Format: "ES256_X509_PEM",
				Key: `-----BEGIN CERTIFICATE-----
MIIBETCBuAIJAKdL4R/jQjJzMAoGCCqGSM49BAMCMBExDzANBgNVBAMMBnVudXNl
ZDAeFw0yMTAzMjYwODA1MjhaFw0yMTA0MjUwODA1MjhaMBExDzANBgNVBAMMBnVu
dXNlZDBZMBMGByqGSM49AgEGCCq9AwEHA0IABCfIe3oJBIO692y2fg13dMPo
AkUpiVqDNuHsJvLoapJ7hAUmjG9C9lM4Wp6yF/6nHCaEmSBOkD/6Zde0AUB2zG4w
CgYIKoZIzj0EAwIDSAAwRQIgQKJvL+i+23DrZqussgSc1XxEwfKCtt0tnhdtk2X0
nbECIQCWeaUHLBAKxiRXiqfk+JNZvFrkKdFQFk77/x3ADrAjfw==
-----END CERTIFICATE-----
`,
			},
		},
	}, {
		desc:  "expired RSA_X509_PEM",
		alg:   "RS256",
		valid: false,
		cred: cloudiot.DeviceCredential{
			ExpirationTime: timeNow().Add(-2 * time.Hour).Format(time.RFC3339),
			PublicKey: &cloudiot.PublicKeyCredential{
				Format: "RSA_X509_PEM",
				Key: `-----BEGIN CERTIFICATE-----
MIICnjCCAYYCCQDNlT9aSLGSlDANBgkqhkiG9w0BAQsFADARMQ8wDQYDVQQDDAZ1
bnVzZWQwHhcNMjEwMzI1MTQ1ODI1WhcNMjEwNDI0MTQ1ODI1WjARMQ8wDQYDVQQD
DAZ1bnVzZWQwggEiMA0GCSqGSIb3DQEBAQUAA4IBDwAwggEKAoIBAQDRv5rVOBbD
JuNKlR4JK9zW0+kn1W+/r7SnjzkWyv0/tJlFMmXHC65fSKgyZmE4sGWkfmUtJ0jT
5uIWqX3EhZ9cinBeEeZQT2Q0jXGdB6d+V4ymcFfsHsRUaLOZJrJrUiQ4nPUPfScA
lBgnMoDHefLwFiwbA+JOWWPT+kVfP2j0IkbmklhqW/gXJIYoRWd/kf0tJj/zBBFi
TaNB/bZvAAIlLmN57jMwqMhocXnXuZlObYZzMeWLCVqN0lSzv4anrL9ggxHb0XNy
zpmHXk+D5N/WNvNfvk23ibLQ6XJRPZGSKYH3YFeAFjBaNF7jkCjjG0JGRbAN4r0C
TyYThDNzUP+7AgMBAAEwDQYJKoZIhvcNAQELBQADggEBACaTOWVouFxoAy4AQ+j0
9VZiiEeNDCtFDviW+n+zMfc14WNsKJ3Iejgd2FKicK/MAQr+GLEGc34MXHnisKkZ
wWDbaiGQ7/MzVT7PkKAc/iawQCobBm6tSvv7Ajd3a7wEM82v7iBWTph1msJpdsS5
bw7T8/WUGGTTLcOJacgoHB607KDtU4hp+5vqhkm02BLyWxMGxMjOtgKbtIo/6i6Y
pAgY1r/cpJX1CMUxrEtUQjWK2hN+wIh5xpk9+SW39xEBlLmWRpqtQqvDGZ9rQESC
LcGHp2ydpEaGPe8Ue2zu0gAbz9j8dvPXbafrrGoVenR13RjXc0TqKo9733BKfNsB
gZo=
-----END CERTIFICATE-----
`,
			},
		},
	}, {
		desc:  "mismatching algorithms",
		alg:   "ES256",
		valid: false,
		cred: cloudiot.DeviceCredential{
			ExpirationTime: time.Unix(0, 0).Format(time.RFC3339),
			PublicKey: &cloudiot.PublicKeyCredential{
				Format: "RSA_X509_PEM",
				Key: `-----BEGIN CERTIFICATE-----
MIICnjCCAYYCCQDNlT9aSLGSlDANBgkqhkiG9w0BAQsFADARMQ8wDQYDVQQDDAZ1
bnVzZWQwHhcNMjEwMzI1MTQ1ODI1WhcNMjEwNDI0MTQ1ODI1WjARMQ8wDQYDVQQD
DAZ1bnVzZWQwggEiMA0GCSqGSIb3DQEBAQUAA4IBDwAwggEKAoIBAQDRv5rVOBbD
JuNKlR4JK9zW0+kn1W+/r7SnjzkWyv0/tJlFMmXHC65fSKgyZmE4sGWkfmUtJ0jT
5uIWqX3EhZ9cinBeEeZQT2Q0jXGdB6d+V4ymcFfsHsRUaLOZJrJrUiQ4nPUPfScA
lBgnMoDHefLwFiwbA+JOWWPT+kVfP2j0IkbmklhqW/gXJIYoRWd/kf0tJj/zBBFi
TaNB/bZvAAIlLmN57jMwqMhocXnXuZlObYZzMeWLCVqN0lSzv4anrL9ggxHb0XNy
zpmHXk+D5N/WNvNfvk23ibLQ6XJRPZGSKYH3YFeAFjBaNF7jkCjjG0JGRbAN4r0C
TyYThDNzUP+7AgMBAAEwDQYJKoZIhvcNAQELBQADggEBACaTOWVouFxoAy4AQ+j0
9VZiiEeNDCtFDviW+n+zMfc14WNsKJ3Iejgd2FKicK/MAQr+GLEGc34MXHnisKkZ
wWDbaiGQ7/MzVT7PkKAc/iawQCobBm6tSvv7Ajd3a7wEM82v7iBWTph1msJpdsS5
bw7T8/WUGGTTLcOJacgoHB607KDtU4hp+5vqhkm02BLyWxMGxMjOtgKbtIo/6i6Y
pAgY1r/cpJX1CMUxrEtUQjWK2hN+wIh5xpk9+SW39xEBlLmWRpqtQqvDGZ9rQESC
LcGHp2ydpEaGPe8Ue2zu0gAbz9j8dvPXbafrrGoVenR13RjXc0TqKo9733BKfNsB
gZo=
-----END CERTIFICATE-----
`,
			},
		},
	}, {
		desc:  "unsupported credential algorithm",
		alg:   "RS256",
		valid: false,
		cred: cloudiot.DeviceCredential{
			ExpirationTime: time.Unix(0, 0).Format(time.RFC3339),
			PublicKey: &cloudiot.PublicKeyCredential{
				Format: "ES256_X509_PEM",
				Key: `-----BEGIN CERTIFICATE-----
MIIBETCBuAIJAKdL4R/jQjJzMAoGCCqGSM49BAMCMBExDzANBgNVBAMMBnVudXNl
ZDAeFw0yMTAzMjYwODA1MjhaFw0yMTA0MjUwODA1MjhaMBExDzANBgNVBAMMBnVu
dXNlZDBZMBMGByqGSM49AgEGCCqGSM49AwEHA0IABCfIe3oJBIO692y2fg13dMPo
AkUpiVqDNuHsJvLoapJ7hAUmjG9C9lM4Wp6yF/6nHCaEmSBOkD/6Zde0AUB2zG4w
CgYIKoZIzj0EAwIDSAAwRQIgQKJvL+i+23DrZqussgSc1XxEwfKCtt0tnhdtk2X0
nbECIQCWeaUHLBAKxiRXiqfk+JNZvFrkKdFQFk77/x3ADrAjfw==
-----END CERTIFICATE-----
`,
			},
		},
	}}
	for _, tC := range testCases {
		t.Run(tC.desc, func(t *testing.T) {
			key, err := validateDeviceCredential(&tC.cred, tC.alg)
			if tC.valid && (key == nil || err != nil) {
				t.Fatal("should be valid, returned", key, err)
			} else if !tC.valid && key != nil {
				t.Fatal("should be invalid, returned", key, err)
			}
		})
	}
}

type gcpConfigTest struct {
	projectID   string
	credentials map[string][]*cloudiot.DeviceCredential
}

func (c *gcpConfigTest) GetProjectID() string {
	return c.projectID
}

func (c *gcpConfigTest) GetDeviceCredentials(ctx context.Context, deviceID string) ([]*cloudiot.DeviceCredential, error) {
	creds, ok := c.credentials[deviceID]
	if ok {
		return creds, nil
	}
	return nil, errors.New("unknown device: " + deviceID)
}

func panicOnErr(x interface{}, err error) interface{} {
	if err != nil {
		panic(err)
	}
	return x
}

func TestValidateJWT(t *testing.T) {
	publicKey := `-----BEGIN CERTIFICATE-----
MIIBEDCBuAIJANWAlyQ8yP7RMAoGCCqGSM49BAMCMBExDzANBgNVBAMMBnVudXNl
ZDAeFw0yMTAzMzAxNDAyNThaFw0yMTA0MjkxNDAyNThaMBExDzANBgNVBAMMBnVu
dXNlZDBZMBMGByqGSM49AgEGCCqGSM49AwEHA0IABNxuJPsAwun7FeKsGHndHxUP
1vKyB9SQMEgC1EYAg7UoPL0y3uVgNnlQa6ktFgOrOR4MUl8gOxkc8DiGq1eNousw
CgYIKoZIzj0EAwIDRwAwRAIgUlf1AwPjxK2d5WuzN/0WguYYmcOd8bCesUazCbxo
MDkCIHjGq+T9KwYqkeP/kKyu2cD0gTvvmB1xdzRXuyje1Q6/
-----END CERTIFICATE-----`
	privateKey := panicOnErr(
		jwt.ParseECPrivateKeyFromPEM(
			[]byte(`-----BEGIN EC PRIVATE KEY-----
MHcCAQEEIPi+9y73Z1Vf44vkeW8jSSyts7F48z8iWaFJjSBKnblmoAoGCCqGSM49
AwEHoUQDQgAE3G4k+wDC6fsV4qwYed0fFQ/W8rIH1JAwSALURgCDtSg8vTLe5WA2
eVBrqS0WA6s5HgxSXyA7GRzwOIarV42i6w==
-----END EC PRIVATE KEY-----`),
		),
	)
	publicKeyWithoutPrivateKey := `-----BEGIN CERTIFICATE-----
MIIBETCBuAIJAKdL4R/jQjJzMAoGCCqGSM49BAMCMBExDzANBgNVBAMMBnVudXNl
ZDAeFw0yMTAzMjYwODA1MjhaFw0yMTA0MjUwODA1MjhaMBExDzANBgNVBAMMBnVu
dXNlZDBZMBMGByqGSM49AgEGCCq9AwEHA0IABCfIe3oJBIO692y2fg13dMPo
AkUpiVqDNuHsJvLoapJ7hAUmjG9C9lM4Wp6yF/6nHCaEmSBOkD/6Zde0AUB2zG4w
CgYIKoZIzj0EAwIDSAAwRQIgQKJvL+i+23DrZqussgSc1XxEwfKCtt0tnhdtk2X0
nbECIQCWeaUHLBAKxiRXiqfk+JNZvFrkKdFQFk77/x3ADrAjfw==
-----END CERTIFICATE-----`
	gcp := &gcpConfigTest{
		projectID: "test-project",
		credentials: map[string][]*cloudiot.DeviceCredential{
			"existing": {
				{
					ExpirationTime: time.Unix(0, 0).Format(time.RFC3339),
					PublicKey: &cloudiot.PublicKeyCredential{
						Format: "ES256_X509_PEM",
						Key:    publicKey,
					},
				},
			},
			"another existing": {
				{
					ExpirationTime: time.Unix(0, 0).Format(time.RFC3339),
					PublicKey: &cloudiot.PublicKeyCredential{
						Format: "ES256_X509_PEM",
						Key:    publicKeyWithoutPrivateKey,
					},
				},
			},
		},
	}
	signingMethod := jwt.GetSigningMethod("ES256")
	newToken := func(id string, expires *time.Time) string {
		if expires == nil {
			t := timeNow().Add(time.Second)
			expires = &t
		}
		token := jwt.NewWithClaims(signingMethod, &jwtClaims{
			DeviceID: id,
			BagName:  "testbag.db3",
			StandardClaims: jwt.StandardClaims{
				Audience:  gcp.projectID,
				ExpiresAt: expires.Unix(),
				IssuedAt:  timeNow().Add(-time.Minute).Unix(),
			},
		})
		s, err := token.SignedString(privateKey)
		if err != nil {
			panic(err)
		}
		return s
	}
	bg := context.Background()

	t.Run("valid token", func(t *testing.T) {
		claims, err := validateJWT(bg, gcp, newToken("existing", nil))
		assert.NoError(t, err)
		assert.Equal(t, claims.DeviceID, "existing")
		assert.Equal(t, claims.BagName, "testbag.db3")
	})
	t.Run("invalid token", func(t *testing.T) {
		token := []byte(newToken("existing", nil))
		token[3] = 2
		claims, err := validateJWT(bg, gcp, string(token))
		assert.Error(t, err)
		assert.Nil(t, claims)
	})
	t.Run("nonexistent device", func(t *testing.T) {
		claims, err := validateJWT(bg, gcp, newToken("nonexistent", nil))
		assert.Error(t, err)
		assert.Nil(t, claims)
	})
	t.Run("expired token", func(t *testing.T) {
		expires := timeNow().Add(-time.Second)
		claims, err := validateJWT(bg, gcp, newToken("existing", &expires))
		if assert.Error(t, err) {
			assert.Contains(t, err.Error(), "expired")
		}
		assert.Nil(t, claims)
	})
	t.Run("no valid key", func(t *testing.T) {
		claims, err := validateJWT(bg, gcp, newToken("another existing", nil))
		if assert.Error(t, err) {
			assert.Contains(t, err.Error(), "unauthorized device")
		}
		assert.Nil(t, claims)
	})
}
