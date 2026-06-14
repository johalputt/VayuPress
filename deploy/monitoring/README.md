# VayuPress Monitoring

Drop-in Prometheus alerts and a Grafana dashboard for a VayuPress instance.
Both reference only metrics actually exported by `GET /metrics`, so nothing here
depends on relabeling or recording rules you have to author yourself.

## Scrape config

VayuPress exposes Prometheus exposition format at `/metrics`. Add a scrape job
(the `job="vayupress"` label is what the alert rules match on):

```yaml
scrape_configs:
  - job_name: vayupress
    metrics_path: /metrics
    static_configs:
      - targets: ["127.0.0.1:8080"]
```

`/metrics` is behind the API key, so scrape with an `Authorization` header:

```yaml
    authorization:
      type: Bearer
      credentials_file: /etc/prometheus/vayupress-api-key
```

## Alerts

Copy `prometheus-alerts.yml` to your rules directory and reference it:

```yaml
rule_files:
  - /etc/prometheus/rules/vayupress-alerts.yml
```

The rules are grouped into availability, latency, reliability, and security.
Thresholds default to the benchmark gates (p95 ≤ 200 ms reads, p99 ≤ 1 s
writes) — tune to your own SLOs. Wire them to Alertmanager as usual; no
VayuPress-specific Alertmanager configuration is required.

## Dashboard

Import `grafana-dashboard.json` in Grafana (Dashboards → New → Import) and pick
your Prometheus datasource when prompted. Panels cover article counts, worker
liveness, cache hit ratio, storage, HTTP/queue latency quantiles, and the
reliability/security counters.
