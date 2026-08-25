package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/NimbleMarkets/ntcharts/canvas/runes"
	"github.com/NimbleMarkets/ntcharts/linechart/timeserieslinechart"
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/joho/godotenv"
	"github.com/xntrik/growud/growatt"
	"github.com/xntrik/growud/keystore"
	"github.com/xntrik/growud/notify"
	"github.com/xntrik/growud/server"
	"github.com/xntrik/growud/tariff"
	"github.com/xntrik/growud/tray"
)

const defaultBaseURL = "https://openapi-au.growatt.com/v1/"

var paths AppPaths

func main() {
	paths = resolveAppPaths()

	// Set up file logging when running as .app bundle
	if isAppBundle() && paths.LogPath != "" {
		exe, _ := os.Executable()
		logFile, err := os.OpenFile(paths.LogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err == nil {
			log.SetOutput(logFile)
			// Also redirect stderr so panics are captured
			os.Stderr = logFile
		}
		log.Printf("Growud starting (bundle mode)")
		log.Printf("Executable: %s", exe)
		log.Printf("Data dir: %s", paths.DataDir)
		log.Printf("Cache dir: %s", paths.CacheDir)
		log.Printf("Config: %s", paths.ConfigEnv)
		log.Printf("DB: %s", paths.DBPath)
	}

	log.Printf("Loading config from: %s", paths.ConfigEnv)

	// Load env from resolved config path
	_ = godotenv.Load(paths.ConfigEnv)

	// Token resolution: env var first (covers .env file too), then OS keyring
	token := os.Getenv("GROWATT_TOKEN")
	if token == "" {
		var err error
		token, err = keystore.GetToken()
		if err != nil {
			log.Printf("Warning: keyring access failed: %v", err)
		}
	}
	log.Printf("Token present: %v", token != "")

	baseURL := os.Getenv("GROWATT_BASE_URL")
	if baseURL == "" {
		baseURL = defaultBaseURL
	}

	verbose := os.Getenv("GROWATT_VERBOSE") == "1"

	// Subcommand routing
	cmd := ""
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	log.Printf("Args: %v, initial cmd: %q", os.Args, cmd)

	// Auto-detect tray mode when launched as .app from Finder
	if cmd == "" && isAppBundle() {
		cmd = "tray"
	}
	log.Printf("Final cmd: %q", cmd)

	// Token is required for most commands — but for .app tray mode,
	// we defer the check so the tray can prompt for it
	if token == "" && cmd != "tray" {
		log.Printf("No token and not tray mode, exiting")
		fmt.Fprintln(os.Stderr, "Error: GROWATT_TOKEN is not set. Export it as an environment variable or run the tray app to save it to the OS keychain.")
		os.Exit(1)
	}

	log.Printf("Routing to command: %q", cmd)

	// Subcommand args — safe slice even when cmd was auto-detected
	var subArgs []string
	if len(os.Args) > 2 {
		subArgs = os.Args[2:]
	}

	switch cmd {
	case "collect":
		runCollect(baseURL, token, verbose, subArgs)
	case "chart":
		runChart(subArgs)
	case "cost":
		runCost(subArgs)
	case "notify":
		runNotify(baseURL, token, subArgs)
	case "serve":
		runServe(baseURL, token, subArgs)
	case "tray":
		runTray(baseURL, token, subArgs)
	default:
		runSummary(baseURL, token, verbose)
	}
}

func envInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

func newClient(baseURL, token string) *growatt.Client {
	client, err := growatt.NewClient(baseURL, token, paths.CacheDir, 0)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error initializing client: %v\n", err)
		os.Exit(1)
	}
	return client
}

