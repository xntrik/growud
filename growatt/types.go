package growatt

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// FlexNumber handles Growatt's inconsistent API where numeric fields may be
// a JSON number (123), a quoted number ("123"), or an empty string ("").
type FlexNumber string

func (f *FlexNumber) UnmarshalJSON(b []byte) error {
	// Remove quotes if present
	s := strings.Trim(string(b), "\"")
	*f = FlexNumber(s)
	return nil
}

func (f FlexNumber) String() string {
	if f == "" {
		return "0"
	}
	return string(f)
}

func (f FlexNumber) Int() int {
	v, _ := strconv.Atoi(string(f))
	return v
}

func (f FlexNumber) Float64() float64 {
	v, _ := strconv.ParseFloat(string(f), 64)
	return v
}

func (f FlexNumber) MarshalJSON() ([]byte, error) {
	if f == "" {
		return []byte("0"), nil
	}
	return json.Marshal(string(f))
}

// APIResponse is the universal response envelope for all V1 endpoints.
type APIResponse struct {
	Data      any    `json:"data"`
	ErrorCode int    `json:"error_code"`
	ErrorMsg  string `json:"error_msg"`
}

type PlantListData struct {
	Count  int     `json:"count"`
	Plants []Plant `json:"plants"`
}

type Plant struct {
	PlantID         FlexNumber `json:"plant_id"`
	PlantName       string     `json:"plant_name"`
	Name            string     `json:"name"`
	PlantStatus     FlexNumber `json:"status"`
	TotalEnergy     FlexNumber `json:"total_energy"`
	CurrentPower    FlexNumber `json:"current_power"`
	City            string     `json:"city"`
	Country         string     `json:"country"`
	CreateDate      string     `json:"create_date"`
	ImageURL        string     `json:"plant_image_url"`
	PeakPowerActual FlexNumber `json:"peak_power_actual"`
}

// DisplayName returns the best available name for the plant.
func (p Plant) DisplayName() string {
	if p.PlantName != "" {
		return p.PlantName
	}
	if p.Name != "" {
		return p.Name
	}
	return fmt.Sprintf("Plant %d", p.PlantID.Int())
}

type PlantEnergyData struct {
	TodayEnergy  FlexNumber `json:"today_energy"`
	TotalEnergy  FlexNumber `json:"total_energy"`
	CurrentPower FlexNumber `json:"current_power"`
	MonthEnergy  FlexNumber `json:"month_energy"`
	YearEnergy   FlexNumber `json:"year_energy"`
}

type DeviceListData struct {
	Count   int      `json:"count"`
	Devices []Device `json:"devices"`
}

type Device struct {
	DeviceSN       string     `json:"device_sn"`
	DeviceID       FlexNumber `json:"device_id"`
	DataloggerSN   string     `json:"datalogger_sn"`
	Type           FlexNumber `json:"type"`
	Model          string     `json:"model"`
	Status         FlexNumber `json:"status"`
	Lost           bool       `json:"lost"`
	Manufacturer   string     `json:"manufacturer"`
	LastUpdateTime string     `json:"last_update_time"`
}

// DeviceTypeInt returns the device type as an int.
func (d Device) DeviceTypeInt() int {
	return d.Type.Int()
}

// DeviceTypeName returns a human-readable name for the device type ID.
func DeviceTypeName(t int) string {
	switch t {
	case 1:
		return "Inverter"
	case 2:
		return "Storage"
	case 3:
		return "Other"
	case 4:
		return "MAX"
	case 5:
		return "SPH/MIX (Hybrid)"
	case 6:
		return "SPA"
	case 7:
		return "MIN/TLX"
	case 8:
		return "PCS"
	case 9:
		return "HPS"
	case 10:
		return "PBD"
	default:
		return "Unknown"
	}
}
