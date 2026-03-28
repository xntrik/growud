package growatt

import (
	"encoding/json"
	"testing"
)

func TestFlexNumber_UnmarshalJSON(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  FlexNumber
	}{
		{"bare number", `123`, FlexNumber("123")},
		{"quoted number", `"456"`, FlexNumber("456")},
		{"quoted float", `"3.14"`, FlexNumber("3.14")},
		{"empty string", `""`, FlexNumber("")},
		{"zero", `0`, FlexNumber("0")},
		{"quoted zero", `"0"`, FlexNumber("0")},
		{"negative", `"-5"`, FlexNumber("-5")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var f FlexNumber
			if err := json.Unmarshal([]byte(tt.input), &f); err != nil {
				t.Fatalf("UnmarshalJSON(%s) error: %v", tt.input, err)
			}
			if f != tt.want {
				t.Errorf("got %q, want %q", f, tt.want)
			}
		})
	}
}

func TestFlexNumber_String(t *testing.T) {
	tests := []struct {
		input FlexNumber
		want  string
	}{
		{FlexNumber(""), "0"},
		{FlexNumber("42"), "42"},
		{FlexNumber("3.14"), "3.14"},
	}
	for _, tt := range tests {
		if got := tt.input.String(); got != tt.want {
			t.Errorf("FlexNumber(%q).String() = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestFlexNumber_Int(t *testing.T) {
	tests := []struct {
		input FlexNumber
		want  int
	}{
		{FlexNumber(""), 0},
		{FlexNumber("42"), 42},
		{FlexNumber("3.14"), 0}, // Atoi fails on floats
		{FlexNumber("abc"), 0},
	}
	for _, tt := range tests {
		if got := tt.input.Int(); got != tt.want {
			t.Errorf("FlexNumber(%q).Int() = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestFlexNumber_Float64(t *testing.T) {
	tests := []struct {
		input FlexNumber
		want  float64
	}{
		{FlexNumber(""), 0},
		{FlexNumber("42"), 42},
		{FlexNumber("3.14"), 3.14},
		{FlexNumber("abc"), 0},
	}
	for _, tt := range tests {
		if got := tt.input.Float64(); got != tt.want {
			t.Errorf("FlexNumber(%q).Float64() = %f, want %f", tt.input, got, tt.want)
		}
	}
}

func TestFlexNumber_MarshalJSON(t *testing.T) {
	tests := []struct {
		input FlexNumber
		want  string
	}{
		{FlexNumber(""), "0"},
		{FlexNumber("42"), `"42"`},
		{FlexNumber("3.14"), `"3.14"`},
	}
	for _, tt := range tests {
		got, err := tt.input.MarshalJSON()
		if err != nil {
			t.Fatalf("MarshalJSON error: %v", err)
		}
		if string(got) != tt.want {
			t.Errorf("FlexNumber(%q).MarshalJSON() = %s, want %s", tt.input, got, tt.want)
		}
	}
}

func TestFlexNumber_RoundTrip(t *testing.T) {
	type wrapper struct {
		Value FlexNumber `json:"value"`
	}

	// Unmarshal a quoted number, then marshal it back
	input := `{"value":"123"}`
	var w wrapper
	if err := json.Unmarshal([]byte(input), &w); err != nil {
		t.Fatal(err)
	}
	if w.Value != FlexNumber("123") {
		t.Fatalf("unmarshal got %q", w.Value)
	}

	out, err := json.Marshal(w)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `{"value":"123"}` {
		t.Errorf("round-trip got %s", out)
	}
}

func TestPlant_DisplayName(t *testing.T) {
	tests := []struct {
		name      string
		plant     Plant
		wantName  string
	}{
		{
			"prefers PlantName",
			Plant{PlantName: "My Plant", Name: "Other", PlantID: FlexNumber("1")},
			"My Plant",
		},
		{
			"falls back to Name",
			Plant{PlantName: "", Name: "Fallback", PlantID: FlexNumber("2")},
			"Fallback",
		},
		{
			"falls back to Plant ID",
			Plant{PlantName: "", Name: "", PlantID: FlexNumber("99")},
			"Plant 99",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.plant.DisplayName(); got != tt.wantName {
				t.Errorf("DisplayName() = %q, want %q", got, tt.wantName)
			}
		})
	}
}

func TestDeviceTypeName(t *testing.T) {
	tests := []struct {
		typeID int
		want   string
	}{
		{1, "Inverter"},
		{5, "SPH/MIX (Hybrid)"},
		{7, "MIN/TLX"},
		{0, "Unknown"},
		{99, "Unknown"},
	}
	for _, tt := range tests {
		if got := DeviceTypeName(tt.typeID); got != tt.want {
			t.Errorf("DeviceTypeName(%d) = %q, want %q", tt.typeID, got, tt.want)
		}
	}
}

func TestDevice_DeviceTypeInt(t *testing.T) {
	d := Device{Type: FlexNumber("5")}
	if got := d.DeviceTypeInt(); got != 5 {
		t.Errorf("DeviceTypeInt() = %d, want 5", got)
	}
}
