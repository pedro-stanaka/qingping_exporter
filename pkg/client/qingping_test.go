package client_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/pedro-stanaka/qingping_exporter/pkg/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClient_TestAuthenticate(t *testing.T) {
	server := createTestAuthServer(t, 3600*time.Second)
	defer server.Close()

	mockedNow := time.Now()
	nowFunc := func() time.Time {
		return mockedNow
	}

	qc := client.New(
		&client.APIConfig{OauthUrl: server.URL, AppKey: "foo", AppSecret: "bar"},
		client.WithNowFunc(nowFunc),
	)
	token, err := qc.Authenticate()
	assert.NoError(t, err)
	assert.Equal(t, "test-token", token)
	assert.True(t, qc.IsAuthenticated())

	// not authenticated, when token is expired
	mockedNow = mockedNow.Add(2 * time.Hour)
	assert.False(t, qc.IsAuthenticated())
}

func createTestAuthServer(t *testing.T, tokenExpiry time.Duration) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			require.Equal(t, "application/x-www-form-urlencoded", r.Header.Get("Content-Type"))
			// must set the basic auth
			require.Contains(t, r.Header.Get("Authorization"), "Basic")
			w.WriteHeader(http.StatusOK)
			expirySeconds := strconv.FormatInt(int64(tokenExpiry.Seconds()), 10)
			w.Write([]byte(`{"access_token": "test-token", "expires_in": ` + expirySeconds + `}`))
			return
		}

		w.WriteHeader(http.StatusNotFound)
	}))
}

func TestClient_GetDeviceList(t *testing.T) {
	authSrv := createTestAuthServer(t, 3600*time.Second)
	defer authSrv.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/apis/devices", r.URL.Path)
		assert.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))

		w.WriteHeader(http.StatusOK)
		w.Header().Set("Content-Type", "application/json")
		http.ServeFile(w, r, "testdata/device_list.json")
	}))
	defer server.Close()

	client := client.New(&client.APIConfig{
		BaseURL:  server.URL,
		OauthUrl: authSrv.URL,
	})

	result, err := client.GetDeviceList()
	assert.NoError(t, err)

	assert.Len(t, result.Devices, 1)
	assert.Equal(t, "34CE00000000", result.Devices[0].Info.MAC)
}

func TestClient_ChangeDeviceSettings(t *testing.T) {
	authSrv := createTestAuthServer(t, 3600*time.Second)
	defer authSrv.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/apis/devices/settings", r.URL.Path)
		assert.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))

		// check the request body - json
		var reqBody map[string]interface{}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&reqBody))
		assert.EqualValues(t, []interface{}{"mac1", "mac2"}, reqBody["mac"])
		assert.EqualValues(t, 5, reqBody["report_interval"])
		assert.EqualValues(t, 10, reqBody["collect_interval"])

		w.WriteHeader(http.StatusOK)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status": "success"}`))
	}))
	defer server.Close()

	client := client.New(&client.APIConfig{
		BaseURL:  server.URL,
		OauthUrl: authSrv.URL,
	})

	err := client.ChangeDeviceSettings([]string{"mac1", "mac2"}, 5*time.Second, 10*time.Second)
	assert.NoError(t, err)
}

func TestGetDataHistory(t *testing.T) {
	authSrv := createTestAuthServer(t, 3600*time.Second)

	// Mock server to simulate API responses
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/apis/devices/data", r.URL.Path)
		assert.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		http.ServeFile(w, r, "testdata/device_data_history.json")
	}))
	defer mockServer.Close()

	// Create a new Qingping client
	qc := client.New(&client.APIConfig{
		BaseURL:  mockServer.URL,
		OauthUrl: authSrv.URL,
	})

	// Call the GetDataHistory method
	startTime := time.Unix(1726749900, 0)
	endTime := time.Unix(1726750800, 0)
	data, err := qc.GetDataHistory("mac1", startTime, endTime)

	// Assert no error
	assert.NoError(t, err)

	// Assert the response data
	expectedData := &client.DeviceDataResponse{
		Total: 2,
		Data: []client.DeviceData{
			{
				Timestamp:   client.ValueData{Value: 1726749900},
				Battery:     client.ValueData{Value: 47},
				Temperature: client.ValueData{Value: 25.6},
				Humidity:    client.ValueData{Value: 58.3},
				CO2:         client.ValueData{Value: 454},
				PM25:        client.ValueData{Value: 12},
				PM10:        client.ValueData{Value: 12},
			},
			{
				Timestamp:   client.ValueData{Value: 1726750800},
				Battery:     client.ValueData{Value: 44},
				Temperature: client.ValueData{Value: 26.1},
				Humidity:    client.ValueData{Value: 56},
				CO2:         client.ValueData{Value: 452},
				PM25:        client.ValueData{Value: 12},
				PM10:        client.ValueData{Value: 12},
			},
		},
	}
	assert.Equal(t, expectedData, data)
}
