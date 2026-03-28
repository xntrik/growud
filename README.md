# Growud

A monitoring and data collection tool for **Growatt** hybrid solar inverters. Provides a CLI, web dashboard, and native macOS menu bar app for tracking solar generation, battery state, load consumption, and grid interaction.

## Supported Devices

- **SPH/MIX** (Type 5) - Hybrid inverters
- **MIN/TLX** (Type 7) - Compact hybrid inverters

## Features

- **Real-time summary** of all plants and devices (solar power, battery SOC, load, grid metrics, temperatures)
- **Historical data collection** with SQLite storage and raw JSON archival
- **Interactive terminal charts** with pan/zoom controls
- **Web dashboard** with live metrics and Chart.js visualizations
- **macOS menu bar app** with status indicator and embedded web server
- **File-based API cache** to minimize Growatt API calls

## Requirements

- Go 1.25.1+
- A [Growatt OpenAPI](https://openapi.growatt.com/) token
- macOS for the menu bar app (`.app` bundle)

## Quick Start

```bash
# Set your Growatt API token (or put it in a .env file)
export GROWATT_TOKEN=your_token_here

# Build and run
make build
./growud
```

## Usage

```bash
# Show real-time status of all plants/devices (default command)
./growud

# Collect historical data for a specific date
./growud collect -date 2026-03-28

# Collect a date range (automatically chunked into 7-day API windows)
./growud collect -from 2026-03-01 -to 2026-03-28

# Interactive terminal chart
./growud chart -date 2026-03-28 -device YOUR_DEVICE_SN

# Start the web dashboard (localhost only by default)
./growud serve -port 8080

# Listen on all interfaces
./growud serve -bind 0.0.0.0

# Launch macOS menu bar app
./growud tray -port 8080 -refresh 5
```

### Chart Controls

| Key | Action |
|-----|--------|
| `h` / `l` | Pan left / right |
| `+` / `-` | Zoom in / out |
| `0` | Reset view |
| `q` | Quit |

## Configuration

Configuration is loaded from environment variables, which can be set in a `.env` file in the working directory.

| Variable | Description | Default |
|----------|-------------|---------|
| `GROWATT_TOKEN` | API authentication token | *(required)* |
| `GROWATT_BASE_URL` | Growatt API endpoint | `https://openapi-au.growatt.com/v1/` |
| `GROWATT_VERBOSE` | Enable verbose output | `0` |
| `GROWUD_BIND` | Address to bind the web server to | `127.0.0.1` |
| `GROWUD_PORT` | Web dashboard port | `8080` |
| `GROWUD_REFRESH` | Refresh interval (minutes) | `5` |

### Path Resolution

When running as a **CLI**, data files are stored relative to the working directory (`.env`, `.cache/`, `growud.db`).

When running as a **macOS .app bundle**, standard macOS directories are used:

| Type | Path |
|------|------|
| Data | `~/Library/Application Support/Growud/` |
| Cache | `~/Library/Caches/Growud/` |
| Logs | `~/Library/Logs/Growud/` |

## Web Dashboard API

| Endpoint | Description |
|----------|-------------|
| `GET /` | HTML dashboard |
| `GET /api/summary` | JSON plant/device summary with current values |
| `GET /api/readings?date=YYYY-MM-DD&device=SN` | Historical readings for charting |

## Building

```bash
make help       # Show available targets
make test       # Run tests
make build      # Build CLI binary -> growud
make build-app  # Build macOS .app bundle -> Growud.app/
make install    # Install .app to /Applications
make clean      # Remove build artifacts
```

## License

[MIT](LICENSE)