func runCollect(baseURL, token string, verbose bool, args []string) {
	fs := flag.NewFlagSet("collect", flag.ExitOnError)
	dateFlag := fs.String("date", "", "Collect data for a specific date (YYYY-MM-DD)")
	fromFlag := fs.String("from", "", "Start date for range collection (YYYY-MM-DD)")
	toFlag := fs.String("to", "", "End date for range collection (YYYY-MM-DD)")
	fs.Parse(args)

	// Determine date range
	today := time.Now().Format("2006-01-02")
	startDate, endDate := today, today

	if *dateFlag != "" {
		startDate = *dateFlag
		endDate = *dateFlag
	} else if *fromFlag != "" || *toFlag != "" {
		if *fromFlag == "" || *toFlag == "" {
			fmt.Fprintln(os.Stderr, "Error: both --from and --to are required for range collection.")
			os.Exit(1)
		}
		startDate = *fromFlag
		endDate = *toFlag
	}

	// Validate dates
	start, err := time.Parse("2006-01-02", startDate)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: invalid start date %q (use YYYY-MM-DD)\n", startDate)
		os.Exit(1)
	}
	end, err := time.Parse("2006-01-02", endDate)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: invalid end date %q (use YYYY-MM-DD)\n", endDate)
		os.Exit(1)
	}
	if end.Before(start) {
		fmt.Fprintln(os.Stderr, "Error: end date is before start date.")
		os.Exit(1)
	}

	client := newClient(baseURL, token)

	store, err := growatt.NewStore(paths.DBPath, paths.ReadingsDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error opening store: %v\n", err)
		os.Exit(1)
	}
	defer store.Close()

	// Get list of supported devices
	devices := discoverDevices(client, verbose)
	if len(devices) == 0 {
		fmt.Println("No supported devices found.")
		return
	}

	// Chunk date range into 7-day windows
	chunks := chunkDateRange(start, end, 7)

	for _, device := range devices {
		devType := device.DeviceTypeInt()
		totalInserted, totalUpdated := 0, 0

		for _, chunk := range chunks {
			if verbose {
				fmt.Printf("  Fetching %s: %s to %s\n", device.DeviceSN, chunk.start, chunk.end)
			}

			datas, err := client.GetDeviceHistory(device, chunk.start, chunk.end)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error fetching history for %s (%s to %s): %v\n",
					device.DeviceSN, chunk.start, chunk.end, err)
				continue
			}

			if len(datas) == 0 {
				if verbose {
					fmt.Printf("  No data for %s to %s\n", chunk.start, chunk.end)
				}
				continue
			}

			inserted, updated, err := store.UpsertReadings(device.DeviceSN, devType, datas)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error storing readings for %s: %v\n", device.DeviceSN, err)
				continue
			}

			totalInserted += inserted
			totalUpdated += updated

			// Archive raw data per day within the chunk
			store.ArchiveDayRaw(device.DeviceSN, chunk.start+"_"+chunk.end, datas)
		}

		total := totalInserted + totalUpdated
		if startDate == endDate {
			fmt.Printf("%s (%s): %d readings — %d new, %d updated\n",
				device.DeviceSN, startDate, total, totalInserted, totalUpdated)
		} else {
			fmt.Printf("%s (%s to %s): %d readings — %d new, %d updated\n",
				device.DeviceSN, startDate, endDate, total, totalInserted, totalUpdated)
		}
	}
}

// --- Cost subcommand ---

func runCost(args []string) {
	fs := flag.NewFlagSet("cost", flag.ExitOnError)
	dateFlag := fs.String("date", "", "Calculate cost for a specific date (YYYY-MM-DD)")
	fromFlag := fs.String("from", "", "Start date (YYYY-MM-DD)")
	toFlag := fs.String("to", "", "End date (YYYY-MM-DD)")
	deviceFlag := fs.String("device", "", "Device serial number (auto-detected if omitted)")
	tariffFlag := fs.String("tariff", paths.TariffPath, "Path to tariff config JSON file")
	fs.Parse(args)

	// Determine date range
	today := time.Now().Format("2006-01-02")
	startDate, endDate := today, today

	if *dateFlag != "" {
		startDate = *dateFlag
		endDate = *dateFlag
	} else if *fromFlag != "" || *toFlag != "" {
		if *fromFlag == "" || *toFlag == "" {
			fmt.Fprintln(os.Stderr, "Error: both --from and --to are required for range calculation.")
			os.Exit(1)
		}
		startDate = *fromFlag
		endDate = *toFlag
	}

	// Validate dates
	if _, err := time.Parse("2006-01-02", startDate); err != nil {
		fmt.Fprintf(os.Stderr, "Error: invalid start date %q (use YYYY-MM-DD)\n", startDate)
		os.Exit(1)
	}
	if _, err := time.Parse("2006-01-02", endDate); err != nil {
		fmt.Fprintf(os.Stderr, "Error: invalid end date %q (use YYYY-MM-DD)\n", endDate)
		os.Exit(1)
	}

	// Load tariff config
	cfg, err := tariff.LoadConfig(*tariffFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading tariff config: %v\n", err)
		os.Exit(1)
	}

	// Open store
	store, err := growatt.NewStore(paths.DBPath, paths.ReadingsDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error opening store: %v\n", err)
		os.Exit(1)
	}
	defer store.Close()

	// Auto-detect device if not specified
	deviceSN := *deviceFlag
	if deviceSN == "" {
		sns, err := store.ListDeviceSNs()
		if err != nil || len(sns) == 0 {
			fmt.Fprintln(os.Stderr, "No devices found in database. Run 'growud collect' first.")
			os.Exit(1)
		}
		deviceSN = sns[0]
	}

	calc := tariff.NewCalculator(cfg, store)
	result, err := calc.Calculate(deviceSN, startDate, endDate)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error calculating costs: %v\n", err)
		os.Exit(1)
	}

	printCostResult(result)
}

