package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/alecthomas/kingpin"
	"github.com/efficientgo/core/errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/thanos-io/thanos/pkg/extprom"
	thanoshttp "github.com/thanos-io/thanos/pkg/extprom/http"
)

type APIConfig struct {
	BaseURL   string
	OAuthURL  string
	AppKey    string
	AppSecret string
}

func (o *APIConfig) BindFlags(app *kingpin.Application) {
	app.Flag("base-url", "Base URL of the Qingping API.").
		Envar("QINGPING_BASE_URL").
		Default("https://apis.cleargrass.com").
		StringVar(&o.BaseURL)

	app.Flag("oauth-url", "OAuth URL of the Qingping API.").
		Envar("QINGPING_OAUTH_URL").
		Default("https://oauth.cleargrass.com/oauth2/token").
		StringVar(&o.OAuthURL)

	app.Flag("app-key", "App key of the Qingping API.").
		Envar("QINGPING_APP_KEY").
		Required().
		StringVar(&o.AppKey)

	app.Flag("app-secret", "App secret of the Qingping API.").
		Envar("QINGPING_APP_SECRET").
		Required().
		StringVar(&o.AppSecret)
}

type oauthToken struct {
	bearer string
	expiry time.Time
}

type Client struct {
	apiConfig  *APIConfig
	HTTPClient *http.Client
	Token      oauthToken
	nowFunc    func() time.Time
}

type DeviceListResponse struct {
	Total   int      `json:"total"`
	Devices []Device `json:"devices"`
}

type DeviceDataResponse struct {
	Total int          `json:"total"`
	Data  []DeviceData `json:"data"`
}

type Device struct {
	Info DeviceInfo `json:"info"`
	Data DeviceData `json:"data"`
}

// PrettyPrint prints the device information in a human-readable format.
// The information is shown in one single line.
// Name (MAC) - Version - Status
func (d Device) PrettyPrint(w io.Writer) {
	status := "Online"
	if d.Info.Status.Offline {
		status = "Offline"
	}
	_, _ = fmt.Fprintf(w, "%s (%s) - %s - %s\n", d.Info.Name, d.Info.MAC, d.Info.Version, status)
}

type DeviceInfo struct {
	MAC            string        `json:"mac"`
	Product        ProductInfo   `json:"product"`
	Name           string        `json:"name"`
	Version        string        `json:"version"`
	CreatedAt      int64         `json:"created_at"`
	GroupID        int           `json:"group_id"`
	GroupName      string        `json:"group_name"`
	Status         DeviceStatus  `json:"status"`
	ConnectionType string        `json:"connection_type"`
	Setting        DeviceSetting `json:"setting"`
}

type ProductInfo struct {
	ID           int    `json:"id"`
	Code         string `json:"code"`
	Name         string `json:"name"`
	EnName       string `json:"en_name"`
	NoBleSetting bool   `json:"noBleSetting"`
}

type DeviceStatus struct {
	Offline bool `json:"offline"`
}

type DeviceSetting struct {
	ReportInterval  int `json:"report_interval"`
	CollectInterval int `json:"collect_interval"`
}

type DeviceData struct {
	Timestamp ValueData `json:"timestamp"`

	Battery ValueData `json:"battery"`

	Temperature ValueData `json:"temperature"`
	Humidity    ValueData `json:"humidity"`
	CO2         ValueData `json:"co2"`
	PM25        ValueData `json:"pm25"`
	PM10        ValueData `json:"pm10"`
}

type ValueData struct {
	Value float64 `json:"value"`
}

type clientOpts struct {
	reg     prometheus.Registerer
	nowFunc func() time.Time
}

var defaultClientOpts = clientOpts{
	nowFunc: time.Now,
}

type Option func(*clientOpts)

func WithRegistry(reg prometheus.Registerer) func(*clientOpts) {
	return func(o *clientOpts) {
		o.reg = reg
	}
}

func WithNowFunc(nowFunc func() time.Time) func(*clientOpts) {
	return func(o *clientOpts) {
		o.nowFunc = nowFunc
	}
}

