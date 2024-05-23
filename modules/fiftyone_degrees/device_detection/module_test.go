package device_detection

import (
	"context"
	"errors"
	"github.com/51Degrees/device-detection-go/v4/dd"
	"github.com/51Degrees/device-detection-go/v4/onpremise"
	"github.com/prebid/prebid-server/v2/hooks/hookstage"
	"github.com/prebid/prebid-server/v2/modules/moduledeps"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"net/http"
	"os"
	"testing"
)

type mockAccValidator struct {
	mock.Mock
}

func (m *mockAccValidator) IsWhiteListed(cfg Config, req []byte) bool {
	args := m.Called(cfg, req)
	return args.Bool(0)
}

type mockEvidenceExtractor struct {
	mock.Mock
}

func (m *mockEvidenceExtractor) FromHeaders(request *http.Request, httpHeaderKeys []dd.EvidenceKey) []StringEvidence {
	args := m.Called(request, httpHeaderKeys)

	return args.Get(0).([]StringEvidence)
}

func (m *mockEvidenceExtractor) FromSuaPayload(request *http.Request, payload []byte) []StringEvidence {
	args := m.Called(request, payload)

	return args.Get(0).([]StringEvidence)
}

func (m *mockEvidenceExtractor) Extract(ctx hookstage.ModuleContext) ([]onpremise.Evidence, string, error) {
	args := m.Called(ctx)

	res := args.Get(0)
	if res == nil {
		return nil, args.String(1), args.Error(2)
	}

	return res.([]onpremise.Evidence), args.String(1), args.Error(2)
}

type mockDeviceDetector struct {
	mock.Mock
}

func (m *mockDeviceDetector) GetSupportedHeaders() []dd.EvidenceKey {
	args := m.Called()
	return args.Get(0).([]dd.EvidenceKey)
}

func (m *mockDeviceDetector) GetDeviceInfo(evidence []onpremise.Evidence, ua string) (*DeviceInfo, error) {

	args := m.Called(evidence, ua)

	res := args.Get(0)

	if res == nil {
		return nil, args.Error(1)
	}

	return res.(*DeviceInfo), args.Error(1)
}

func TestHandleEntrypointNotWhitelistedHook(t *testing.T) {
	var mockValidator mockAccValidator

	mockValidator.On("IsWhiteListed", mock.Anything, mock.Anything).Return(false)

	module := Module{
		accountValidator: &mockValidator,
	}

	_, err := module.HandleEntrypointHook(nil, hookstage.ModuleInvocationContext{}, hookstage.EntrypointPayload{})
	assert.Error(t, err)
	assert.Equal(t, "hook execution failed: account not whitelisted", err.Error())
}

func TestHandleEntrypointWhitelistedHook(t *testing.T) {
	var mockValidator mockAccValidator

	mockValidator.On("IsWhiteListed", mock.Anything, mock.Anything).Return(true)

	var mockEvidenceExtractor mockEvidenceExtractor
	mockEvidenceExtractor.On("FromHeaders", mock.Anything, mock.Anything).Return(
		[]StringEvidence{{
			Prefix: "123",
			Key:    "key",
			Value:  "val",
		}},
	)

	mockEvidenceExtractor.On("FromSuaPayload", mock.Anything, mock.Anything).Return(
		[]StringEvidence{{
			Prefix: "123",
			Key:    "User-Agent",
			Value:  "ua",
		}},
	)

	var mockDeviceDetector mockDeviceDetector

	mockDeviceDetector.On("GetSupportedHeaders").Return(
		[]dd.EvidenceKey{{
			Prefix: dd.HttpEvidenceQuery,
			Key:    "key",
		}},
	)

	module := Module{
		deviceDetector:    &mockDeviceDetector,
		evidenceExtractor: &mockEvidenceExtractor,
		accountValidator:  &mockValidator,
	}

	result, err := module.HandleEntrypointHook(nil, hookstage.ModuleInvocationContext{}, hookstage.EntrypointPayload{})
	assert.NoError(t, err)

	assert.Equal(
		t, result.ModuleContext[EvidenceFromHeadersCtxKey], []StringEvidence{{
			Prefix: "123",
			Key:    "key",
			Value:  "val",
		}},
	)

	assert.Equal(
		t, result.ModuleContext[EvidenceFromSuaCtxKey], []StringEvidence{{
			Prefix: "123",
			Key:    "User-Agent",
			Value:  "ua",
		}},
	)
}