func printCostResult(r *tariff.CostResult) {
	dateRange := r.From
	if r.From != r.To {
		dateRange = r.From + " to " + r.To
	}
	fmt.Printf("Grid Cost Summary: %s (%s)\n", dateRange, r.Timezone)
	fmt.Printf("Device: %s\n\n", r.DeviceSN)

	printDirectionTable("Import (cost)", r.Import, r.Currency)
	fmt.Println()
	printDirectionTable("Export (credit)", r.Export, r.Currency)

	fmt.Println()
	netDollars := r.NetCents / 100.0
	if r.NetCents >= 0 {
		fmt.Printf("  Net: %s $%.2f cost\n", r.Currency, netDollars)
	} else {
		fmt.Printf("  Net: %s $%.2f credit\n", r.Currency, -netDollars)
	}
}

func printDirectionTable(label string, dr tariff.DirectionResult, currency string) {
	fmt.Printf("  %s\n", label)
	fmt.Printf("  %-24s %10s %10s %10s\n", "Window", "kWh", "Rate", "Amount")
	fmt.Printf("  %s\n", strings.Repeat("-", 56))

	for _, w := range dr.Windows {
		if w.KWh == 0 {
			continue
		}
		fmt.Printf("  %-24s %10.2f %8.1fc %8s$%.2f\n",
			w.Name, w.KWh, w.CentsPerKWh, currency+" ", w.CostCents/100.0)
	}
	fmt.Printf("  %s\n", strings.Repeat("-", 56))
	fmt.Printf("  %-24s %10.2f %10s %8s$%.2f\n",
		"Total", dr.TotalKWh, "", currency+" ", dr.TotalCents/100.0)
}

type dateChunk struct {
	start, end string
}

func chunkDateRange(start, end time.Time, maxDays int) []dateChunk {
	var chunks []dateChunk
	cursor := start

	for !cursor.After(end) {
		chunkEnd := cursor.AddDate(0, 0, maxDays-1)
		if chunkEnd.After(end) {
			chunkEnd = end
		}
		chunks = append(chunks, dateChunk{
			start: cursor.Format("2006-01-02"),
			end:   chunkEnd.Format("2006-01-02"),
		})
		cursor = chunkEnd.AddDate(0, 0, 1)
	}

	return chunks
}

// discoverDevices returns the supported devices, exiting on failure. Callers
// that must survive a transient API error — anything running in a loop —
// should use discoverDevicesErr instead.
func discoverDevices(client *growatt.Client, verbose bool) []growatt.Device {
	devices, err := discoverDevicesErr(client, verbose)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	return devices
}

func discoverDevicesErr(client *growatt.Client, verbose bool) ([]growatt.Device, error) {
	plantList, err := client.ListPlants()
	if err != nil {
		return nil, fmt.Errorf("listing plants: %w", err)
	}

	var devices []growatt.Device
	for _, plant := range plantList.Plants {
		deviceList, err := client.ListDevices(plant.PlantID.Int())
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not list devices for plant %s: %v\n", plant.DisplayName(), err)
			continue
		}
		for _, device := range deviceList.Devices {
			t := device.DeviceTypeInt()
			if t == 5 || t == 7 { // supported types
				devices = append(devices, device)
			} else if verbose {
				fmt.Fprintf(os.Stderr, "Skipping %s (type %d: %s)\n", device.DeviceSN, t, growatt.DeviceTypeName(t))
			}
		}
	}
	return devices, nil
}

// --- Notify subcommand ---

