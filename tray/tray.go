package tray

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/caseymrm/menuet"
	"github.com/xntrik/growud/growatt"
	"github.com/xntrik/growud/keystore"
	"github.com/xntrik/growud/server"
)

// TrayApp manages the menu bar application.
type TrayApp struct {
	client      *growatt.Client
	store       *growatt.Store
	bind        string
	port        int
	refreshMins int
	mu          sync.Mutex
	lastData    map[string]any
	device      growatt.Device
	hasData     bool

	// Config for deferred initialization
	baseURL     string
	token       string
	configEnv   string
	dbPath      string
	readingsDir string
	cacheDir    string

	// Shutdown coordination
	cancel context.CancelFunc
	wg     sync.WaitGroup
	srv    *server.Server
}

// NewTrayApp creates a new menu bar application.
func NewTrayApp(client *growatt.Client, store *growatt.Store, bind string, port, refreshMins int) *TrayApp {
	if refreshMins < 1 {
		refreshMins = 5
	}
	return &TrayApp{
		client:      client,
		store:       store,
		bind:        bind,
		port:        port,
		refreshMins: refreshMins,
	}
}

// SetConfig stores configuration for deferred initialization.
// When client/store are nil, Run() will initialize them after the event loop starts,
// prompting for token if needed.
func (t *TrayApp) SetConfig(baseURL, token, configEnv, dbPath, readingsDir, cacheDir string) {
	t.baseURL = baseURL
	t.token = token
	t.configEnv = configEnv
	t.dbPath = dbPath
	t.readingsDir = readingsDir
	t.cacheDir = cacheDir
}

// Run starts the menu bar app. Blocks forever.
func (t *TrayApp) Run() {
	log.Printf("TrayApp.Run() called")

	ctx, cancel := context.WithCancel(context.Background())
	t.cancel = cancel

	// Configure menuet
	menuet.App().Label = "com.github.xntrik.growud"
	menuet.App().Children = t.menuItems
	menuet.App().HideStartup()

	// Register graceful shutdown handler so we clean up before menuet's Quit terminates the app
	menuWg, menuCtx := menuet.App().GracefulShutdownHandles()
	menuWg.Add(1)
	go func() {
		defer menuWg.Done()
		<-menuCtx.Done()
		t.shutdown()
	}()

	log.Printf("Setting initial title")
	t.setTitle("Growud")

	// Shut down cleanly on SIGINT/SIGTERM
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		select {
		case s := <-sig:
			log.Printf("Growud shutting down (signal: %v)", s)
			signal.Stop(sig)
			t.shutdown()
			os.Exit(0)
		case <-ctx.Done():
			signal.Stop(sig)
		}
	}()

	// Initialize in background after event loop starts
	t.wg.Add(1)
	go func() {
		defer t.wg.Done()
		log.Printf("Init goroutine started, waiting for event loop...")
		// Small delay to let the event loop start
		time.Sleep(1 * time.Second)
		log.Printf("Init goroutine: delay complete, initializing...")

		if err := t.initialize(); err != nil {
			log.Printf("Initialization error: %v", err)
			t.setTitle("growud: error")
			return
		}

		// Start HTTP server
		srv, err := server.NewServer(t.client, t.store, t.bind, t.port)
		if err != nil {
			log.Printf("Error creating server: %v", err)
		} else {
			t.srv = srv
			t.wg.Add(1)
			go func() {
				defer t.wg.Done()
				log.Printf("Starting web server on %s:%d", t.bind, t.port)
				if err := srv.Start(); err != nil && !errors.Is(err, http.ErrServerClosed) {
					log.Printf("Server error: %v", err)
				}
			}()
		}

		// Initial fetch and collect
		t.refresh()
		t.collect()

		// Start refresh loop
		t.wg.Add(1)
		go t.refreshLoop(ctx)
	}()

	// Run the app (blocks)
	log.Printf("Calling menuet.App().RunApplication()")
	menuet.App().RunApplication()
	log.Printf("RunApplication() returned (unexpected)")
}

// shutdown cancels all background goroutines and cleans up resources.
func (t *TrayApp) shutdown() {
	log.Printf("Growud shutting down")
	t.cancel()

	// Shut down HTTP server
	if t.srv != nil {
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutCancel()
		if err := t.srv.Shutdown(shutCtx); err != nil {
			log.Printf("HTTP server shutdown error: %v", err)
		}
	}

	// Close the store
	if t.store != nil {
		if err := t.store.Close(); err != nil {
			log.Printf("Store close error: %v", err)
		}
	}

	// Wait for goroutines to finish
	t.wg.Wait()
}

