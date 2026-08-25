package notify

import "testing"

func f(v float64) *float64 { return &v }

func snapshotWith(vals map[string]float64) Snapshot {
	return Snapshot{DeviceSN: "TEST", Values: vals}
}

func TestCondition_Leaf(t *testing.T) {
	s := snapshotWith(map[string]float64{"battery_soc": 96})

	tests := []struct {
		op    string
		value float64
		want  bool
	}{
		{">=", 95, true},
		{">=", 97, false},
		{">", 96, false},
		{"<", 100, true},
		{"<=", 96, true},
		{"==", 96, true},
		{"==", 95, false},
		{"!=", 95, true},
	}

	for _, tt := range tests {
		c := Condition{Metric: "battery_soc", Op: tt.op, Value: f(tt.value)}
		if got := c.Eval(s); got != tt.want {
			t.Errorf("battery_soc %s %v = %v, want %v", tt.op, tt.value, got, tt.want)
		}
	}
}

func TestCondition_MissingMetricIsFalse(t *testing.T) {
	c := Condition{Metric: "battery_soc", Op: ">", Value: f(0)}
	if c.Eval(snapshotWith(map[string]float64{})) {
		t.Error("a condition on an absent metric should not hold")
	}
}

func TestCondition_Combinators(t *testing.T) {
	s := snapshotWith(map[string]float64{
		"battery_soc":          96,
		"solar_power":          3200,
		"minutes_until_sunset": 180,
	})

	all := Condition{All: []Condition{
		{Metric: "battery_soc", Op: ">=", Value: f(95)},
		{Metric: "minutes_until_sunset", Op: ">=", Value: f(150)},
		{Metric: "solar_power", Op: ">", Value: f(2000)},
	}}
	if !all.Eval(s) {
		t.Error("all: expected the combined condition to hold")
	}

	allWithOneFalse := Condition{All: []Condition{
		{Metric: "battery_soc", Op: ">=", Value: f(95)},
		{Metric: "solar_power", Op: ">", Value: f(5000)},
	}}
	if allWithOneFalse.Eval(s) {
		t.Error("all: one false leaf should fail the whole condition")
	}

	any := Condition{Any: []Condition{
		{Metric: "battery_soc", Op: ">", Value: f(99)},
		{Metric: "solar_power", Op: ">", Value: f(2000)},
	}}
	if !any.Eval(s) {
		t.Error("any: expected one true leaf to be enough")
	}

	not := Condition{Not: &Condition{Metric: "battery_soc", Op: ">", Value: f(99)}}
	if !not.Eval(s) {
		t.Error("not: expected the negation of a false condition to hold")
	}

	nested := Condition{All: []Condition{
		{Metric: "battery_soc", Op: ">=", Value: f(95)},
		{Any: []Condition{
			{Metric: "solar_power", Op: ">", Value: f(9000)},
			{Metric: "minutes_until_sunset", Op: ">=", Value: f(120)},
		}},
	}}
	if !nested.Eval(s) {
		t.Error("nested: expected the combined condition to hold")
	}
}

func TestCondition_Validate(t *testing.T) {
	tests := []struct {
		name    string
		cond    Condition
		wantErr bool
	}{
		{"valid leaf", Condition{Metric: "battery_soc", Op: ">=", Value: f(95)}, false},
		{"valid all", Condition{All: []Condition{{Metric: "solar_power", Op: ">", Value: f(0)}}}, false},
		{"valid not", Condition{Not: &Condition{Metric: "solar_power", Op: ">", Value: f(0)}}, false},
		{"empty", Condition{}, true},
		{"unknown metric", Condition{Metric: "battery_charge", Op: ">", Value: f(1)}, true},
		{"unknown op", Condition{Metric: "battery_soc", Op: "=>", Value: f(1)}, true},
		{"missing value", Condition{Metric: "battery_soc", Op: ">"}, true},
		{"missing op", Condition{Metric: "battery_soc", Value: f(1)}, true},
		{"mixed branches", Condition{
			Metric: "battery_soc", Op: ">", Value: f(1),
			All: []Condition{{Metric: "solar_power", Op: ">", Value: f(0)}},
		}, true},
		{"invalid nested", Condition{All: []Condition{{Metric: "nope", Op: ">", Value: f(0)}}}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cond.validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestCondition_Describe(t *testing.T) {
	c := Condition{All: []Condition{
		{Metric: "battery_soc", Op: ">=", Value: f(95)},
		{Any: []Condition{
			{Metric: "solar_power", Op: ">", Value: f(2000)},
			{Not: &Condition{Metric: "grid_import_power", Op: ">", Value: f(0)}},
		}},
	}}

	want := "(battery_soc >= 95 AND (solar_power > 2000 OR NOT grid_import_power > 0))"
	if got := c.Describe(); got != want {
		t.Errorf("Describe() = %q, want %q", got, want)
	}
}