// runNotify checks a notification config against live data. It is a dry run:
// rules are evaluated and the results printed, but nothing is sent and the
// firing history is left untouched. The tray app is what actually delivers
// notifications. Use -test to send a real message to every target.
func runNotify(baseURL, token string, args []string) {
	fs := flag.NewFlagSet("notify", flag.ExitOnError)
	configFlag := fs.String("config", paths.NotifyPath, "Path to notification config JSON file")
	deviceFlag := fs.String("device", "", "Device serial number (auto-detected if omitted)")
	testFlag := fs.Bool("test", false, "Send a test notification to every configured target and exit")
	fs.Parse(args)

	cfg, err := notify.LoadConfig(*configFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading notification config: %v\n", err)
		os.Exit(1)
	}

	// No state path: a dry run must never consume a rule's edge trigger or
	// start its cooldown.
	engine := notify.NewEngine(cfg, "")
	engine.SetLogger(func(format string, args ...any) {})

	if *testFlag {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		errs := engine.SendTest(ctx)
		for _, err := range errs {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		}
		if len(errs) > 0 {
			os.Exit(1)
		}
		fmt.Printf("Test notification sent to %d target(s).\n", len(cfg.Targets))
		return
	}

	client := newClient(baseURL, token)

	device, data, err := latestDeviceData(client, *deviceFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	snapshot, results := engine.Evaluate(device.DeviceSN, data, time.Now())
	printNotifyDryRun(cfg, *configFlag, snapshot, results)
}

// latestDeviceData fetches current data for one device, picking the first
// supported device when no serial is given.
func latestDeviceData(client *growatt.Client, deviceSN string) (growatt.Device, map[string]any, error) {
	devices, err := discoverDevicesErr(client, false)
	if err != nil {
		return growatt.Device{}, nil, err
	}
	if len(devices) == 0 {
		return growatt.Device{}, nil, fmt.Errorf("no supported devices found")
	}

	for _, device := range devices {
		if deviceSN != "" && device.DeviceSN != deviceSN {
			continue
		}
		data, err := client.GetDeviceLastData(device)
		if err != nil {
			if deviceSN != "" {
				return growatt.Device{}, nil, fmt.Errorf("fetching data for %s: %w", deviceSN, err)
			}
			continue
		}
		return device, data, nil
	}

	if deviceSN != "" {
		return growatt.Device{}, nil, fmt.Errorf("device %s not found", deviceSN)
	}
	return growatt.Device{}, nil, fmt.Errorf("no device returned usable data")
}

func printNotifyDryRun(cfg *notify.Config, configPath string, snapshot notify.Snapshot, results []notify.Result) {
	fmt.Printf("Notification config: %s\n", configPath)
	fmt.Printf("Timezone: %s   Location: %.4f, %.4f\n", cfg.Timezone, cfg.Site.Latitude, cfg.Site.Longitude)
	fmt.Printf("Device: %s at %s (sunrise %s, sunset %s)\n\n",
		snapshot.DeviceSN,
		snapshot.At.Format("2006-01-02 15:04"),
		formatSunTime(snapshot.Sunrise),
		formatSunTime(snapshot.Sunset))

	fmt.Println("  Metrics")
	for _, name := range notify.MetricNames() {
		fmt.Printf("    %-26s %10.2f\n", name, snapshot.Values[name])
	}

	fmt.Printf("\n  Rules (dry run — nothing is sent)\n")
	if len(results) == 0 {
		fmt.Println("    (no rules configured)")
		return
	}

	for _, r := range results {
		fmt.Printf("    %s %s\n", notifyStatusMarker(r), r.Rule.Name)
		fmt.Printf("      when:    %s\n", r.Rule.When.Describe())

		switch {
		case r.Skipped != "":
			fmt.Printf("      skipped: %s\n", r.Skipped)
		case !r.Matched:
			fmt.Printf("      skipped: condition not met\n")
		}

		if r.Matched {
			fmt.Printf("      title:   %s\n", r.Notification.Title)
			fmt.Printf("      message: %s\n", r.Notification.Message)
			fmt.Printf("      targets: %s\n", strings.Join(targetNames(cfg, r.Rule), ", "))
		}
		fmt.Println()
	}
}

// notifyStatusMarker flags whether a rule would notify right now.
func notifyStatusMarker(r notify.Result) string {
	if r.Matched && r.Skipped == "" {
		return "[WOULD NOTIFY]"
	}
	return "[   quiet    ]"
}

func targetNames(cfg *notify.Config, rule *notify.Rule) []string {
	targets := cfg.TargetsFor(rule)
	names := make([]string, 0, len(targets))
	for _, t := range targets {
		names = append(names, fmt.Sprintf("%s (%s)", t.Name, t.Type))
	}
	return names
}

func formatSunTime(t time.Time) string {
	if t.IsZero() {
		return "--:--"
	}
	return t.Format("15:04")
}

// --- Tray subcommand ---

func runTray(baseURL, token string, args []string) {
	log.Printf("runTray: args=%v", args)

	fs := flag.NewFlagSet("tray", flag.ContinueOnError)
	portFlag := fs.Int("port", envInt("GROWUD_PORT", 8080), "Port for web dashboard")
	bindFlag := fs.String("bind", envStr("GROWUD_BIND", "127.0.0.1"), "Address to bind to")
	refreshFlag := fs.Int("refresh", envInt("GROWUD_REFRESH", 5), "Refresh interval in minutes")
	if err := fs.Parse(args); err != nil {
		log.Printf("runTray: flag parse error (ignoring): %v", err)
	}

	if baseURL == "" {
		baseURL = defaultBaseURL
	}

	log.Printf("runTray: creating TrayApp (bind=%s, port=%d, refresh=%d)", *bindFlag, *portFlag, *refreshFlag)

	// Pass token/baseURL/configPath to TrayApp — it will handle
	// the first-run prompt after menuet's event loop is running
	app := tray.NewTrayApp(nil, nil, *bindFlag, *portFlag, *refreshFlag)
	app.SetConfig(baseURL, token, paths.ConfigEnv, paths.DBPath, paths.ReadingsDir, paths.CacheDir, paths.TariffPath)
	app.SetNotifyConfig(paths.NotifyPath, paths.NotifyState)

	log.Printf("runTray: calling app.Run()")
	app.Run() // blocks forever
}

// --- Serve subcommand ---

func envStr(key, fallback string) string {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	return v
}

func runServe(baseURL, token string, args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	portFlag := fs.Int("port", envInt("GROWUD_PORT", 8080), "Port to listen on")
	bindFlag := fs.String("bind", envStr("GROWUD_BIND", "127.0.0.1"), "Address to bind to")
	refreshFlag := fs.Int("refresh", envInt("GROWUD_REFRESH", 5), "Notification evaluation interval in minutes")
	fs.Parse(args)

	client := newClient(baseURL, token)

	store, err := growatt.NewStore(paths.DBPath, paths.ReadingsDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error opening store: %v\n", err)
		os.Exit(1)
	}
	defer store.Close()

	srv, err := server.NewServer(client, store, *bindFlag, *portFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating server: %v\n", err)
		os.Exit(1)
	}

	// Load tariff config if available (optional for serve mode)
	if cfg, err := tariff.LoadConfig(paths.TariffPath); err == nil {
		srv.SetTariffConfig(cfg)
		fmt.Printf("Tariff config loaded from %s\n", paths.TariffPath)
	} else {
		fmt.Fprintf(os.Stderr, "Tariff config not loaded: %v\n", err)
	}

	// Evaluate notification rules in the background, if any are configured.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if cfg, err := notify.LoadConfig(paths.NotifyPath); err != nil {
		fmt.Fprintf(os.Stderr, "Notifications not enabled: %v\n", err)
	} else {
		fmt.Printf("Notifications enabled: %d rule(s), %d target(s) from %s — evaluating every %d min\n",
			len(cfg.Rules), len(cfg.Targets), paths.NotifyPath, *refreshFlag)
		go notifyLoop(ctx, client, notify.NewEngine(cfg, paths.NotifyState), *refreshFlag)
	}

	// Graceful shutdown on SIGINT/SIGTERM
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		s := <-sig
		fmt.Fprintf(os.Stderr, "\nShutting down (signal: %v)...\n", s)
		cancel()
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutCancel()
		srv.Shutdown(shutCtx)
	}()

	if err := srv.Start(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
		os.Exit(1)
	}
}