func (t *TrayApp) initialize() error {
	token := t.token

	// Prompt for token if missing
	if token == "" {
		log.Printf("No token found, prompting user")
		token = PromptForToken()
		if token == "" {
			return fmt.Errorf("no token provided")
		}
		t.token = token
	}

	// Create client
	log.Printf("Creating client (base URL: %s)", t.baseURL)
	client, err := growatt.NewClient(t.baseURL, token, t.cacheDir, 0)
	if err != nil {
		return fmt.Errorf("creating client: %w", err)
	}
	t.client = client

	// Create store
	log.Printf("Opening store (DB: %s)", t.dbPath)
	store, err := growatt.NewStore(t.dbPath, t.readingsDir)
	if err != nil {
		return fmt.Errorf("opening store: %w", err)
	}
	t.store = store

	log.Printf("Initialization complete (refresh every %d min)", t.refreshMins)
	return nil
}

func (t *TrayApp) refreshLoop(ctx context.Context) {
	defer t.wg.Done()
	ticker := time.NewTicker(time.Duration(t.refreshMins) * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			t.refresh()
			t.collect()
		}
	}
}

func (t *TrayApp) refresh() {
	if t.client == nil {
		return
	}

	plantList, err := t.client.ListPlants()
	if err != nil {
		log.Printf("Error listing plants: %v", err)
		t.setTitle("growud: error")
		return
	}

	if len(plantList.Plants) == 0 {
		t.setTitle("growud: no plants")
		return
	}

	plant := plantList.Plants[0]
	deviceList, err := t.client.ListDevices(plant.PlantID.Int())
	if err != nil {
		log.Printf("Error listing devices: %v", err)
		t.setTitle("growud: error")
		return
	}

	for _, device := range deviceList.Devices {
		data, err := t.client.GetDeviceLastData(device)
		if err != nil {
			continue
		}

		t.mu.Lock()
		t.lastData = data
		t.device = device
		t.hasData = true
		t.mu.Unlock()

		t.updateTitle(data)
		log.Printf("Refreshed: PV %.0fW, SOC %.0f%%, Load %.0fW",
			growatt.MapGetFloat(data, "ppv"),
			growatt.MapGetFloat(data, "soc"),
			growatt.MapGetFloat(data, "plocalLoadTotal"))
		return
	}

	t.setTitle("growud")
}

func (t *TrayApp) updateTitle(data map[string]any) {
	ppv := growatt.MapGetFloat(data, "ppv")
	soc := growatt.MapGetFloat(data, "soc")
	load := growatt.MapGetFloat(data, "plocalLoadTotal")

	title := ""
	if ppv > 0 {
		title = fmt.Sprintf("☀ %.0fW  ", ppv)
	}
	title += fmt.Sprintf("🔋 %.0f%%  ⚡ %.0fW", soc, load)

	t.setTitle(title)
}

func (t *TrayApp) setTitle(title string) {
	menuet.App().SetMenuState(&menuet.MenuState{
		Title: title,
	})
}

func (t *TrayApp) menuItems() []menuet.MenuItem {
	t.mu.Lock()
	data := t.lastData
	hasData := t.hasData
	device := t.device
	t.mu.Unlock()

	if !hasData {
		return []menuet.MenuItem{
			{Text: "Loading..."},
			menuSeparator(),
			t.openDashboardItem(),
			// t.quitItem(),
		}
	}

	items := []menuet.MenuItem{
		// Status
		{Text: growatt.MapGetStr(data, "statusText"), FontWeight: menuet.WeightBold},
		{Text: fmt.Sprintf("Last Update: %s", device.LastUpdateTime)},
		menuSeparator(),

		// Solar
		{Text: "Solar", FontWeight: menuet.WeightBold},
		{Text: fmt.Sprintf("  PV Total:    %.0f W", growatt.MapGetFloat(data, "ppv"))},
		{Text: fmt.Sprintf("  PV1:         %.0f W", growatt.MapGetFloat(data, "ppv1"))},
		{Text: fmt.Sprintf("  PV2:         %.0f W", growatt.MapGetFloat(data, "ppv2"))},
		{Text: fmt.Sprintf("  Today:       %.1f kWh", growatt.MapGetFloat(data, "epvtoday"))},
		menuSeparator(),

		// Battery
		{Text: "Battery", FontWeight: menuet.WeightBold},
		{Text: fmt.Sprintf("  SOC:         %.0f%%", growatt.MapGetFloat(data, "soc"))},
		t.batteryStatusItem(data),
		{Text: fmt.Sprintf("  Voltage:     %.1f V", growatt.MapGetFloat(data, "vbat"))},
		{Text: fmt.Sprintf("  Temperature: %.0f°C", growatt.MapGetFloat(data, "batteryTemperature"))},
		menuSeparator(),

		// Load
		{Text: "Load", FontWeight: menuet.WeightBold},
		{Text: fmt.Sprintf("  Power:       %.0f W", growatt.MapGetFloat(data, "plocalLoadTotal"))},
		{Text: fmt.Sprintf("  Today:       %.1f kWh", growatt.MapGetFloat(data, "elocalLoadToday"))},
		menuSeparator(),

		// Grid
		{Text: "Grid", FontWeight: menuet.WeightBold},
		{Text: fmt.Sprintf("  Voltage:     %.1f V  (%.1f Hz)", growatt.MapGetFloat(data, "vac1"), growatt.MapGetFloat(data, "fac"))},
		{Text: fmt.Sprintf("  Export Today: %.1f kWh", growatt.MapGetFloat(data, "etoGridToday"))},
		{Text: fmt.Sprintf("  Import Today: %.1f kWh", growatt.MapGetFloat(data, "etoUserToday"))},
		menuSeparator(),

		// Actions
		t.openDashboardItem(),
		t.collectNowItem(),
		// menuSeparator(),
		// t.quitItem(),
	}

	return items
}