func TestModule_HandleRawAuctionHookDisabledDdContext(t *testing.T) {

	module := Module{}
	var emptyResult hookstage.HookResult[hookstage.RawAuctionRequestPayload]
	result, err := module.HandleRawAuctionHook(
		nil, hookstage.ModuleInvocationContext{
			ModuleContext: make(hookstage.ModuleContext),
		}, hookstage.RawAuctionRequestPayload{},
	)
	assert.NoError(t, err)
	assert.Equal(t, result, emptyResult)
}

func TestModule_HandleRawAuctionHookWithNilDeviceDetector(t *testing.T) {
	module := Module{}

	mctx := make(hookstage.ModuleContext)
	mctx[DDEnabledCtxKey] = true

	_, err := module.HandleRawAuctionHook(
		nil, hookstage.ModuleInvocationContext{
			ModuleContext: mctx,
		},
		hookstage.RawAuctionRequestPayload{},
	)
	assert.Errorf(t, err, "error getting device detector")
}

func TestModule_TestModule_HandleRawAuctionHookExtractError(t *testing.T) {
	var mockValidator mockAccValidator

	mockValidator.On("IsWhiteListed", mock.Anything, mock.Anything).Return(true)

	var evidenceExtractorM mockEvidenceExtractor
	evidenceExtractorM.On("Extract", mock.Anything).Return(
		nil,
		"ua",
		nil,
	)

	var mockDeviceDetector mockDeviceDetector

	module := Module{
		deviceDetector:    &mockDeviceDetector,
		evidenceExtractor: &evidenceExtractorM,
		accountValidator:  &mockValidator,
	}

	mctx := make(hookstage.ModuleContext)

	mctx[DDEnabledCtxKey] = true

	result, err := module.HandleRawAuctionHook(
		context.TODO(), hookstage.ModuleInvocationContext{
			ModuleContext: mctx,
		},
		hookstage.RawAuctionRequestPayload{},
	)

	assert.NoError(t, err)
	assert.Equal(t, len(result.ChangeSet.Mutations()), 1)
	assert.Equal(t, result.ChangeSet.Mutations()[0].Type(), hookstage.MutationUpdate)

	mutation := result.ChangeSet.Mutations()[0]

	body := []byte(`{}`)

	_, err = mutation.Apply(body)
	assert.Errorf(t, err, "error extracting evidence")

	var mockEvidenceErrExtractor mockEvidenceExtractor
	mockEvidenceErrExtractor.On("Extract", mock.Anything).Return(
		nil,
		"",
		errors.New("error"),
	)

	module.evidenceExtractor = &mockEvidenceErrExtractor

	result, err = module.HandleRawAuctionHook(
		context.TODO(), hookstage.ModuleInvocationContext{
			ModuleContext: mctx,
		},
		hookstage.RawAuctionRequestPayload{},
	)

	assert.NoError(t, err)

	assert.Equal(t, len(result.ChangeSet.Mutations()), 1)

	assert.Equal(t, result.ChangeSet.Mutations()[0].Type(), hookstage.MutationUpdate)

	mutation = result.ChangeSet.Mutations()[0]

	_, err = mutation.Apply(body)
	assert.Errorf(t, err, "error extracting evidence error")

}