// notifyLoop evaluates the notification rules on a ticker until ctx is done.
// Every failure here is logged and retried on the next tick — a transient
// Growatt API error must never take the web server down with it.
func notifyLoop(ctx context.Context, client *growatt.Client, engine *notify.Engine, refreshMins int) {
	if refreshMins < 1 {
		refreshMins = 5
	}

	evaluate := func() {
		device, data, err := latestDeviceData(client, "")
		if err != nil {
			log.Printf("Notification check skipped: %v", err)
			return
		}

		runCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()

		_, results := engine.Run(runCtx, device.DeviceSN, data, time.Now())
		for _, r := range results {
			if r.Fired {
				fmt.Printf("Notification sent for rule %q: %s\n", r.Rule.Name, r.Notification.Message)
			}
		}
	}

	evaluate()

	ticker := time.NewTicker(time.Duration(refreshMins) * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			evaluate()
		}
	}
}

// --- Chart subcommand ---

const (
	dsSolar     = "Solar PV"
	dsLoad      = "Load"
	dsDischarge = "Bat Discharge"
	dsCharge    = "Bat Charge"
	dsGridIn    = "Grid Import"
	dsGridEx    = "Grid Export"
)

var (
	solarStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))  // green
	loadStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))   // red
	dischargeStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("5"))   // purple
	chargeStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("13"))  // magenta/pink
	gridInStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("208")) // orange
	gridExStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("4"))   // blue
	axisStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))   // gray
	labelStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))   // cyan
)

