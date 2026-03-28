package server

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"regexp"
	"time"

	"github.com/xntrik/growud/growatt"
)

var (
	dateRe     = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	deviceSNRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
)

// Server handles the HTTP dashboard.
type Server struct {
	client *growatt.Client
	store  *growatt.Store
	bind   string
	port   int
	tmpl   *template.Template
	srv    *http.Server
}

// NewServer creates a new dashboard server.
// bind is the address to listen on (e.g. "127.0.0.1" or "0.0.0.0").
func NewServer(client *growatt.Client, store *growatt.Store, bind string, port int) (*Server, error) {
	tmpl, err := template.New("dashboard").Parse(dashboardHTML)
	if err != nil {
		return nil, fmt.Errorf("parsing template: %w", err)
	}
	return &Server{
		client: client,
		store:  store,
		bind:   bind,
		port:   port,
		tmpl:   tmpl,
	}, nil
}

// Start begins listening on the configured port.
func (s *Server) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleDashboard)
	mux.HandleFunc("/api/summary", s.handleAPISummary)
	mux.HandleFunc("/api/readings", s.handleAPIReadings)

	addr := fmt.Sprintf("%s:%d", s.bind, s.port)
	fmt.Printf("Growud server listening on http://%s\n", addr)

	s.srv = &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	return s.srv.ListenAndServe()
}

// Shutdown gracefully shuts down the server.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.srv == nil {
		return nil
	}
	return s.srv.Shutdown(ctx)
}

type dashboardData struct {
	PlantName string
	Location  string
	Today     string
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	plantName := "Growud Solar"
	location := ""

	plantList, err := s.client.ListPlants()
	if err == nil && len(plantList.Plants) > 0 {
		p := plantList.Plants[0]
		plantName = p.DisplayName()
		location = fmt.Sprintf("%s, %s", p.City, p.Country)
	}

	data := dashboardData{
		PlantName: plantName,
		Location:  location,
		Today:     time.Now().Format("2006-01-02"),
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	s.tmpl.Execute(w, data)
}

// Summary JSON types

type summaryResponse struct {
	Plant    summaryPlant    `json:"plant"`
	Devices  []summaryDevice `json:"devices"`
	Cache    summaryCache    `json:"cache"`
	LoadedAt time.Time       `json:"loaded_at"`
}

type summaryPlant struct {
	Name    string `json:"name"`
	City    string `json:"city"`
	Country string `json:"country"`
	Status  int    `json:"status"`
}

type summaryDevice struct {
	SN         string       `json:"sn"`
	Type       string       `json:"type"`
	StatusText string       `json:"status_text"`
	LastUpdate string       `json:"last_update"`
	Solar      solarSummary `json:"solar"`
	Battery    batSummary   `json:"battery"`
	Load       loadSummary  `json:"load"`
	Grid       gridSummary  `json:"grid"`
}

type solarSummary struct {
	PVTotal  float64 `json:"pv_total"`
	PV1      float64 `json:"pv1"`
	PV2      float64 `json:"pv2"`
	TodayKWh float64 `json:"today_kwh"`
}

type batSummary struct {
	SOC         float64 `json:"soc"`
	ChargeW     float64 `json:"charge_w"`
	DischargeW  float64 `json:"discharge_w"`
	Voltage     float64 `json:"voltage"`
	Temperature float64 `json:"temperature"`
}

type loadSummary struct {
	Power      float64 `json:"power"`
	TodayKWh   float64 `json:"today_kwh"`
	SelfUseKWh float64 `json:"self_use_kwh"`
}

type gridSummary struct {
	Voltage     float64 `json:"voltage"`
	Frequency   float64 `json:"frequency"`
	ExportToday float64 `json:"export_today"`
	ImportToday float64 `json:"import_today"`
}

type summaryCache struct {
	Hits   int    `json:"hits"`
	Misses int    `json:"misses"`
	TTL    string `json:"ttl"`
}

func (s *Server) handleAPISummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.client.ResetCacheStats()

	resp := summaryResponse{
		LoadedAt: time.Now(),
	}

	plantList, err := s.client.ListPlants()
	if err != nil {
		log.Printf("Error listing plants: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list plants"})
		return
	}

	if len(plantList.Plants) > 0 {
		p := plantList.Plants[0]
		resp.Plant = summaryPlant{
			Name:    p.DisplayName(),
			City:    p.City,
			Country: p.Country,
			Status:  p.PlantStatus.Int(),
		}

		deviceList, err := s.client.ListDevices(p.PlantID.Int())
		if err == nil {
			for _, device := range deviceList.Devices {
				data, err := s.client.GetDeviceLastData(device)
				if err != nil {
					continue
				}

				devType := device.DeviceTypeInt()
				sd := summaryDevice{
					SN:         device.DeviceSN,
					Type:       growatt.DeviceTypeName(devType),
					StatusText: growatt.MapGetStr(data, "statusText"),
					LastUpdate: device.LastUpdateTime,
					Solar: solarSummary{
						PVTotal:  growatt.MapGetFloat(data, "ppv"),
						PV1:      growatt.MapGetFloat(data, "ppv1"),
						PV2:      growatt.MapGetFloat(data, "ppv2"),
						TodayKWh: growatt.MapGetFloat(data, "epvtoday"),
					},
					Battery: batSummary{
						SOC:         growatt.MapGetFloat(data, "soc"),
						ChargeW:     growatt.MapGetFloat(data, "pcharge1"),
						DischargeW:  growatt.MapGetFloat(data, "pdischarge1"),
						Voltage:     growatt.MapGetFloat(data, "vbat"),
						Temperature: growatt.MapGetFloat(data, "batteryTemperature"),
					},
					Load: loadSummary{
						Power:      growatt.MapGetFloat(data, "plocalLoadTotal"),
						TodayKWh:   growatt.MapGetFloat(data, "elocalLoadToday"),
						SelfUseKWh: growatt.MapGetFloat(data, "eselftoday"),
					},
					Grid: gridSummary{
						Voltage:     growatt.MapGetFloat(data, "vac1"),
						Frequency:   growatt.MapGetFloat(data, "fac"),
						ExportToday: growatt.MapGetFloat(data, "etoGridToday"),
						ImportToday: growatt.MapGetFloat(data, "etoUserToday"),
					},
				}
				resp.Devices = append(resp.Devices, sd)
			}
		}
	}

	hits, misses := s.client.CacheStats()
	resp.Cache = summaryCache{
		Hits:   hits,
		Misses: misses,
		TTL:    growatt.DefaultCacheTTL.String(),
	}

	writeJSON(w, http.StatusOK, resp)
}