func (t *TrayApp) batteryStatusItem(data map[string]any) menuet.MenuItem {
	charge := growatt.MapGetFloat(data, "pcharge1")
	discharge := growatt.MapGetFloat(data, "pdischarge1")
	if charge > 0 {
		return menuet.MenuItem{Text: fmt.Sprintf("  Charging:    %.0f W", charge)}
	}
	if discharge > 0 {
		return menuet.MenuItem{Text: fmt.Sprintf("  Discharging: %.0f W", discharge)}
	}
	return menuet.MenuItem{Text: "  Power:       Idle"}
}

func (t *TrayApp) openDashboardItem() menuet.MenuItem {
	return menuet.MenuItem{
		Text: "Open Dashboard",
		Clicked: func() {
			url := fmt.Sprintf("http://localhost:%d", t.port)
			exec.Command("open", url).Start()
		},
	}
}

func (t *TrayApp) collect() {
	if t.client == nil || t.store == nil {
		return
	}

	today := time.Now().Format("2006-01-02")
	plantList, err := t.client.ListPlants()
	if err != nil {
		log.Printf("Collect error (plants): %v", err)
		return
	}
	for _, plant := range plantList.Plants {
		deviceList, err := t.client.ListDevices(plant.PlantID.Int())
		if err != nil {
			continue
		}
		for _, device := range deviceList.Devices {
			devType := device.DeviceTypeInt()
			if devType != 5 && devType != 7 {
				continue
			}
			datas, err := t.client.GetDeviceHistory(device, today, today)
			if err != nil || len(datas) == 0 {
				continue
			}
			inserted, updated, err := t.store.UpsertReadings(device.DeviceSN, devType, datas)
			if err != nil {
				log.Printf("Collect error (upsert): %v", err)
				continue
			}
			t.store.ArchiveDayRaw(device.DeviceSN, today, datas)
			log.Printf("Collected %s: %d new, %d updated", device.DeviceSN, inserted, updated)
		}
	}
}

func (t *TrayApp) collectNowItem() menuet.MenuItem {
	return menuet.MenuItem{
		Text: "Collect Now",
		Clicked: func() {
			go func() {
				t.collect()
				t.refresh()
				menuet.App().Notification(menuet.Notification{
					Title:    "Growud",
					Subtitle: "Data collection complete",
				})
			}()
		},
	}
}

func menuSeparator() menuet.MenuItem {
	return menuet.MenuItem{
		Type: menuet.Separator,
	}
}

// PromptForToken shows a macOS dialog asking for the Growatt API token
// and saves it to the OS keychain. Returns the token string.
func PromptForToken() string {
	response := menuet.App().Alert(menuet.Alert{
		MessageText:     "Welcome to Growud",
		InformativeText: "Enter your Growatt API token to get started.\nYou can find this in the ShinePhone app under Me > API Token.",
		Inputs:          []string{"API Token"},
		Buttons:         []string{"Save", "Cancel"},
	})

	if response.Button != 0 || len(response.Inputs) == 0 || response.Inputs[0] == "" {
		return ""
	}

	token := response.Inputs[0]

	// Save to OS keychain
	if err := keystore.SetToken(token); err != nil {
		log.Printf("Error saving token to keychain: %v", err)
	}

	// Set in current environment so the running process picks it up
	os.Setenv("GROWATT_TOKEN", token)

	return token
}