type chartModel struct {
	chart    timeserieslinechart.Model
	date     string
	deviceSN string
	points   int
	err      error
	origMinX float64
	origMaxX float64
}

func (m chartModel) Init() tea.Cmd {
	return nil
}

func (m chartModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "0":
			m.chart.SetViewXRange(m.origMinX, m.origMaxX)
			m.chart.DrawAll()
		default:
			// Forward all other keys through the chart's Update handler
			// which handles h/l for pan and +/-  for zoom (via KeyMap)
			m.chart, _ = m.chart.Update(msg)
			m.chart.DrawAll()
		}
	case tea.WindowSizeMsg:
		m.chart.Resize(msg.Width-2, msg.Height-5)
		m.chart.DrawAll()
	}
	return m, nil
}

func (m chartModel) View() string {
	if m.err != nil {
		return fmt.Sprintf("Error: %v\nPress q to quit.\n", m.err)
	}
	if m.points == 0 {
		return fmt.Sprintf("No data for %s on %s.\nRun 'growud collect' first.\nPress q to quit.\n", m.deviceSN, m.date)
	}

	header := lipgloss.NewStyle().Bold(true).Render(
		fmt.Sprintf("  %s — %s — %d readings", m.deviceSN, m.date, m.points))

	legend := fmt.Sprintf("  %s Solar  %s Load  %s Discharge  %s Charge  %s Grid In  %s Grid Out",
		solarStyle.Render("●"),
		loadStyle.Render("●"),
		dischargeStyle.Render("●"),
		chargeStyle.Render("●"),
		gridInStyle.Render("●"),
		gridExStyle.Render("●"))

	help := lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(
		"  h/l: pan | +/-: zoom | 0: reset | q: quit")

	return fmt.Sprintf("%s\n%s\n%s\n%s\n", header, m.chart.View(), legend, help)
}

