package growatt

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func apiResponse(t *testing.T, data any) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"data":       data,
		"error_code": 0,
		"error_msg":  "",
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func newTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	client, err := NewClient(srv.URL, "test-token", dir, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestListPlants(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write(apiResponse(t, map[string]any{
			"count": 1,
			"plants": []map[string]any{
				{
					"plant_id":   "12345",
					"plant_name": "Home Solar",
					"city":       "Sydney",
					"country":    "Australia",
				},
			},
		}))
	})

	result, err := client.ListPlants()
	if err != nil {
		t.Fatal(err)
	}
	if result.Count != 1 {
		t.Errorf("count = %d, want 1", result.Count)
	}
	if len(result.Plants) != 1 {
		t.Fatalf("got %d plants, want 1", len(result.Plants))
	}
	if result.Plants[0].DisplayName() != "Home Solar" {
		t.Errorf("name = %q", result.Plants[0].DisplayName())
	}
}

func TestListDevices(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write(apiResponse(t, map[string]any{
			"count": 2,
			"devices": []map[string]any{
				{"device_sn": "INV001", "type": "5", "model": "SPH5000"},
				{"device_sn": "INV002", "type": "7", "model": "MIN3000"},
			},
		}))
	})

	result, err := client.ListDevices(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Devices) != 2 {
		t.Fatalf("got %d devices, want 2", len(result.Devices))
	}
	if result.Devices[0].DeviceSN != "INV001" {
		t.Errorf("device 0 SN = %q", result.Devices[0].DeviceSN)
	}
	if result.Devices[1].DeviceTypeInt() != 7 {
		t.Errorf("device 1 type = %d, want 7", result.Devices[1].DeviceTypeInt())
	}
}

func TestGetDeviceLastData_Dispatch(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write(apiResponse(t, map[string]any{
			"ppv": 1500, "soc": 80,
		}))
	})

	// SPH/MIX type 5
	data, err := client.GetDeviceLastData(Device{DeviceSN: "MIX001", Type: FlexNumber("5")})
	if err != nil {
		t.Fatal(err)
	}
	if data["soc"] != float64(80) {
		t.Errorf("soc = %v", data["soc"])
	}

	// MIN/TLX type 7
	data, err = client.GetDeviceLastData(Device{DeviceSN: "TLX001", Type: FlexNumber("7")})
	if err != nil {
		t.Fatal(err)
	}
	if data["ppv"] != float64(1500) {
		t.Errorf("ppv = %v", data["ppv"])
	}

	// Unsupported type
	_, err = client.GetDeviceLastData(Device{DeviceSN: "UNK001", Type: FlexNumber("99")})
	if err == nil {
		t.Error("expected error for unsupported device type")
	}
}

func TestGetDeviceHistory_UnsupportedType(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		// Should not be called
		t.Error("unexpected HTTP call")
	})

	_, err := client.GetDeviceHistory(Device{DeviceSN: "X", Type: FlexNumber("3")}, "2026-03-27", "2026-03-27")
	if err == nil {
		t.Error("expected error for unsupported type")
	}
}

func TestGetMixHistory_Pagination(t *testing.T) {
	page := 0
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		page++
		var datas []map[string]any
		count := 3

		if page == 1 {
			datas = []map[string]any{
				{"time": "2026-03-27 10:00:00"},
				{"time": "2026-03-27 10:05:00"},
			}
		} else {
			datas = []map[string]any{
				{"time": "2026-03-27 10:10:00"},
			}
		}

		rawDatas, _ := json.Marshal(datas)
		resp := map[string]any{
			"count":              count,
			"datas":              json.RawMessage(rawDatas),
			"mix_sn":            "MIX001",
			"next_page_start_id": 0,
		}
		w.Write(apiResponse(t, resp))
	})

	datas, err := client.GetMixHistory("MIX001", "2026-03-27", "2026-03-27")
	if err != nil {
		t.Fatal(err)
	}
	if len(datas) != 3 {
		t.Errorf("got %d data points, want 3", len(datas))
	}
}