func New(apiConf *APIConfig, opts ...Option) *Client {
	o := defaultClientOpts
	for _, opt := range opts {
		opt(&o)
	}

	t := http.DefaultTransport.(*http.Transport).Clone()
	t.MaxIdleConns = 100
	t.MaxConnsPerHost = 100
	t.MaxIdleConnsPerHost = 100

	httpClient := &http.Client{
		Timeout:   10 * time.Second,
		Transport: t,
	}
	if o.reg != nil {
		// wrap HTTP client with promhttp.InstrumentRoundTripperDuration
		reg := extprom.WrapRegistererWithPrefix("qingping_", o.reg)
		httpClientMetrics := thanoshttp.NewClientMetrics(reg)
		httpClient.Transport = thanoshttp.InstrumentedRoundTripper(httpClient.Transport, httpClientMetrics)
	}

	return &Client{
		apiConfig:  apiConf,
		HTTPClient: httpClient,
		nowFunc:    o.nowFunc,
	}
}

func (c *Client) IsAuthenticated() bool {
	return c.Token.bearer != "" && c.nowFunc().Before(c.Token.expiry)
}

func (c *Client) Authenticate() (string, error) {
	data := url.Values{}
	data.Set("grant_type", "client_credentials")
	data.Set("scope", "device_full_access")

	req, err := http.NewRequest("POST", c.apiConfig.OAuthURL, bytes.NewBufferString(data.Encode()))
	if err != nil {
		return "", err
	}
	req.SetBasicAuth(c.apiConfig.AppKey, c.apiConfig.AppSecret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to get OAuth token: %s", resp.Status)
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	accessToken := result["access_token"].(string)
	expiresIn := int(result["expires_in"].(float64)) // Convert to int

	c.Token = oauthToken{
		bearer: accessToken,
		expiry: time.Now().Add(time.Duration(expiresIn) * time.Second),
	}

	return accessToken, nil
}

func (c *Client) ensureAuthenticated() error {
	if !c.IsAuthenticated() {
		_, err := c.Authenticate()
		if err != nil {
			return err
		}
	}

	return nil
}

func (c *Client) doAuthenticatedReq(req *http.Request) (*http.Response, error) {
	err := c.ensureAuthenticated()
	if err != nil {
		return nil, errors.Wrap(err, "failed to authenticate")
	}

	req.Header.Set("Authorization", "Bearer "+c.Token.bearer)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}

	return resp, nil
}

func (c *Client) GetDeviceList() (*DeviceListResponse, error) {
	timestamp := strconv.FormatInt(c.nowFunc().Unix(), 10)
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/v1/apis/devices?timestamp=%s", c.apiConfig.BaseURL, timestamp), nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.doAuthenticatedReq(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to get device list: %s", resp.Status)
	}

	var result DeviceListResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return &result, nil
}

func (c *Client) ChangeDeviceSettings(mac []string, reportInterval, collectInterval time.Duration) error {
	body := map[string]interface{}{
		"mac":              mac,
		"report_interval":  int64(reportInterval.Abs().Seconds()),
		"collect_interval": int64(collectInterval.Abs().Seconds()),
		"timestamp":        c.nowFunc().Unix(),
	}

	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("PUT", fmt.Sprintf("%s/v1/apis/devices/settings", c.apiConfig.BaseURL), bytes.NewBuffer(bodyBytes))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.Token.bearer)

	resp, err := c.doAuthenticatedReq(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to change device settings: %s", resp.Status)
	}

	return nil
}
func (c *Client) GetDataHistory(mac string, startTime, endTime time.Time) (*DeviceDataResponse, error) {
	values := url.Values{}
	values.Set("mac", mac)
	values.Set("start_time", strconv.FormatInt(startTime.Unix(), 10))
	values.Set("end_time", strconv.FormatInt(endTime.Unix(), 10))
	values.Set("timestamp", strconv.FormatInt(c.nowFunc().UnixMilli(), 10))
	values.Set("limit", "200")

	url := fmt.Sprintf("%s/v1/apis/devices/data?%s", c.apiConfig.BaseURL, values.Encode())
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.doAuthenticatedReq(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to get data history: %s", resp.Status)
	}

	var result DeviceDataResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return &result, nil
}