func runChart(args []string) {
	fs := flag.NewFlagSet("chart", flag.ExitOnError)
	dateFlag := fs.String("date", time.Now().Format("2006-01-02"), "Date to chart (YYYY-MM-DD)")
	deviceFlag := fs.String("device", "", "Device serial number (auto-detected if omitted)")
	fs.Parse(args)

	store, err := growatt.NewStore(paths.DBPath, paths.ReadingsDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error opening store: %v\n", err)
		os.Exit(1)
	}
	defer store.Close()

	// Auto-detect device if not specified
	deviceSN := *deviceFlag
	if deviceSN == "" {
		sns, err := store.ListDeviceSNs()
		if err != nil || len(sns) == 0 {
			fmt.Fprintln(os.Stderr, "No devices found in database. Run 'growud collect' first.")
			os.Exit(1)
		}
		deviceSN = sns[0]
	}

	// Query data
	points, err := store.QueryDayReadings(deviceSN, *dateFlag)

	// Find max Y value for range setting
	maxY := 100.0
	for _, p := range points {
		for _, v := range []float64{p.PPVTotal, p.LoadPower, p.DischargePower, p.ChargePower, p.GridImportPower, p.GridExportPower} {
			if v > maxY {
				maxY = v
			}
		}
	}
	maxY = maxY * 1.1 // 10% headroom

	// Create chart
	width := 100
	height := 20

	chart := timeserieslinechart.New(width, height,
		timeserieslinechart.WithYRange(0, maxY),
		timeserieslinechart.WithAxesStyles(axisStyle, labelStyle),
		timeserieslinechart.WithXLabelFormatter(timeserieslinechart.HourTimeLabelFormatter()),
		timeserieslinechart.WithUpdateHandler(timeserieslinechart.HourUpdateHandler(1)),
		timeserieslinechart.WithStyle(solarStyle),
		timeserieslinechart.WithLineStyle(runes.ThinLineStyle),
		timeserieslinechart.WithDataSetStyle(dsLoad, loadStyle),
		timeserieslinechart.WithDataSetLineStyle(dsLoad, runes.ThinLineStyle),
		timeserieslinechart.WithDataSetStyle(dsDischarge, dischargeStyle),
		timeserieslinechart.WithDataSetLineStyle(dsDischarge, runes.ThinLineStyle),
		timeserieslinechart.WithDataSetStyle(dsCharge, chargeStyle),
		timeserieslinechart.WithDataSetLineStyle(dsCharge, runes.ThinLineStyle),
		timeserieslinechart.WithDataSetStyle(dsGridIn, gridInStyle),
		timeserieslinechart.WithDataSetLineStyle(dsGridIn, runes.ThinLineStyle),
		timeserieslinechart.WithDataSetStyle(dsGridEx, gridExStyle),
		timeserieslinechart.WithDataSetLineStyle(dsGridEx, runes.ThinLineStyle),
	)

	// Add +/- as zoom keys (replacing PgUp/PgDown)
	chart.Canvas.KeyMap.PgUp = key.NewBinding(key.WithKeys("+", "="))
	chart.Canvas.KeyMap.PgDown = key.NewBinding(key.WithKeys("-"))
	chart.Focus()

	// Push data points
	for _, p := range points {
		chart.Push(timeserieslinechart.TimePoint{Time: p.Time, Value: p.PPVTotal})
		chart.PushDataSet(dsLoad, timeserieslinechart.TimePoint{Time: p.Time, Value: p.LoadPower})
		chart.PushDataSet(dsDischarge, timeserieslinechart.TimePoint{Time: p.Time, Value: p.DischargePower})
		chart.PushDataSet(dsCharge, timeserieslinechart.TimePoint{Time: p.Time, Value: p.ChargePower})
		chart.PushDataSet(dsGridIn, timeserieslinechart.TimePoint{Time: p.Time, Value: p.GridImportPower})
		chart.PushDataSet(dsGridEx, timeserieslinechart.TimePoint{Time: p.Time, Value: p.GridExportPower})
	}

	chart.DrawAll()

	m := chartModel{
		chart:    chart,
		date:     *dateFlag,
		deviceSN: deviceSN,
		points:   len(points),
		err:      err,
		origMinX: chart.MinX(),
		origMaxX: chart.MaxX(),
	}

	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error running chart: %v\n", err)
		os.Exit(1)
	}
}

func runSummary(baseURL, token string, verbose bool) {
	client := newClient(baseURL, token)

	if verbose {
		raw, err := client.ListPlantsRaw()
		if err == nil {
			fmt.Printf("--- Raw plant/list response ---\n%s\n---\n\n", string(raw))
		}
	}

	plantList, err := client.ListPlants()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error listing plants: %v\n", err)
		os.Exit(1)
	}

	for _, plant := range plantList.Plants {
		plantID := plant.PlantID.Int()

		fmt.Printf("=== %s ===\n", plant.DisplayName())
		fmt.Printf("  Location:      %s, %s\n", plant.City, plant.Country)
		fmt.Printf("  Status:        %s\n", plantStatus(plant.PlantStatus.Int()))
		fmt.Println()

		deviceList, err := client.ListDevices(plantID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  Warning: could not list devices: %v\n", err)
			continue
		}

		for _, device := range deviceList.Devices {
			devType := device.DeviceTypeInt()

			data, err := client.GetDeviceLastData(device)
			if err != nil {
				if verbose {
					fmt.Printf("  [%s] %s (%s) — %v\n", device.DeviceSN, growatt.DeviceTypeName(devType), device.Model, err)
				}
				continue
			}

			printDeviceSummary(device, data)

			if verbose {
				fmt.Printf("\n    --- All Fields ---\n")
				printDataMap(data, "    ")
			}
		}

		fmt.Println()
	}

	hits, misses := client.CacheStats()
	if hits > 0 {
		fmt.Printf("(cache: %d hit, %d miss — TTL %s)\n", hits, misses, growatt.DefaultCacheTTL)
	}
}

