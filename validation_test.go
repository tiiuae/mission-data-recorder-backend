package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dgrijalva/jwt-go"
	"github.com/stretchr/testify/assert"
	"google.golang.org/api/cloudiot/v1"
)

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
	projectID     string
	rawPrivateKey []byte
	privateKey    interface{}
	credentials   map[string][]*cloudiot.DeviceCredential
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

func (c *gcpConfigTest) newTestToken(id, name string, expires *time.Time) string {
	signingMethod := jwt.GetSigningMethod("RS256")
	if expires == nil {
		t := timeNow().Add(time.Second)
		expires = &t
	}
	type noBagNameClaims struct {
		jwt.StandardClaims
		DeviceID string
	}
	standardClaims := jwt.StandardClaims{
		Audience:  c.projectID,
		ExpiresAt: expires.Unix(),
		IssuedAt:  timeNow().Add(-time.Minute).Unix(),
	}
	var claims jwt.Claims
	if name == "" {
		claims = &noBagNameClaims{
			DeviceID:       id,
			StandardClaims: standardClaims,
		}
	} else {
		claims = &jwtClaims{
			DeviceID:       id,
			BagName:        name,
			StandardClaims: standardClaims,
		}
	}
	token := jwt.NewWithClaims(signingMethod, claims)
	s, err := token.SignedString(c.privateKey)
	if err != nil {
		panic(err)
	}
	return s
}

func panicOnErr(x interface{}, err error) interface{} {
	if err != nil {
		panic(err)
	}
	return x
}

