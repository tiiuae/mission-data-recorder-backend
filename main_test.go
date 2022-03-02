package main

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/stretchr/testify/require"
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

func TestSignedURLGeneratorHandler(t *testing.T) {
	gcp := testGCP()
	config := &configuration{
		Bucket:            "testbucket",
		Account:           "testaccount",
		privateKey:        gcp.rawPrivateKey,
		URLValidDuration:  5 * time.Minute,
		Debug:             true,
		DisableValidation: true,
	}
	handler := signedURLGeneratorHandler(config, gcp)
	t.Run("bag name included", func(t *testing.T) {
		token := gcp.newTestToken("existing", "", "test-bag.db3.gz", nil)
		req := httptest.NewRequest("POST", "/generate-url", nil)
		req.Header.Add("Authorization", "Bearer "+token)
		resp := httptest.NewRecorder()
		handler.ServeHTTP(resp, req)
		require.Equal(t,
			"{\"url\":\"https://storage.googleapis.com/testbucket/test-tenant/existing/test-bag.db3.gz?Expires=1616758260\\u0026GoogleAccessId=testaccount\\u0026Signature=XlFzKCa8PMtJfqDuuEw0R5hB7uJ0pz%2BmG%2Buqf8B5pkGNGcULMeZzj%2Fbm7K%2B%2Bnt5G03cvtoMfLtERAAI%2BPFQh3ohzqpjjiqJxEnpCvLjgg1IetqwkOJMVz7%2FTCnl5%2Fah4En2UkCMAXAU4XWTxaqZqFp7H8iZCT0ize4NWVB2zIW8bChF3hXl6sIO8WALeG%2FMKKrnPd1ieVqJKq6EkCZKpF7A3QoSHlabGnIfqvyYXM1fuFo3aKjK8AA7Nmxe3bmHt0F4xE5b09DPJ1BGRqVs1b71mrq0FbZmAEeNUIqCyxee2bz%2FBCUZ%2F5dWT8Zhuhfdeh4Iaje%2FIjZ%2BdIzTC085KPQ%3D%3D\"}\n",
			resp.Body.String(),
		)
	})
	t.Run("bag name not included", func(t *testing.T) {
		token := gcp.newTestToken("existing", "", "", nil)
		req := httptest.NewRequest("POST", "/generate-url", nil)
		req.Header.Add("Authorization", "Bearer "+token)
		resp := httptest.NewRecorder()
		handler.ServeHTTP(resp, req)
		require.Equal(t,
			"{\"url\":\"https://storage.googleapis.com/testbucket/test-tenant/existing/2021-03-26T11:26:00.000000000Z.db3?Expires=1616758260\\u0026GoogleAccessId=testaccount\\u0026Signature=F6n36cLTodQId09QmVTK%2FFjCUBh6WdvFfp8SN35tRj43g5UW0qBGTVnP6bF66KKpwS9mYLv9WN8pm%2Bk%2FLfsxj1w4l%2BKTWnMySkwzonwT9YaAQ79DpXCIv6CPYAaSRkx4H%2ByIjl%2FUHvsgS%2BgzeHNdsDLK3WrNP%2Fn2sFN9inVVDERqpjA0eL04nJo7G%2BUy%2B%2BzLzZvZu4KSqKcwalVBL6U75ShT%2BFxNVqppr4KSMCjfAFds9gzvu%2BOrI8xCgXI6frQYUVvUmsDRO1hyoXrBbU%2Bl9Dad0jkBxl8QTEsvDTTmq8yoGf3WyIEq%2BbPPuoiXVat2UTIXs5hrxYCzc7yKnN6JuA%3D%3D\"}\n",
			resp.Body.String(),
		)
	})
}

func TestLocalUploading(t *testing.T) {
	gcp := testGCP()
	dir := t.TempDir()

	r := mux.NewRouter()
	server := httptest.NewServer(r)
	defer server.Close()
	r.Path("/generate-url").Methods("POST").Handler(localURLGeneratorHandler(server.URL))
	r.Path("/upload").Methods("PUT").Handler(receiveUploadHandler(dir, "fleet-registry"))

	validateFile := func(t *testing.T, tenant, device, bagName, data string) {
		t.Helper()
		fileData, err := os.ReadFile(filepath.Join(dir, tenant, device, bagName))
		require.Nil(t, err)
		require.Equal(t, data, string(fileData))
	}

	uploadFile := func(t *testing.T, device, bagName, data string) {
		t.Helper()
		token := gcp.newTestToken(device, "test-tenant", bagName, nil)
		req, err := http.NewRequestWithContext(
			context.Background(), "POST", server.URL+"/generate-url", nil,
		)
		require.Nil(t, err)
		req.Header.Add("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		require.Nil(t, err)
		defer resp.Body.Close()
		require.Equal(t, resp.StatusCode, 200)
		body, err := io.ReadAll(resp.Body)
		require.Nil(t, err)
		var url struct{ URL string }
		require.Nil(t, json.Unmarshal(body, &url))

		bagData := strings.NewReader(data)
		req2, err := http.NewRequestWithContext(
			context.Background(), "PUT", url.URL, bagData,
		)
		t.Log(err)
		require.Nil(t, err)
		t.Log(url.URL)
		resp2, err := http.DefaultClient.Do(req2)
		require.Nil(t, err)
		defer resp2.Body.Close()
		body2, err := io.ReadAll(resp2.Body)
		require.Nil(t, err)
		t.Logf("%s", body2)
		require.Equal(t, "", string(body2))
		require.Equal(t, resp2.StatusCode, 200)
	}

	t.Run("upload a file", func(t *testing.T) {
		uploadFile(t, "testdevice", "rosbag.db3", "hello world")
		validateFile(t, "test-tenant", "testdevice", "rosbag.db3", "hello world")
		uploadFile(t, "testdevice", "", "another file")
		uploadFile(t, "/../device", "../.../.", "file with\nnewline")

		// Check that the files haven't been overwritten
		validateFile(t, "test-tenant", "testdevice", "rosbag.db3", "hello world")
		validateFile(t, "test-tenant", "testdevice", generateBagName(), "another file")
		validateFile(t, "test-tenant", "___device", "___._.", "file with\nnewline")
	})
}