func TestModule_HandleRawAuctionHookEnrichment(t *testing.T) {
	var mockValidator mockAccValidator

	mockValidator.On("IsWhiteListed", mock.Anything, mock.Anything).Return(true)

	var mockEvidenceExtractor mockEvidenceExtractor
	mockEvidenceExtractor.On("Extract", mock.Anything).Return(
		[]onpremise.Evidence{
			{
				Key:   "key",
				Value: "val",
			},
		},
		"ua",
		nil,
	)

	var deviceDetectorM mockDeviceDetector

	deviceDetectorM.On("GetDeviceInfo", mock.Anything, mock.Anything).Return(
		&DeviceInfo{
			HardwareVendor:        "Apple",
			HardwareName:          "Macbook",
			DeviceType:            "device",
			PlatformVendor:        "Apple",
			PlatformName:          "MacOs",
			PlatformVersion:       "14",
			BrowserVendor:         "Google",
			BrowserName:           "Crome",
			BrowserVersion:        "12",
			ScreenPixelsWidth:     1024,
			ScreenPixelsHeight:    1080,
			PixelRatio:            223,
			Javascript:            true,
			GeoLocation:           true,
			HardwareFamily:        "Macbook",
			HardwareModel:         "Macbook",
			HardwareModelVariants: "Macbook",
			UserAgent:             "ua",
			DeviceId:              "",
		},
		nil,
	)

	module := Module{
		deviceDetector:    &deviceDetectorM,
		evidenceExtractor: &mockEvidenceExtractor,
		accountValidator:  &mockValidator,
	}

	mctx := make(hookstage.ModuleContext)
	mctx[DDEnabledCtxKey] = true

	result, err := module.HandleRawAuctionHook(
		nil, hookstage.ModuleInvocationContext{
			ModuleContext: mctx,
		},
		[]byte{},
	)
	assert.NoError(t, err)
	assert.Equal(t, len(result.ChangeSet.Mutations()), 1)
	assert.Equal(t, result.ChangeSet.Mutations()[0].Type(), hookstage.MutationUpdate)

	mutation := result.ChangeSet.Mutations()[0]

	body := []byte(`{
		"device": {
			"connectiontype": 2,
			"ext": {
				"atts": 0,
				"ifv": "1B8EFA09-FF8F-4123-B07F-7283B50B3870"
			},
			"sua": {
				"source": 2,
				"browsers": [
					{
						"brand": "Not A(Brand",
						"version": [
							"99",
							"0",
							"0",
							"0"
						]
					},
					{
						"brand": "Google Chrome",
						"version": [
							"121",
							"0",
							"6167",
							"184"
						]
					},
					{
						"brand": "Chromium",
						"version": [
							"121",
							"0",
							"6167",
							"184"
						]
					}
				],
				"platform": {
					"brand": "macOS",
					"version": [
						"14",
						"0",
						"0"
					]
				},
				"mobile": 0,
				"architecture": "arm",
				"model": ""
			}
		}
	}`)

	mutationResult, err := mutation.Apply(body)

	assert.Equal(
		t, mutationResult, hookstage.RawAuctionRequestPayload(
			hookstage.RawAuctionRequestPayload(
				[]byte(`{
		"device": {
			"connectiontype": 2,
			"ext": {
				"atts": 0,
				"ifv": "1B8EFA09-FF8F-4123-B07F-7283B50B3870"
			,"fiftyonedegrees_deviceId":""},
			"sua": {
				"source": 2,
				"browsers": [
					{
						"brand": "Not A(Brand",
						"version": [
							"99",
							"0",
							"0",
							"0"
						]
					},
					{
						"brand": "Google Chrome",
						"version": [
							"121",
							"0",
							"6167",
							"184"
						]
					},
					{
						"brand": "Chromium",
						"version": [
							"121",
							"0",
							"6167",
							"184"
						]
					}
				],
				"platform": {
					"brand": "macOS",
					"version": [
						"14",
						"0",
						"0"
					]
				},
				"mobile": 0,
				"architecture": "arm",
				"model": ""
			}
		,"devicetype":2,"ua":"ua","make":"Apple","model":"Macbook","os":"MacOs","osv":"14","h":1080,"w":1024,"pxratio":223,"js":1,"geoFetch":1}
	}`),
			),
		),
	)

	var deviceDetectorErrM mockDeviceDetector

	deviceDetectorErrM.On("GetDeviceInfo", mock.Anything, mock.Anything).Return(
		nil,
		errors.New("error"),
	)

	module.deviceDetector = &deviceDetectorErrM

	result, err = module.HandleRawAuctionHook(
		nil, hookstage.ModuleInvocationContext{
			ModuleContext: mctx,
		},
		[]byte{},
	)

	assert.NoError(t, err)

	assert.Equal(t, len(result.ChangeSet.Mutations()), 1)

	assert.Equal(t, result.ChangeSet.Mutations()[0].Type(), hookstage.MutationUpdate)

	mutation = result.ChangeSet.Mutations()[0]

	_, err = mutation.Apply(body)
	assert.Errorf(t, err, "error getting device info")
}