func testGCP() *gcpConfigTest {
	publicKey := `-----BEGIN CERTIFICATE-----
MIIDBTCCAe2gAwIBAgIUZabeiF2YUMpyWg93MA9PBT3mioIwDQYJKoZIhvcNAQEL
BQAwETEPMA0GA1UEAwwGdW51c2VkMCAXDTIxMTAxNDA4MzcyNVoYDzIyOTUwNzI5
MDgzNzI1WjARMQ8wDQYDVQQDDAZ1bnVzZWQwggEiMA0GCSqGSIb3DQEBAQUAA4IB
DwAwggEKAoIBAQCS5hnI/4Z9RLZNjnCmDGmrl6KXAWwD9X1q29rGqHsp34xnb7pY
4bl3yb1TxrTJTA4WHe8fxmcZDRMZpEbs9fTz3KnraXICo1jLD1eal9myII7RFow7
8Cn4hCpAYNp0Kt0WaM7GDjYw33hoZHLTWFMKoZ5D5AXMLarG8o3gHdMP7GM8LZ8D
QzWaTl9MkM/oIpoCP0TtqAiIF+r2IbLT1/3x6rG/9CWGD8rpifkxAaLjSaMl50Fi
R+R0EbS70qTshda6VwrJgZ72SZG23xTuroFh9RNmy2NJG0BXHG9GNHet1hiOZ66E
GfAJIlV8EPmQQF2SmXItALZ2+FK9RDcqw9dRAgMBAAGjUzBRMB0GA1UdDgQWBBRo
DlEkUkBrYeo1oV98hwFAlcdfxTAfBgNVHSMEGDAWgBRoDlEkUkBrYeo1oV98hwFA
lcdfxTAPBgNVHRMBAf8EBTADAQH/MA0GCSqGSIb3DQEBCwUAA4IBAQATrLJv6WgS
Vs53zBhH6k/wiYwqbN9lUT49jATFtSkUQveyaWjmWgBN8K/2iKk7D4d9/tbEHkOl
CTpeqTSKIIWw/dYCAqs+rM4VkpLGMXlATCM6/P7Z5cDZZEaGvFbl/fRRNDHdBes5
kHIdda+8GwcBJqh1Ujh8sEDBGmcMXFzmN32WL0QyM53uZ+sQZEEOKK9jY/XQYDIT
kLrYyqf+iMsfW6Je/VKHTtUjjdf0uuHP6zd42y5S2QQ+lYehwyIOV10G5JcuTnA/
UOO/jUR+5ZYC2nwfDx6Zn1I5IG3QFNuKuKnQC9t3z1SYqsTQEDGDOmD8tDI8YWG6
H0tMDJXfOZAo
-----END CERTIFICATE-----`
	rawPrivateKey := []byte(`-----BEGIN PRIVATE KEY-----
MIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKcwggSjAgEAAoIBAQCS5hnI/4Z9RLZN
jnCmDGmrl6KXAWwD9X1q29rGqHsp34xnb7pY4bl3yb1TxrTJTA4WHe8fxmcZDRMZ
pEbs9fTz3KnraXICo1jLD1eal9myII7RFow78Cn4hCpAYNp0Kt0WaM7GDjYw33ho
ZHLTWFMKoZ5D5AXMLarG8o3gHdMP7GM8LZ8DQzWaTl9MkM/oIpoCP0TtqAiIF+r2
IbLT1/3x6rG/9CWGD8rpifkxAaLjSaMl50FiR+R0EbS70qTshda6VwrJgZ72SZG2
3xTuroFh9RNmy2NJG0BXHG9GNHet1hiOZ66EGfAJIlV8EPmQQF2SmXItALZ2+FK9
RDcqw9dRAgMBAAECggEBAI0L3so9fxaceSZyk/r7hCK8D+NJ/Dq45dlKi/+fGdMU
0C0o/BYHdhtsWxsreb6mBgh6aXVq/ObyxNoj/+3aI35a69QbhNq/mKwwaP8Iun/r
/vUH31JVwRbbX+48kMRlu66ep5tHXgUDLQufFxmSfvmAQQQS1vY7CvTHRC5itJtd
+2sJ2I1bWMfbEGegnmZirUBcl1ECs7Lwlik3iGUwf7myRjeh31q4ENAa7upaUu/X
JjPLGPNGiCHLTnV7wZuGxEqqA1rHjCjc0tdy1ToMjL/tnI9oPCvgKSvPw4rwM/8l
+Uss6PJIFm198I1w796fRqrNIDWkG/Q8ZzUbBx5AcyECgYEAwtsHAOiwWneCdXwb
RFPqSifioNmWrDc8ksM66bgWSv+fXLMI5Bd4ijzB0AePJnROba7A/epgkKNNQRlu
WzSQLPLueZ8pLhVV3fG4jxA+Ncxr/opazGy9zWTl7cq8R9x5gg14qia1KQFfup+X
PgwmWRMDZcBTXwq7JbM0NGo+M1UCgYEAwP6nIsw6MQlg7MXvOA2lNGmOuP/CSWyc
AVV7qVURGYwl67cWEv6NknJMJcnDu3mKy1IGeJcgKd7MDNmv0ruUMCwpeeqEzkCC
+tNPM/BAHZQDMVW4SDZpKU+AsHOikPRZUsdOR3hcseruUHJq84FfCNv4IWOnA05L
h/Ns/HDmTA0CgYB2MbeA1KRMa9ulef9sJd6i1qjAWtvrYKIMgAHXTUOwgHfhGfRV
rur+JzaFAmDRuZDtNSh5nNawRW4SA+QNzMd7jGwdN+8ZtfVc6EfD991Ucsg7IR9M
itVipkZWRDiK+nB188fypgITenLf1/g8uc/1DfRsnwmzR+YXSylqddt+9QKBgFK3
knkGoVZNF77DoyEaMBmDuIkwDVyc8UxdEBBmhlq1x7b8lLh1Y8ZFuL9ld7/NeyBj
uqRK2Z04gapsTsB6ZywycWBwlJU17y2EDelL6p8Cxk+J1t8UewQasCRwm1eXcwVY
qQNW4hvbfmL6dz6Az3Ojm/jrljSDhTnyql6UIRCtAoGAUwz/p7J8JuUOXwZkT/et
CaYc8irByLwCEbGoS9nVV39yIGN1B3QBoYEwi1VUINUrYrgkzF/hKTpy0nQQFlrj
qq7inE+nwW89vs+xubAAvj3r8Koyeo2V+/SpxV2Q1TT5+4DNKiz0V57UCHrs48MP
V7S+vfEn4CMXg+cjBAUVbuw=
-----END PRIVATE KEY-----`)
	privateKey := panicOnErr(jwt.ParseRSAPrivateKeyFromPEM(rawPrivateKey))
	publicKeyWithoutPrivateKey := `-----BEGIN CERTIFICATE-----
MIIDBTCCAe2gAwIBAgIURewcgV5ZVft0qvsHijFomvB8a18wDQYJKoZIhvcNAQEL
BQAwETEPMA0GA1UEAwwGdW51c2VkMCAXDTIxMTAxNDA4NDAyOFoYDzIyOTUwNzI5
MDg0MDI4WjARMQ8wDQYDVQQDDAZ1bnVzZWQwggEiMA0GCSqGSIb3DQEBAQUAA4IB
DwAwggEKAoIBAQChe3JJILj9jtB+w2LlwVeiU2K2voqIMeL1ycsQEDHuXlWgtv8Q
pMgj/tVO8Rr8mNJTYZts6fyqKQ9ctzuyhXePrKoJLJiEggT5YPlLj0NF6dLAw5lX
HIwtmq6kn7zoYzoqHjJyx+qTYP6mQpbTIMGbmKJtFfHJ+r/uC5RqC2f0r5RdVRZH
Aw/3hUcws02+3fSJNQ9A/xXp4DIV6kVZgNBY5wjwGTi+cGktjI70H0E9f2XeArAi
/4QE3JZ3lt5eK4bET2nmvKAorqDmdWkhoxC0sxNQHUWMj2HarbDglmm6MC7bqTgi
89yP5TZEZw4VwjtvNU+i61k1iE/B9r208iNTAgMBAAGjUzBRMB0GA1UdDgQWBBSP
c8nOxS+37+ZMFf7oT7zuuC4RbDAfBgNVHSMEGDAWgBSPc8nOxS+37+ZMFf7oT7zu
uC4RbDAPBgNVHRMBAf8EBTADAQH/MA0GCSqGSIb3DQEBCwUAA4IBAQAdjqaSub/z
VGBAPxBga5epSUAj1UCDQJd7ssOgHsdZ+51cR/IHYxSB3qK3IgzR+vAOV2qyunB5
nwuWpVEpfm3klTVN37LK3XBUah/V+3WRwAQuTEoTGuXJ0waUWClMKIXJEe0bxZli
0OI3NI7v0TLihE8qdIQSgxY7iYNmHgnA7ulSnziZr+D5Esd+dCTPk0/2SFt38vjo
Vm6CKlHluu8TWy3xy7ppT9dhEAyQ3KQzr2hBrvoInX9By1ym98iZOG/TBfGu9s9M
oPCV0e7ZhaMkq7vmQC+R4Cry0LLhL2y5Erx8r/MqCy09tONbc6FFzO535tCjF++k
v1aXtd+Hg5fu=
-----END CERTIFICATE-----`
	return &gcpConfigTest{
		projectID:     "test-project",
		privateKey:    privateKey,
		rawPrivateKey: rawPrivateKey,
		credentials: map[string][]*cloudiot.DeviceCredential{
			"existing": {
				{
					ExpirationTime: time.Unix(0, 0).Format(time.RFC3339),
					PublicKey: &cloudiot.PublicKeyCredential{
						Format: "RSA_X509_PEM",
						Key:    publicKey,
					},
				},
			},
			"another existing": {
				{
					ExpirationTime: time.Unix(0, 0).Format(time.RFC3339),
					PublicKey: &cloudiot.PublicKeyCredential{
						Format: "RSA_X509_PEM",
						Key:    publicKeyWithoutPrivateKey,
					},
				},
			},
		},
	}
}

func TestValidateJWT(t *testing.T) {
	gcp := testGCP()
	newToken := func(id string, expires *time.Time) string {
		return gcp.newTestToken(id, "testbag.db3", expires)
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
	t.Run("empty bag name", func(t *testing.T) {
		claims, err := validateJWT(bg, gcp, gcp.newTestToken("existing", "", nil))
		assert.Nil(t, err)
		assert.Equal(t, claims.DeviceID, "existing")
		assert.Equal(t, claims.BagName, "")
	})
}
