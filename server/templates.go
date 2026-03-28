package server

const dashboardHTML = `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Growud — Solar Dashboard</title>
    <link rel="icon" type="image/png" href="/favicon.png">
    <script src="https://cdn.jsdelivr.net/npm/chart.js@4"></script>
    <script src="https://cdn.jsdelivr.net/npm/chartjs-adapter-date-fns@3"></script>
    <script src="https://cdn.jsdelivr.net/npm/hammerjs@2"></script>
    <script src="https://cdn.jsdelivr.net/npm/chartjs-plugin-zoom@2"></script>
    <style>
        * { margin: 0; padding: 0; box-sizing: border-box; }
        body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif; background: #1a1a2e; color: #e0e0e0; }
        .container { max-width: 1200px; margin: 0 auto; padding: 20px; }
        h1 { font-size: 1.5rem; color: #fff; margin-bottom: 4px; }
        .subtitle { color: #888; font-size: 0.85rem; margin-bottom: 20px; }

        .overview { display: grid; grid-template-columns: repeat(auto-fit, minmax(250px, 1fr)); gap: 16px; margin-bottom: 24px; }
        .card { background: #16213e; border-radius: 10px; padding: 16px; border: 1px solid #1a1a4e; }
        .card h3 { font-size: 0.8rem; text-transform: uppercase; letter-spacing: 1px; color: #888; margin-bottom: 10px; }
        .metric { display: flex; justify-content: space-between; padding: 4px 0; font-size: 0.95rem; }
        .metric .label { color: #aaa; }
        .metric .value { font-weight: 600; }

        .solar .value { color: #4ade80; }
        .battery .value { color: #c084fc; }
        .load .value { color: #f87171; }
        .grid .value { color: #fb923c; }

        .chart-section { background: #16213e; border-radius: 10px; padding: 20px; border: 1px solid #1a1a4e; }
        .chart-header { display: flex; justify-content: space-between; align-items: center; margin-bottom: 16px; }
        .chart-header h2 { font-size: 1.1rem; }
        input[type="date"] {
            background: #1a1a2e; color: #e0e0e0; border: 1px solid #333;
            border-radius: 6px; padding: 6px 12px; font-size: 0.9rem;
        }
        .chart-container { position: relative; height: 400px; }

        .energy-estimates { display: flex; flex-wrap: wrap; gap: 12px; padding: 12px 0 0; border-top: 1px solid rgba(255,255,255,0.05); margin-top: 12px; }
        .energy-estimates .est-item { font-size: 0.85rem; color: #aaa; }
        .energy-estimates .est-value { font-weight: 600; margin-left: 4px; }
        .energy-estimates .est-label { font-size: 0.75rem; color: #666; font-style: italic; margin-left: 8px; }

        .footer { margin-top: 16px; text-align: center; color: #555; font-size: 0.8rem; }
        .cache-info { color: #666; font-size: 0.8rem; margin-top: 4px; }

        .status-badge { display: inline-block; padding: 2px 8px; border-radius: 4px; font-size: 0.8rem; }
        .status-online { background: #065f46; color: #6ee7b7; }
        .status-offline { background: #7f1d1d; color: #fca5a5; }
    </style>
</head>
<body>
    <div class="container">
        <h1>{{.PlantName}}</h1>
        <div class="subtitle">
            {{.Location}} &mdash;
            <span id="loaded-at"></span>
            <span class="cache-info" id="cache-info"></span>
        </div>

        <div class="overview" id="overview">
            <div class="card solar">
                <h3>Solar</h3>
                <div id="solar-metrics">Loading...</div>
            </div>
            <div class="card battery">
                <h3>Battery</h3>
                <div id="battery-metrics">Loading...</div>
            </div>
            <div class="card load">
                <h3>Load</h3>
                <div id="load-metrics">Loading...</div>
            </div>
            <div class="card grid">
                <h3>Grid</h3>
                <div id="grid-metrics">Loading...</div>
            </div>
        </div>

        <div class="chart-section">
            <div class="chart-header">
                <h2>Energy Trend</h2>
                <div>
                    <button id="reset-zoom" style="background:#1a1a2e;color:#e0e0e0;border:1px solid #333;border-radius:6px;padding:6px 12px;font-size:0.9rem;cursor:pointer;margin-right:8px;">Reset Zoom</button>
                    <input type="date" id="date-picker" value="{{.Today}}">
                </div>
            </div>
            <div class="chart-container">
                <canvas id="chart"></canvas>
            </div>
            <div class="energy-estimates" id="energy-estimates"></div>
        </div>

        <div class="footer">Growud &mdash; Growatt Solar Monitor</div>
    </div>

    <script>
    const chartColors = {
        solar:     'rgba(74, 222, 128, 1)',
        load:      'rgba(248, 113, 113, 1)',
        discharge: 'rgba(192, 132, 252, 1)',
        charge:    'rgba(240, 171, 252, 1)',
        gridIn:    'rgba(251, 146, 60, 1)',
        gridOut:   'rgba(96, 165, 250, 1)',
    };

    let chart = null;

    function updateEstimates() {
        if (!chart) return;
        const xScale = chart.scales.x;
        const minT = xScale.min;
        const maxT = xScale.max;
        const el = document.getElementById('energy-estimates');
        const names = ['Solar PV', 'Load', 'Discharge', 'Charge', 'Grid Import', 'Grid Export'];
        const colors = [chartColors.solar, chartColors.load, chartColors.discharge, chartColors.charge, chartColors.gridIn, chartColors.gridOut];
        let html = '';
        chart.data.datasets.forEach(function(ds, i) {
            const pts = ds.data.filter(function(p) {
                var t = new Date(p.x).getTime();
                return t >= minT && t <= maxT;
            });
            var kwh = 0;
            for (var j = 1; j < pts.length; j++) {
                var dt = (new Date(pts[j].x).getTime() - new Date(pts[j-1].x).getTime()) / 3600000;
                kwh += (pts[j-1].y + pts[j].y) / 2 * dt / 1000;
            }
            html += '<span class="est-item"><span style="color:' + colors[i] + '">' + names[i] + ':</span><span class="est-value" style="color:' + colors[i] + '">~' + kwh.toFixed(2) + ' kWh</span></span>';
        });
        html += '<span class="est-label">estimated via trapezoidal integration</span>';
        el.innerHTML = html;
    }

    function metric(label, value, unit) {
        return '<div class="metric"><span class="label">' + label + '</span><span class="value">' + value + (unit || '') + '</span></div>';
    }

    function round(v, d) {
        if (v === undefined || v === null) return '0';
        return Number(v).toFixed(d !== undefined ? d : 0);
    }

    async function loadSummary() {
        try {
            const resp = await fetch('/api/summary');
            const data = await resp.json();

            document.getElementById('loaded-at').textContent = 'Loaded: ' + new Date(data.loaded_at).toLocaleTimeString();
            document.getElementById('cache-info').textContent =
                '(cache: ' + data.cache.hits + ' hit, ' + data.cache.misses + ' miss, TTL ' + data.cache.ttl + ')';

            if (data.devices && data.devices.length > 0) {
                const d = data.devices[0];

                document.getElementById('solar-metrics').innerHTML =
                    metric('PV Total', round(d.solar.pv_total), ' W') +
                    metric('PV1', round(d.solar.pv1), ' W') +
                    metric('PV2', round(d.solar.pv2), ' W') +
                    metric('Today', round(d.solar.today_kwh, 1), ' kWh');

                let battStatus = 'Idle';
                if (d.battery.discharge_w > 0) battStatus = 'Discharging ' + round(d.battery.discharge_w) + ' W';
                else if (d.battery.charge_w > 0) battStatus = 'Charging ' + round(d.battery.charge_w) + ' W';

                document.getElementById('battery-metrics').innerHTML =
                    metric('SOC', round(d.battery.soc), '%') +
                    metric('Status', battStatus) +
                    metric('Voltage', round(d.battery.voltage, 1), ' V') +
                    metric('Temperature', round(d.battery.temperature), '°C');

                document.getElementById('load-metrics').innerHTML =
                    metric('Power', round(d.load.power), ' W') +
                    metric('Today', round(d.load.today_kwh, 1), ' kWh') +
                    metric('Self Use', round(d.load.self_use_kwh, 1), ' kWh');

                document.getElementById('grid-metrics').innerHTML =
                    metric('Voltage', round(d.grid.voltage, 1), ' V') +
                    metric('Frequency', round(d.grid.frequency, 1), ' Hz') +
                    metric('Export Today', round(d.grid.export_today, 1), ' kWh') +
                    metric('Import Today', round(d.grid.import_today, 1), ' kWh');
            }
        } catch (err) {
            console.error('Failed to load summary:', err);
        }
    }

    async function loadChart(date) {
        try {
            const resp = await fetch('/api/readings?date=' + date);
            const data = await resp.json();

            const readings = data.readings || [];

            const datasets = [
                { label: 'Solar PV', data: readings.map(r => ({x: r.time, y: r.solar})), borderColor: chartColors.solar, backgroundColor: 'rgba(74, 222, 128, 0.1)', fill: true, tension: 0.3, pointRadius: 0 },
                { label: 'Load', data: readings.map(r => ({x: r.time, y: r.load})), borderColor: chartColors.load, backgroundColor: 'rgba(248, 113, 113, 0.1)', fill: true, tension: 0.3, pointRadius: 0 },
                { label: 'Discharge', data: readings.map(r => ({x: r.time, y: r.discharge})), borderColor: chartColors.discharge, backgroundColor: 'transparent', tension: 0.3, pointRadius: 0 },
                { label: 'Charge', data: readings.map(r => ({x: r.time, y: r.charge})), borderColor: chartColors.charge, backgroundColor: 'transparent', tension: 0.3, pointRadius: 0 },
                { label: 'Grid Import', data: readings.map(r => ({x: r.time, y: r.grid_in})), borderColor: chartColors.gridIn, backgroundColor: 'transparent', tension: 0.3, pointRadius: 0 },
                { label: 'Grid Export', data: readings.map(r => ({x: r.time, y: r.grid_out})), borderColor: chartColors.gridOut, backgroundColor: 'transparent', tension: 0.3, pointRadius: 0 },
            ];

            if (chart) {
                chart.data.datasets = datasets;
                chart.update();
                updateEstimates();
            } else {
                const ctx = document.getElementById('chart').getContext('2d');
                chart = new Chart(ctx, {
                    type: 'line',
                    data: { datasets: datasets },
                    options: {
                        responsive: true,
                        maintainAspectRatio: false,
                        interaction: { mode: 'index', intersect: false },
                        plugins: {
                            zoom: {
                                zoom: {
                                    wheel: { enabled: true },
                                    pinch: { enabled: true },
                                    drag: {
                                        enabled: true,
                                        modifierKey: 'shift',
                                        backgroundColor: 'rgba(255,255,255,0.1)',
                                        borderColor: 'rgba(255,255,255,0.3)',
                                        borderWidth: 1,
                                    },
                                    mode: 'x',
                                    onZoomComplete: function() { updateEstimates(); },
                                },
                                pan: {
                                    enabled: true,
                                    mode: 'x',
                                    threshold: 5,
                                    onPanComplete: function() { updateEstimates(); },
                                },
                            },
                            legend: {
                                labels: { color: '#ccc', usePointStyle: true, padding: 16 }
                            },
                            tooltip: {
                                backgroundColor: '#1a1a2e',
                                borderColor: '#333',
                                borderWidth: 1,
                                titleColor: '#fff',
                                bodyColor: '#ccc',
                                callbacks: {
                                    label: function(ctx) {
                                        return ctx.dataset.label + ': ' + Math.round(ctx.parsed.y) + ' W';
                                    }
                                }
                            }
                        },
                        scales: {
                            x: {
                                type: 'time',
                                time: { unit: 'hour', displayFormats: { hour: 'HH:mm' } },
                                ticks: { color: '#888' },
                                grid: { color: 'rgba(255,255,255,0.05)' }
                            },
                            y: {
                                beginAtZero: true,
                                ticks: {
                                    color: '#888',
                                    callback: function(v) { return v >= 1000 ? (v/1000).toFixed(1) + ' kW' : v + ' W'; }
                                },
                                grid: { color: 'rgba(255,255,255,0.05)' }
                            }
                        }
                    }
                });
                updateEstimates();
            }
        } catch (err) {
            console.error('Failed to load chart:', err);
        }
    }

    document.getElementById('date-picker').addEventListener('change', function() {
        loadChart(this.value);
    });

    document.getElementById('reset-zoom').addEventListener('click', function() {
        if (chart) { chart.resetZoom(); updateEstimates(); }
    });

    loadSummary();
    loadChart(document.getElementById('date-picker').value);
    </script>
</body>
</html>`