func TestModule_HandleRawAuctionHookEnrichmentWithErrors(t *testing.T) {
	var mockValidator mockAccValidator

	mockValidator.On("IsWhiteListed", mock.Anything, mock.Anything).Return(true)

	var mockEvidenceExtractor mockEvidenceExtractor
	mockEvidenceExtractor.On("Extract", mock.Anything).Return(
		[]onpremise.Evidence{
			{
				Key:   "key",
				Value: "val",
			},
		},
		"ua",
		nil,
	)

	var mockDeviceDetector mockDeviceDetector

	mockDeviceDetector.On("GetDeviceInfo", mock.Anything, mock.Anything).Return(
		&DeviceInfo{
			HardwareVendor:        "Apple",
			HardwareName:          "Macbook",
			DeviceType:            "device",
			PlatformVendor:        "Apple",
			PlatformName:          "MacOs",
			PlatformVersion:       "14",
			BrowserVendor:         "Google",
			BrowserName:           "Crome",
			BrowserVersion:        "12",
			ScreenPixelsWidth:     1024,
			ScreenPixelsHeight:    1080,
			PixelRatio:            223,
			Javascript:            true,
			GeoLocation:           true,
			HardwareFamily:        "Macbook",
			HardwareModel:         "Macbook",
			HardwareModelVariants: "Macbook",
			UserAgent:             "ua",
			DeviceId:              "",
			ScreenInchesHeight:    7,
		},
		nil,
	)

	module := Module{
		deviceDetector:    &mockDeviceDetector,
		evidenceExtractor: &mockEvidenceExtractor,
		accountValidator:  &mockValidator,
	}

	mctx := make(hookstage.ModuleContext)
	mctx[DDEnabledCtxKey] = true

	result, err := module.HandleRawAuctionHook(
		nil, hookstage.ModuleInvocationContext{
			ModuleContext: mctx,
		},
		[]byte{},
	)
	assert.NoError(t, err)
	assert.Equal(t, len(result.ChangeSet.Mutations()), 1)
	assert.Equal(t, result.ChangeSet.Mutations()[0].Type(), hookstage.MutationUpdate)

	mutation := result.ChangeSet.Mutations()[0]

	invalidJsonBody := []byte(`{`)

	mutationResult, err := mutation.Apply(invalidJsonBody)
	assert.NoError(t, err)
	assert.Equal(
		t,
		mutationResult,
		hookstage.RawAuctionRequestPayload([]byte(`{"device":{"ua":"ua","make":"Apple","model":"Macbook","os":"MacOs","osv":"14","h":1080,"w":1024,"pxratio":223,"js":1,"geoFetch":1,"ppi":154,"ext":{"fiftyonedegrees_deviceId":""}}}`)),
	)

	mutationResult, err = mutation.Apply(hookstage.RawAuctionRequestPayload(nil))
	assert.NoError(t, err)
	assert.Equal(
		t,
		mutationResult,
		hookstage.RawAuctionRequestPayload([]byte(`{"device":{"devicetype":2,"ua":"ua","make":"Apple","model":"Macbook","os":"MacOs","osv":"14","h":1080,"w":1024,"pxratio":223,"js":1,"geoFetch":1,"ppi":154,"ext":{"fiftyonedegrees_deviceId":""}}}`)),
	)
}

func TestConfigHashFromConfig(t *testing.T) {
	cfg := Config{
		Performance: Performance{
			Profile:        "",
			Concurrency:    nil,
			Difference:     nil,
			AllowUnmatched: nil,
			Drift:          nil,
		},
	}

	result := configHashFromConfig(&cfg)
	assert.Equal(t, result.PerformanceProfile(), dd.Default)
	assert.Equal(t, result.Concurrency(), uint16(0xa))
	assert.Equal(t, result.Difference(), int32(0))
	assert.Equal(t, result.AllowUnmatched(), false)
	assert.Equal(t, result.Drift(), int32(0))

	concurrency := 1
	difference := 1
	allowUnmatched := true
	drift := 1

	cfg = Config{
		Performance: Performance{
			Profile:        "Balanced",
			Concurrency:    &concurrency,
			Difference:     &difference,
			AllowUnmatched: &allowUnmatched,
			Drift:          &drift,
		},
	}

	result = configHashFromConfig(&cfg)
	assert.Equal(t, result.PerformanceProfile(), dd.Balanced)
	assert.Equal(t, result.Concurrency(), uint16(1))
	assert.Equal(t, result.Difference(), int32(1))
	assert.Equal(t, result.AllowUnmatched(), true)
	assert.Equal(t, result.Drift(), int32(1))

	cfg = Config{
		Performance: Performance{
			Profile: "InMemory",
		},
	}
	result = configHashFromConfig(&cfg)
	assert.Equal(t, result.PerformanceProfile(), dd.InMemory)

	cfg = Config{
		Performance: Performance{
			Profile: "HighPerformance",
		},
	}
	result = configHashFromConfig(&cfg)
	assert.Equal(t, result.PerformanceProfile(), dd.HighPerformance)
}

