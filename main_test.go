package main

import (
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
	gen := &urlGenerator{
		Bucket:        "testbucket",
		Account:       "testaccount",
		SigningKey:    gcp.rawPrivateKey,
		ValidDuration: 5 * time.Minute,
	}
	handler := signedURLGeneratorHandler(gen, gcp, true)
	t.Run("bag name included", func(t *testing.T) {
		token := gcp.newTestToken("existing", "test-bag.db3.gz", nil)
		req := httptest.NewRequest("POST", "/generate-url", nil)
		req.Header.Add("Authorization", "Bearer "+token)
		resp := httptest.NewRecorder()
		handler.ServeHTTP(resp, req)
		require.Equal(t,
			"{\"url\":\"https://storage.googleapis.com/testbucket/existing/test-bag.db3.gz?Expires=1616758260\\u0026GoogleAccessId=testaccount\\u0026Signature=LbwByUcGK2b%2FbCl6nqjgTLnnHEznXz%2Fcs%2B%2FNo5KE4Epi7%2BMvA%2FfVgtKCk4jQyIekiqroAUHFNHp6uh0z4Ft%2F5TY95%2BKHFsPB%2FmiOrBtyjdfP3cdmV3Z2IgoftEvk0ESY9u3GQQJi8BTnHVgF%2B8yJLyo9%2B9WYGH6nHvVNvOHf6129mV7J5o2EhB%2F%2BPo5JNHI4hreQzXbR8%2Br1a9mbJYjNhY%2FI5gzTtjARfO4hEus5y6I8k6AtQuNjyV7mx4LsXh0XGSSSlfwsOioiY%2FOnWahMBxViZWInnnni%2FUJVT1QuNATllSNd6eIMajVFv2noFbGhiyq8Nmo45NlxDD1gvyRV0w%3D%3D\"}\n",
			resp.Body.String(),
		)
	})
	t.Run("bag name not included", func(t *testing.T) {
		token := gcp.newTestToken("existing", "", nil)
		req := httptest.NewRequest("POST", "/generate-url", nil)
		req.Header.Add("Authorization", "Bearer "+token)
		resp := httptest.NewRecorder()
		handler.ServeHTTP(resp, req)
		require.Equal(t,
			"{\"url\":\"https://storage.googleapis.com/testbucket/existing/2021-03-26T11:26:00Z.db3?Expires=1616758260\\u0026GoogleAccessId=testaccount\\u0026Signature=h0HH7Mc%2BARqpivFQE1a38eagKN8rnI%2FjeE4gnvif3LDXbkVu7JKRwfE3wYeQGtxxG%2BLm3prg%2FYjPgyNAi9S2mFHywDHec1M2%2BSnGy4EO4bigXazx5AiQ5mj1BSL%2B7%2BexjTlTL75MLZeBf0pK1eIfTm5nOQstpQpmuigcURVQc0twiQLjaKVm3KUHRzO6eJdhcaA0FMvala0w%2FZZLEHSwHTI3Tjk%2FdHjNBGOF%2FIVDvD42qnwx6blTwfxebq4lLQN1rNAotd6P5qHPmyNK3bYj9tQt2txzPdtF5lSsVIIKJmSbPj5vOaqNGLbw4mbjf4roQAcs%2FmU44xGMj5%2BV32SNNg%3D%3D\"}\n",
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
	r.Path("/upload").Methods("PUT").Handler(receiveUploadHandler(dir))

	validateFile := func(t *testing.T, device, bagName, data string) {
		fileData, err := os.ReadFile(filepath.Join(dir, device, bagName))
		require.Nil(t, err)
		require.Equal(t, data, string(fileData))
	}

	uploadFile := func(t *testing.T, device, bagName, data string) {
		token := gcp.newTestToken(device, bagName, nil)
		req, err := http.NewRequest("POST", server.URL+"/generate-url", nil)
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
		req2, err := http.NewRequest("PUT", url.URL, bagData)
		t.Log(err)
		require.Nil(t, err)
		resp2, err := http.DefaultClient.Do(req2)
		require.Nil(t, err)
		defer resp2.Body.Close()
		require.Equal(t, resp2.StatusCode, 200)
		body2, err := io.ReadAll(resp2.Body)
		require.Nil(t, err)
		require.Equal(t, "", string(body2))
	}

	t.Run("upload a file", func(t *testing.T) {
		uploadFile(t, "testdevice", "rosbag.db3", "hello world")
		validateFile(t, "testdevice", "rosbag.db3", "hello world")
		uploadFile(t, "testdevice", "", "another file")
		uploadFile(t, "/../device", "../.../.", "file with\nnewline")

		// Check that the files haven't been overwritten
		validateFile(t, "testdevice", "rosbag.db3", "hello world")
		validateFile(t, "testdevice", generateBagName(), "another file")
		validateFile(t, "___device", "___._.", "file with\nnewline")
	})
}