// Readings JSON types

type readingsResponse struct {
	Device   string         `json:"device"`
	Date     string         `json:"date"`
	Readings []readingPoint `json:"readings"`
}

type readingPoint struct {
	Time      string  `json:"time"` // local time string, no timezone
	Solar     float64 `json:"solar"`
	Load      float64 `json:"load"`
	Discharge float64 `json:"discharge"`
	Charge    float64 `json:"charge"`
	GridIn    float64 `json:"grid_in"`
	GridOut   float64 `json:"grid_out"`
}

func (s *Server) handleAPIReadings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	date := r.URL.Query().Get("date")
	if date == "" {
		date = time.Now().Format("2006-01-02")
	} else if !dateRe.MatchString(date) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid date format, expected YYYY-MM-DD"})
		return
	} else if _, err := time.Parse("2006-01-02", date); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid date value"})
		return
	}

	deviceSN := r.URL.Query().Get("device")
	if deviceSN == "" {
		sns, err := s.store.ListDeviceSNs()
		if err != nil || len(sns) == 0 {
			writeJSON(w, http.StatusOK, readingsResponse{Date: date})
			return
		}
		deviceSN = sns[0]
	} else if !deviceSNRe.MatchString(deviceSN) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid device serial number"})
		return
	}

	points, err := s.store.QueryDayReadings(deviceSN, date)
	if err != nil {
		log.Printf("Error querying readings for %s on %s: %v", deviceSN, date, err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to query readings"})
		return
	}

	resp := readingsResponse{
		Device:   deviceSN,
		Date:     date,
		Readings: make([]readingPoint, 0, len(points)),
	}

	for _, p := range points {
		resp.Readings = append(resp.Readings, readingPoint{
			Time:      p.Time.Format("2006-01-02T15:04:05"),
			Solar:     p.PPVTotal,
			Load:      p.LoadPower,
			Discharge: p.DischargePower,
			Charge:    p.ChargePower,
			GridIn:    p.GridImportPower,
			GridOut:   p.GridExportPower,
		})
	}

	writeJSON(w, http.StatusOK, resp)
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}