func TestSignDeviceData(t *testing.T) {

	payload := []byte(`{}`)

	deviceInfo := DeviceInfo{
		DeviceId: "test-device-id",
	}

	result, err := signDeviceData(
		payload, &deviceInfo, map[string]any{
			"my-key": "my-value",
		},
	)
	assert.NoError(t, err)

	assert.Equal(
		t,
		result,
		[]byte(`{"device":{"ext":{"fiftyonedegrees_deviceId":"test-device-id","my-key":"my-value"}}}`),
	)
}

func TestBuilderWithInvalidJson(t *testing.T) {
	_, err := Builder([]byte(`{`), moduledeps.ModuleDeps{})
	assert.Error(t, err)
	assert.Errorf(t, err, "failed to parse config")
}

func TestBuilderWithInvalidConfig(t *testing.T) {
	_, err := Builder([]byte(`{"data_file":{}}`), moduledeps.ModuleDeps{})
	assert.Error(t, err)
	assert.Errorf(t, err, "invalid config")
}

func TestBuilderHandleDeviceDetectorError(t *testing.T) {
	var mockConfig Config
	mockConfig.Performance.Profile = "default"
	testFile, _ := os.Create("test-builder-config.hash")
	defer testFile.Close()
	defer os.Remove("test-builder-config.hash")

	_, err := Builder(
		[]byte(`{ 
"enabled": true,
          "data_file": {
            "path": "test-builder-config.hash",
            "update": {
              "auto": true,
              "url": "https://my.datafile.com/datafile.gz",
              "polling_interval": 3600,
              "licence_key": "your_licence_key",
              "product": "V4Enterprise"
            }
          },
          "account_filter": {"allow_list": ["123"]},
				  "performance": {
					"profile": "123",
					"concurrency": 1,
					"difference": 1,
					"allow_unmatched": true,
					"drift": 1	
				  }
}`), moduledeps.ModuleDeps{},
	)
	assert.Error(t, err)
	assert.Errorf(t, err, "failed to create device detector")
}

type mockDeviceMapper struct {
	mock.Mock
}

func (m *mockDeviceMapper) HydratePPI(payload hookstage.RawAuctionRequestPayload) ([]byte, error) {
	args := m.Called(payload)

	res := args.Get(0)
	if res == nil {
		return nil, args.Error(1)
	}

	return args.Get(0).([]byte), args.Error(1)
}

func (m *mockDeviceMapper) HydrateDeviceType(payload hookstage.RawAuctionRequestPayload) ([]byte, error) {
	args := m.Called(payload)

	res := args.Get(0)
	if res == nil {
		return nil, args.Error(1)
	}

	return args.Get(0).([]byte), args.Error(1)
}

func (m *mockDeviceMapper) HydrateUserAgent(payload hookstage.RawAuctionRequestPayload) ([]byte, error) {
	args := m.Called(payload)

	res := args.Get(0)
	if res == nil {
		return nil, args.Error(1)
	}

	return args.Get(0).([]byte), args.Error(1)
}

func (m *mockDeviceMapper) HydrateMake(device hookstage.RawAuctionRequestPayload) ([]byte, error) {
	args := m.Called(device)

	res := args.Get(0)
	if res == nil {
		return nil, args.Error(1)
	}

	return args.Get(0).([]byte), args.Error(1)
}

func (m *mockDeviceMapper) HydrateModel(payload hookstage.RawAuctionRequestPayload, extMap map[string]any) ([]byte, error) {
	args := m.Called(payload, extMap)

	res := args.Get(0)
	if res == nil {
		return nil, args.Error(1)
	}

	return args.Get(0).([]byte), args.Error(1)
}

func (m *mockDeviceMapper) HydrateOS(payload hookstage.RawAuctionRequestPayload) ([]byte, error) {
	args := m.Called(payload)

	res := args.Get(0)
	if res == nil {
		return nil, args.Error(1)
	}

	return args.Get(0).([]byte), args.Error(1)
}

