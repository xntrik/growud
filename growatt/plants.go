package growatt

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
)

// ListPlants returns all plants associated with the account.
func (c *Client) ListPlants() (*PlantListData, error) {
	data, err := c.get("plant/list", nil)
	if err != nil {
		return nil, err
	}
	var result PlantListData
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("parsing plant list: %w", err)
	}
	return &result, nil
}

// ListPlantsRaw returns the raw JSON for debugging field names.
func (c *Client) ListPlantsRaw() (json.RawMessage, error) {
	return c.get("plant/list", nil)
}

// GetPlantEnergy returns the energy overview for a plant.
func (c *Client) GetPlantEnergy(plantID int) (*PlantEnergyData, error) {
	params := url.Values{"plant_id": {strconv.Itoa(plantID)}}
	data, err := c.get("plant/data", params)
	if err != nil {
		return nil, err
	}
	var result PlantEnergyData
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("parsing plant energy: %w", err)
	}
	return &result, nil
}

// ListDevices returns all devices in a plant.
func (c *Client) ListDevices(plantID int) (*DeviceListData, error) {
	params := url.Values{"plant_id": {strconv.Itoa(plantID)}}
	data, err := c.get("device/list", params)
	if err != nil {
		return nil, err
	}
	var result DeviceListData
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("parsing device list: %w", err)
	}
	return &result, nil
}

// GetMixLastData returns current data for an SPH/MIX hybrid inverter.
func (c *Client) GetMixLastData(deviceSN string) (map[string]any, error) {
	form := url.Values{"mix_sn": {deviceSN}}
	data, err := c.post("device/mix/mix_last_data", form)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("parsing mix last data: %w", err)
	}
	return result, nil
}

// GetTLXLastData returns current data for a MIN/TLX inverter.
func (c *Client) GetTLXLastData(deviceSN string) (map[string]any, error) {
	form := url.Values{"tlx_sn": {deviceSN}}
	data, err := c.post("device/tlx/tlx_last_data", form)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("parsing tlx last data: %w", err)
	}
	return result, nil
}

// GetDeviceLastData fetches current data for a device based on its type.
func (c *Client) GetDeviceLastData(device Device) (map[string]any, error) {
	t := device.DeviceTypeInt()
	switch t {
	case 5:
		return c.GetMixLastData(device.DeviceSN)
	case 7:
		return c.GetTLXLastData(device.DeviceSN)
	default:
		return nil, fmt.Errorf("device type %d (%s) not yet supported", t, DeviceTypeName(t))
	}
}

// MixHistoryResponse represents the response from device/mix/mix_data.
type MixHistoryResponse struct {
	Count            int              `json:"count"`
	Datas            []map[string]any `json:"-"`
	RawDatas         json.RawMessage  `json:"datas"`
	DeviceSN         string           `json:"mix_sn"`
	DataloggerSN     string           `json:"datalogger_sn"`
	NextPageStartID  int              `json:"next_page_start_id"`
}

// GetMixHistory returns historical time-series data for an SPH/MIX device.
// startDate and endDate should be YYYY-MM-DD format, max 7-day range.
// This bypasses the cache since historical data changes throughout the day.
func (c *Client) GetMixHistory(deviceSN, startDate, endDate string) ([]map[string]any, error) {
	form := url.Values{
		"mix_sn":     {deviceSN},
		"start_date": {startDate},
		"end_date":   {endDate},
	}

	var allData []map[string]any
	page := 1

	for {
		form.Set("page", strconv.Itoa(page))
		data, err := c.postNoCache("device/mix/mix_data", form)
		if err != nil {
			return nil, fmt.Errorf("fetching mix history: %w", err)
		}

		var resp MixHistoryResponse
		if err := json.Unmarshal(data, &resp); err != nil {
			return nil, fmt.Errorf("parsing mix history: %w", err)
		}

		// Parse the datas array as []map[string]any
		var datas []map[string]any
		if err := json.Unmarshal(resp.RawDatas, &datas); err != nil {
			return nil, fmt.Errorf("parsing mix history datas: %w", err)
		}

		allData = append(allData, datas...)

		// Check if there are more pages
		if len(allData) >= resp.Count || len(datas) == 0 {
			break
		}
		page++
	}

	return allData, nil
}

// GetDeviceHistory fetches historical time-series data for a device.
func (c *Client) GetDeviceHistory(device Device, startDate, endDate string) ([]map[string]any, error) {
	t := device.DeviceTypeInt()
	switch t {
	case 5:
		return c.GetMixHistory(device.DeviceSN, startDate, endDate)
	default:
		return nil, fmt.Errorf("history for device type %d (%s) not yet supported", t, DeviceTypeName(t))
	}
}