func printDeviceSummary(device growatt.Device, data map[string]any) {
	fmt.Printf("  [%s] %s\n", device.DeviceSN, growatt.MapGetStr(data, "statusText"))
	fmt.Printf("    Last Update:    %s\n", device.LastUpdateTime)
	fmt.Println()

	// Solar PV
	ppv1 := growatt.MapGetFloat(data, "ppv1")
	ppv2 := growatt.MapGetFloat(data, "ppv2")
	ppvTotal := growatt.MapGetFloat(data, "ppv")
	if ppvTotal == 0 {
		ppvTotal = ppv1 + ppv2
	}
	fmt.Printf("    Solar\n")
	fmt.Printf("      PV Total:     %.0f W\n", ppvTotal)
	if ppv1 > 0 || ppv2 > 0 {
		fmt.Printf("      PV1:          %.0f W  (%.1f V)\n", ppv1, growatt.MapGetFloat(data, "vpv1"))
		fmt.Printf("      PV2:          %.0f W  (%.1f V)\n", ppv2, growatt.MapGetFloat(data, "vpv2"))
	}
	fmt.Printf("      Today:        %.1f kWh\n", growatt.MapGetFloat(data, "epvtoday"))
	fmt.Printf("      Lifetime:     %.1f kWh\n", growatt.MapGetFloat(data, "epvTotal"))
	fmt.Println()

	// Battery
	fmt.Printf("    Battery\n")
	fmt.Printf("      SOC:          %.0f%%\n", growatt.MapGetFloat(data, "soc"))
	charge := growatt.MapGetFloat(data, "pcharge1")
	discharge := growatt.MapGetFloat(data, "pdischarge1")
	if charge > 0 {
		fmt.Printf("      Charging:     %.0f W\n", charge)
	} else if discharge > 0 {
		fmt.Printf("      Discharging:  %.0f W\n", discharge)
	} else {
		fmt.Printf("      Power:        Idle\n")
	}
	fmt.Printf("      Voltage:      %.1f V\n", growatt.MapGetFloat(data, "vbat"))
	fmt.Printf("      Temperature:  %.0f°C\n", growatt.MapGetFloat(data, "batteryTemperature"))
	fmt.Printf("      SOH:          %.0f%%\n", growatt.MapGetFloat(data, "bmsSOH"))
	fmt.Printf("      Cycles:       %.0f\n", growatt.MapGetFloat(data, "bmsCycleCnt"))
	fmt.Println()

	// Load / consumption
	fmt.Printf("    Load\n")
	fmt.Printf("      Power:        %.0f W\n", growatt.MapGetFloat(data, "plocalLoadTotal"))
	fmt.Printf("      Today:        %.1f kWh\n", growatt.MapGetFloat(data, "elocalLoadToday"))
	fmt.Printf("      Self Use:     %.1f kWh\n", growatt.MapGetFloat(data, "eselftoday"))
	fmt.Println()

	// Grid interaction
	fmt.Printf("    Grid\n")
	fmt.Printf("      Voltage:      %.1f V  (%.1f Hz)\n", growatt.MapGetFloat(data, "vac1"), growatt.MapGetFloat(data, "fac"))
	fmt.Printf("      Export Today: %.1f kWh\n", growatt.MapGetFloat(data, "etoGridToday"))
	fmt.Printf("      Import Today: %.1f kWh\n", growatt.MapGetFloat(data, "etoUserToday"))
	fmt.Println()

	// Inverter temps
	fmt.Printf("    Inverter\n")
	fmt.Printf("      Temp 1:       %.1f°C\n", growatt.MapGetFloat(data, "temp1"))
	fmt.Printf("      Temp 2:       %.1f°C\n", growatt.MapGetFloat(data, "temp2"))
	fmt.Printf("      Temp 3:       %.1f°C\n", growatt.MapGetFloat(data, "temp3"))
}

func plantStatus(status int) string {
	switch status {
	case 0:
		return "Offline"
	case 1:
		return "Online"
	default:
		return fmt.Sprintf("Unknown (%d)", status)
	}
}

func printDataMap(data map[string]any, indent string) {
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		v := data[k]
		switch val := v.(type) {
		case map[string]any:
			continue
		case string:
			if val == "" {
				continue
			}
		case nil:
			continue
		}
		label := strings.ReplaceAll(k, "_", " ")
		fmt.Printf("%s  %-30s %v\n", indent, label, v)
	}
}