func (m *mockDeviceMapper) HydrateOSVersion(payload hookstage.RawAuctionRequestPayload) ([]byte, error) {
	args := m.Called(payload)

	res := args.Get(0)
	if res == nil {
		return nil, args.Error(1)
	}

	return args.Get(0).([]byte), args.Error(1)
}

func (m *mockDeviceMapper) HydrateScreenHeight(payload hookstage.RawAuctionRequestPayload) ([]byte, error) {
	args := m.Called(payload)

	res := args.Get(0)
	if res == nil {
		return nil, args.Error(1)
	}

	return args.Get(0).([]byte), args.Error(1)
}

func (m *mockDeviceMapper) HydrateScreenWidth(payload hookstage.RawAuctionRequestPayload) ([]byte, error) {
	args := m.Called(payload)

	res := args.Get(0)
	if res == nil {
		return nil, args.Error(1)
	}

	return args.Get(0).([]byte), args.Error(1)
}

func (m *mockDeviceMapper) HydratePixelRatio(payload hookstage.RawAuctionRequestPayload) ([]byte, error) {
	args := m.Called(payload)

	res := args.Get(0)
	if res == nil {
		return nil, args.Error(1)
	}

	return args.Get(0).([]byte), args.Error(1)
}

func (m *mockDeviceMapper) HydrateJavascript(payload hookstage.RawAuctionRequestPayload) ([]byte, error) {
	args := m.Called(payload)

	res := args.Get(0)
	if res == nil {
		return nil, args.Error(1)
	}

	return args.Get(0).([]byte), args.Error(1)
}

func (m *mockDeviceMapper) HydrateGeoLocation(payload hookstage.RawAuctionRequestPayload) ([]byte, error) {
	args := m.Called(payload)

	res := args.Get(0)
	if res == nil {
		return nil, args.Error(1)
	}

	return args.Get(0).([]byte), args.Error(1)
}

func TestHydrateFieldsErrors(t *testing.T) {
	var deviceMapper mockDeviceMapper

	deviceMapper.On("HydrateDeviceType", mock.Anything).Return(nil, errors.New("error"))
	deviceMapper.On("HydrateUserAgent", mock.Anything).Return(nil, errors.New("error"))
	deviceMapper.On("HydrateMake", mock.Anything).Return(nil, errors.New("error"))
	deviceMapper.On("HydrateModel", mock.Anything, mock.Anything).Return(nil, errors.New("error"))
	deviceMapper.On("HydrateOS", mock.Anything).Return(nil, errors.New("error"))
	deviceMapper.On("HydrateOSVersion", mock.Anything).Return(nil, errors.New("error"))
	deviceMapper.On("HydrateScreenHeight", mock.Anything).Return(nil, errors.New("error"))
	deviceMapper.On("HydrateScreenWidth", mock.Anything).Return(nil, errors.New("error"))
	deviceMapper.On("HydratePixelRatio", mock.Anything).Return(nil, errors.New("error"))
	deviceMapper.On("HydrateJavascript", mock.Anything).Return(nil, errors.New("error"))
	deviceMapper.On("HydrateGeoLocation", mock.Anything).Return(nil, errors.New("error"))
	deviceMapper.On("HydratePPI", mock.Anything).Return(nil, errors.New("error"))

	deviceInfo := &DeviceInfo{
		HardwareVendor:        "Apple",
		HardwareName:          "Macbook",
		DeviceType:            "device",
		PlatformVendor:        "Apple",
		PlatformName:          "MacOs",
		PlatformVersion:       "14",
		BrowserVendor:         "Google",
		BrowserName:           "Crome",
		BrowserVersion:        "12",
		ScreenPixelsWidth:     1024,
		ScreenPixelsHeight:    1080,
		PixelRatio:            223,
		Javascript:            true,
		GeoLocation:           true,
		HardwareFamily:        "Macbook",
		HardwareModel:         "Macbook",
		HardwareModelVariants: "Macbook",
		UserAgent:             "ua",
		DeviceId:              "dev-ide",
	}

	_, err := hydrateFields(deviceInfo, &deviceMapper, []byte{})

	assert.Error(t, err)
	assert.Errorf(t, err, "error hydrating device type error")
}
